// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/wavestate"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Limiter is the user-facing concurrency-control primitive. Limiters are
// bound to an op via [WithLimits] at construction time; the op's
// dispatch pipeline acquires a permit before the work runs and releases
// it when the work completes.
//
// Limiters are values, but the underlying state is shared by reference:
// copying a Limiter does not duplicate its permits. Sharing the same
// Limiter across multiple ops makes them compete for the same pool of
// permits.
//
// In v0.x the internal contract is closed — Limiter is a sealed type
// constructed only via framework functions ([NewSemaphore], later
// NewRateLimit, etc.). This keeps the framework free to evolve the
// acquire/notify machinery without breaking users. Custom concurrency
// logic that doesn't fit the built-ins should live inside the user's
// Task or Accumulator body, calling whatever blocking primitive is
// appropriate.
type Limiter struct {
	impl limiterImpl
}

// limiterImpl is the internal contract every Limiter constructor must
// satisfy. It is unexported, so external packages cannot introduce new
// Limiter types. It is implemented by a scheduler — for now only the
// direct scheduler over a single resource.
type limiterImpl interface {
	// newRequest allocates a request handle (state PENDING) for one
	// admission of the given applicant.
	newRequest(a applicant) request
}

// applicant gives a limiter lazy access to the work it is being asked to
// admit. Accessors box only when a limiter actually reads them, so a
// count-based semaphore allocates nothing. The zero applicant (nil) is
// allowed; size-blind resources must not dereference it.
type applicant interface {
	Processor() any // op's Handler or Accumulator, type-assertable to a sizing interface
	Value() any
	Err() error
}

// request is the handle through which the framework drives one admission's
// whole lifecycle. It is limiter-owned and externally serialized: never
// touched concurrently, with every cross-goroutine hand-off carrying a
// happens-before edge through the queue or notifier it travels on (see
// docs/limiter-suspend-resume.md, "Serialization and scoping").
//
// State machine (illegal transitions panic — framework bugs fail loud):
//
//	PENDING ──tryAcquire──► HELD
//	HELD    ──suspend─────► SUSPENDED ──tryResume──► HELD   (mid-body park)
//	HELD    ──postpone────► POSTPONED ──tryAcquire─► HELD   (pre-body yield)
//	any     ──release─────► DONE                            (idempotent)
//
// Capacity-returning transitions (suspend, postpone, release from HELD)
// trigger the scheduler's availability-wakeup; state-discarding ones
// (release from SUSPENDED/POSTPONED) credit and notify nothing.
type request interface {
	// tryAcquire attempts to reach HELD: from PENDING it acquires; from
	// POSTPONED it re-grants, re-taking exactly what postpone released
	// (so a rate dimension is never re-paid). Returns false, state
	// unchanged, if capacity is unavailable.
	tryAcquire() bool
	// postpone yields a grant whose gated work could not start
	// (HELD -> POSTPONED), returning all held capacity.
	postpone()
	// suspend parks a held permit (HELD -> SUSPENDED), returning the
	// suspendable capacity. Returns false without effect if already
	// SUSPENDED — the re-entrancy no-op for nested brackets.
	suspend() bool
	// tryResume reclaims a suspended permit (SUSPENDED -> HELD).
	// Returns false, still SUSPENDED, if capacity is unavailable.
	tryResume() bool
	// release finishes the request from any state (idempotent): give
	// back a HELD grant, abandon a PENDING request, discard a
	// SUSPENDED/POSTPONED one (whose give-back already happened).
	release()
	// notifier returns the wait/notify target for the request's current
	// phase. Never nil.
	notifier() *workq.Notifier
}

// requestState is the request handle's lifecycle state.
type requestState int32

const (
	requestPending requestState = iota
	requestHeld
	requestSuspended
	requestPostponed
	requestDone
)

func (s requestState) String() string {
	switch s {
	case requestPending:
		return "PENDING"
	case requestHeld:
		return "HELD"
	case requestSuspended:
		return "SUSPENDED"
	case requestPostponed:
		return "POSTPONED"
	case requestDone:
		return "DONE"
	default:
		return fmt.Sprintf("requestState(%d)", int32(s))
	}
}

// resource is pure accounting over a capacity — the open extension point
// behind a scheduler. It has no handle, lifecycle, or notification routing;
// availability-wakeup belongs to the scheduler that drives it.
type resource interface {
	// demand reports how much of this resource the applicant needs
	// (1 for a semaphore; bytes for a memory resource).
	demand(a applicant) int
	// tryAcquire deducts amount if it fits.
	tryAcquire(amount int) bool
	// release restores amount — pure accounting, no notify.
	release(amount int)
	// suspendable reports whether holdings are relinquished while the
	// holder is parked mid-body (concurrency: yes; memory: no). A
	// POSTPONED request returns its holdings regardless — nothing has
	// materialized pre-body.
	suspendable() bool
	// setCapacityChangedFn installs the scheduler's bind-time hook for
	// out-of-band capacity growth. The resource must call fn (if
	// non-nil) with the number of newly available slots after raising
	// its capacity; unlimitedCapacityDelta means "now unbounded".
	// Fixed-size resources never call it.
	setCapacityChangedFn(fn func(delta int))
}

// unlimitedCapacityDelta is passed to a capacity-changed hook when a
// resize removes the bound entirely (e.g. SetMaxConcurrency(-1)).
const unlimitedCapacityDelta = math.MaxInt

// directScheduler is the trivial scheduler: one resource, one notifier
// shared by all request phases, first-come discipline. It implements
// limiterImpl for a self-scheduled Limiter.
type directScheduler struct {
	res    resource
	notify workq.Notifier
}

func newDirectScheduler(res resource) *directScheduler {
	s := &directScheduler{res: res}
	s.notify.Init()
	res.setCapacityChangedFn(s.capacityChanged)
	return s
}

func (s *directScheduler) newRequest(a applicant) request {
	r := directRequestPool.Get()
	r.sched = s
	r.amount = s.res.demand(a)
	return r
}

// freeRequest recycles a finished request handle. Must be called exactly
// once, by the work that owns the handle's lifecycle, after the final
// release() — never while any other reference might still drive it.
func freeRequest(req request) {
	if r, ok := req.(*directRequest); ok {
		directRequestPool.Put(r)
	}
}

// notifyAvailable wakes waiters after capacity was returned, one per slot
// until a wake goes undelivered (nobody left to wake).
func (s *directScheduler) notifyAvailable(amount int) {
	for range amount {
		if !s.notify.Notify(nil) {
			break
		}
	}
}

// capacityChanged is the bind-time hook installed on the resource: routes
// out-of-band capacity growth to the full waiter set (parked waiters AND
// postponed listeners).
func (s *directScheduler) capacityChanged(delta int) {
	if delta >= unlimitedCapacityDelta {
		s.notify.NotifyAll()
		return
	}
	s.notifyAvailable(delta)
}

// directRequest is the direct scheduler's request handle. Externally
// serialized (see the request doc); no internal synchronization.
type directRequest struct {
	sched  *directScheduler
	state  requestState
	amount int
	// parkedReleased is the capacity returned at suspend/postpone time,
	// to be re-taken on tryResume/re-grant — and exactly that, so a
	// non-suspendable holding is never double-released and a rate-like
	// dimension is never re-paid.
	parkedReleased int
}

func (r *directRequest) illegal(op string) {
	panic(fmt.Sprintf("limiter request: %s illegal in state %s", op, r.state))
}

func (r *directRequest) tryAcquire() bool {
	switch r.state {
	case requestPending:
		if !r.sched.res.tryAcquire(r.amount) {
			return false
		}
	case requestPostponed:
		if !r.sched.res.tryAcquire(r.parkedReleased) {
			return false
		}
		r.parkedReleased = 0
	default:
		r.illegal("tryAcquire")
	}
	r.state = requestHeld
	return true
}

func (r *directRequest) postpone() {
	if r.state != requestHeld {
		r.illegal("postpone")
	}
	// Pre-body: nothing has materialized, so all holdings return, even
	// ones a mid-body suspend would keep.
	r.sched.res.release(r.amount)
	r.parkedReleased = r.amount
	r.state = requestPostponed
	r.sched.notifyAvailable(r.amount)
}

func (r *directRequest) suspend() bool {
	switch r.state {
	case requestSuspended:
		return false // re-entrant no-op for nested brackets
	case requestHeld:
	default:
		r.illegal("suspend")
	}
	if r.sched.res.suspendable() {
		r.sched.res.release(r.amount)
		r.parkedReleased = r.amount
		r.state = requestSuspended
		r.sched.notifyAvailable(r.amount)
	} else {
		r.parkedReleased = 0
		r.state = requestSuspended
	}
	return true
}

func (r *directRequest) tryResume() bool {
	if r.state != requestSuspended {
		r.illegal("tryResume")
	}
	if r.parkedReleased > 0 {
		if !r.sched.res.tryAcquire(r.parkedReleased) {
			return false
		}
		r.parkedReleased = 0
	}
	r.state = requestHeld
	return true
}

func (r *directRequest) release() {
	switch r.state {
	case requestDone:
		return // idempotent
	case requestPending, requestPostponed:
		// Abandon/discard: nothing held (a postponed grant was already
		// returned in full), so no credit and no notify.
	case requestHeld:
		r.sched.res.release(r.amount)
		r.state = requestDone
		r.sched.notifyAvailable(r.amount)
		return
	case requestSuspended:
		// Only a non-suspendable remainder is still held; the
		// suspendable portion was returned (and notified) at suspend.
		if remainder := r.amount - r.parkedReleased; remainder > 0 {
			r.sched.res.release(remainder)
			r.state = requestDone
			r.sched.notifyAvailable(remainder)
			return
		}
	}
	r.state = requestDone
}

func (r *directRequest) notifier() *workq.Notifier {
	return &r.sched.notify
}

// Reset implements omnipool.Resetter.
func (r *directRequest) Reset() {
	*r = directRequest{}
}

var directRequestPool = omnipool.For[directRequest]()

// acquireOrWait drives a request handle toward HELD under the dispatch
// context's discipline — the routing-plus-blockingAcquire split of what
// workq.ExecuteOrWait did for the pre-handle gates:
//
//   - one-shot (ex.AddToListeners nil): a single tryAcquire attempt;
//   - postpone (no blockFn for this ctx): subscribe to the request's
//     current-phase notifier, recheck, and return — the work is re-invoked
//     on wake, and the subscription survives a false return;
//   - block (top-level): loop tryAcquire under the block-and-help blockFn,
//     re-propagating any unconsumed wake (renotify conservation).
//
// Returns whether the request is HELD on return. tryAcquire is state-aware
// (acquire from PENDING, re-grant from POSTPONED), so retries after a
// postpone() route through here unchanged.
func acquireOrWait(
	ctx context.Context,
	ex workq.Execution,
	deadline time.Time,
	bb workq.BlockBehavior,
	req request,
) (bool, error) {
	if req.tryAcquire() {
		return true, nil
	}
	if !ex.ShouldBlockOrPostpone() {
		return false, nil
	}
	blockFn := bb.ShouldBlock(ctx)
	if blockFn == nil {
		ex.AddToListeners(&req.notifier().Listeners)
		// Recheck in case capacity freed before the subscription was
		// registered and could receive the notification.
		return req.tryAcquire(), nil
	}

	// NOTE: no suspend bracket here. The blocking branch only ever runs
	// inside a top-level dispatch, whose WHOLE span — through the inner
	// post — is one suspend episode bracketed in ctxMeta.ExecuteNowOrQueue.
	// Bracketing just this loop deadlocks on self-acquisition: the
	// deferred reclaim would run after the grant but before the gated
	// work is posted, waiting forever on a task that isn't queued yet.

	b := requestBlockerPool.Get()
	defer requestBlockerPool.Put(b)
	b.req = req
	b.ex = ex

	var renotifyFn workq.RenotifyFunc
	for !b.advance() {
		if renotifyFn != nil {
			// Can't productively use the notification received, so pass
			// it along.
			renotifyFn()
		}
		var err error
		renotifyFn, err = blockFn(ctx, deadline, &req.notifier().Waiters, b.confirmFn)
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// reclaimRequest drives a SUSPENDED request back to HELD at the end of a
// suspend-class episode. It is help-shaped: blockFn is the pool's
// block-and-help wait, so the goroutine keeps draining the skim domain it
// currently drives while waiting for its slot — plain-wait reclaim
// deadlocks under shared limiters (see "Reclaim is help-shaped" in
// docs/limiter-suspend-resume.md). On cancellation it returns with the
// handle still SUSPENDED; the body's completion release discards it (no
// double give-back).
func reclaimRequest(ctx context.Context, blockFn workq.BlockFunc, req request) {
	b := requestBlockerPool.Get()
	defer requestBlockerPool.Put(b)
	b.req = req
	b.resume = true

	helping := true

	var renotifyFn workq.RenotifyFunc
	for !b.advance() {
		if renotifyFn != nil {
			// Can't productively use the notification received, so pass
			// it along.
			renotifyFn()
		}
		var err error
		if helping {
			renotifyFn, err = blockFn(ctx, time.Time{}, &req.notifier().Waiters, b.confirmFn)
			switch {
			case err == nil:
			case ctx.Err() != nil:
				// Canceled: leave the handle SUSPENDED; the body's
				// completion release discards it (no double give-back).
				return
			case errors.Is(err, ErrWaveDone):
				// Help domain exhausted — e.g. the drained subwave this
				// goroutine was driving reports end-of-work. The reclaim
				// becomes vacuously plain: keep waiting on the notifier
				// without help. Abandoning here instead would let the
				// body resume computing UNPERMITTED while a sibling
				// holds the slot.
				helping = false
			default:
				// A handler error surfaced by helped work. The work ran
				// either way, and the reclaim must not abandon the
				// permit; keep helping.
			}
		} else {
			renotifyFn, err = req.notifier().Wait(ctx, b.confirmFn)
			if err != nil {
				return // canceled: leave SUSPENDED, as above
			}
		}
	}
}

// requestBlocker latches the acquire/resume across the blocking loop's
// confirm/advance calls — once HELD, further tryAcquire/tryResume calls
// would be illegal, so the latch is what keeps the loop state-legal.
type requestBlocker struct {
	req            request
	ex             workq.Execution
	resume         bool // advance via tryResume (reclaim) instead of tryAcquire
	held           bool
	blockingCalled bool

	confirmFn func() bool // avoid reallocating closure
}

func (b *requestBlocker) Init() {
	b.confirmFn = b.confirm
}

func (b *requestBlocker) Reset() {
	*b = requestBlocker{
		confirmFn: b.confirmFn,
	}
}

func (b *requestBlocker) advance() bool {
	if !b.held {
		if b.resume {
			b.held = b.req.tryResume()
		} else {
			b.held = b.req.tryAcquire()
		}
	}
	return b.held
}

func (b *requestBlocker) confirm() bool {
	if b.advance() {
		return false
	}
	if !b.blockingCalled {
		b.blockingCalled = true
		if b.ex.Blocking != nil { // nil on the reclaim path (no executor)
			b.ex.Blocking()
		}
	}
	return true
}

var requestBlockerPool = omnipool.For[requestBlocker]()

// suspendForEpisode suspends the calling body's held limiter request, if
// any, at the start of a suspend-class episode (a gather or
// block-and-help wait — see "Where suspend fires" in
// docs/limiter-suspend-resume.md). Returns the request to reclaim at
// episode end, or nil: no enclosing body holds a permit, or the handle is
// already suspended by an enclosing episode (re-entrancy — the reclaim
// belongs to the episode that suspended it).
func suspendForEpisode(meta *ctxMeta) request {
	if r := meta.currentHeldRequest(); r != nil && r.suspend() {
		return r
	}
	return nil
}

// SetMaxConcurrency adjusts the maximum number of simultaneously-held
// permits on a [Semaphore]-backed Limiter. Use n < 0 for unlimited.
// Panics if l is not Semaphore-backed.
//
// Raising the limit immediately unblocks waiters up to the new ceiling;
// lowering it lets in-flight work drain naturally — no permits are
// revoked.
func SetMaxConcurrency(l Limiter, n int) {
	s, ok := l.impl.(*directScheduler)
	if !ok {
		panic("SetMaxConcurrency: Limiter is not Semaphore-backed")
	}
	sem, ok := s.res.(*semaphoreResource)
	if !ok {
		panic("SetMaxConcurrency: Limiter is not Semaphore-backed")
	}
	sem.setMaxConcurrency(n)
}

// NewSemaphore returns a Limiter that grants at most n simultaneous
// permits. Use n < 0 for unlimited; n == 0 blocks all dispatches.
//
// Share one Limiter across ops for a collective cap; pass several to a
// single op's [WithLimits] for AND-composition (admitted jointly in a
// global order, deadlock-free).
func NewSemaphore(n int) Limiter {
	if n < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", n))
	}
	if n > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", n, math.MaxInt32))
	}
	r := &semaphoreResource{}
	r.maxConcurrency.Store(int32(n))
	return Limiter{impl: newDirectScheduler(r)}
}

// semaphoreResource is the in-flight-counter-backed concurrency resource.
// Mirrors the pre-Wave-4 TaskPool semantics: maxConcurrency==-1 means
// unlimited; ==0 blocks everything; >0 caps to that many concurrent
// permits. Pure accounting — wakeups belong to the owning scheduler.
type semaphoreResource struct {
	maxConcurrency    atomic.Int32
	inFlight          wavestate.InFlightCounter
	capacityChangedFn func(delta int)
}

func (s *semaphoreResource) demand(applicant) int {
	return 1
}

func (s *semaphoreResource) tryAcquire(amount int) bool {
	limit := s.maxConcurrency.Load()
	switch {
	case limit < 0:
		for range amount {
			s.inFlight.Increment()
		}
		return true
	case limit == 0:
		return false
	default:
		// amount is always 1 for a semaphore (demand is 1 and the
		// direct scheduler re-takes what it released, which is 1).
		return s.inFlight.IncrementIfUnder(int(limit))
	}
}

func (s *semaphoreResource) release(amount int) {
	for range amount {
		s.inFlight.Decrement()
	}
}

func (s *semaphoreResource) suspendable() bool {
	return true
}

func (s *semaphoreResource) setCapacityChangedFn(fn func(delta int)) {
	s.capacityChangedFn = fn
}

func (s *semaphoreResource) setMaxConcurrency(limit int) {
	if limit < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", limit))
	}
	if limit > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", limit, math.MaxInt32))
	}
	oldLimit := int(s.maxConcurrency.Swap(int32(limit)))
	fn := s.capacityChangedFn
	if fn == nil {
		return
	}
	switch {
	case limit == oldLimit:
	case limit == -1:
		fn(unlimitedCapacityDelta)
	case oldLimit != -1 && limit > oldLimit:
		fn(limit - oldLimit)
	}
}

// limiterScatterWork gates an inner workq.Work behind a Limiter request
// handle. It drives the handle only through the gate phase
// (acquire/re-grant, and postpone when the gated work can't start); the
// handle's lifecycle — release at completion, recycle — is owned by the
// taskWork the request travels with across the queue hand-off.
type limiterScatterWork struct {
	workq.Work
	wave     *Wave
	req      request
	deadline time.Time
}

func newLimiterScatterWork(
	wv *Wave, deadline time.Time, inner workq.Work, req request,
) *limiterScatterWork {
	wk := limiterScatterWorkPool.Get()
	wk.Work = inner
	wk.wave = wv
	wk.req = req
	wk.deadline = deadline
	return wk
}

func (wk *limiterScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	held, err := acquireOrWait(ctx, ex, wk.deadline, wk.wave.protoBB, wk.req)
	if err != nil || !held {
		return err
	}
	err = wk.Work.Execute(ctx, ex)
	if !ex.Started() {
		// Granted, but the inner post couldn't start (downstream queue
		// full under postpone discipline): yield the grant while the work
		// waits for queue space, keeping the request's identity for the
		// re-grant on retry. See "The POSTPONED state" in
		// docs/limiter-suspend-resume.md.
		wk.req.postpone()
	}
	return err
}

func (wk *limiterScatterWork) Free() {
	wk.Work.Free()
	// wk.req is owned by the taskWork (released and recycled in
	// taskWork.Free); just drop the reference via the pool's zeroing Put.
	limiterScatterWorkPool.Put(wk)
}

var limiterScatterWorkPool = omnipool.For[limiterScatterWork]()
