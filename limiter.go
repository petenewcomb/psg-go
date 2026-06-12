// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
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

// Scheduler is the coordination point for joint admission across multiple
// Limiters: every Limiter is bound to at most one Scheduler, fixed at
// construction, and all Limiters passed to a single op's [WithLimits] must
// resolve to the same Scheduler. Passing a nil *Scheduler to a Limiter
// constructor means self-scheduled (the Limiter is its own trivial,
// single-member scheduler).
//
// In v0.x there are no Scheduler constructors yet — nil is the only
// supported value. The parameter exists now so that the scheduler's role
// is visible at the moment a Limiter is created.
type Scheduler struct {
	_ [0]func() // opaque; no public constructors yet
}

// limiterImpl is the internal contract every Limiter constructor must
// satisfy. It is unexported, so external packages cannot introduce new
// Limiter types. It is implemented by a scheduler — for now only the
// direct scheduler over a single resource.
type limiterImpl interface {
	// newRequest allocates a request handle (state PENDING) for one
	// admission of the given applicant.
	newRequest(a applicant) request

	// Legacy acquire/release surface, used by the pre-handle gates in
	// limiterScatterWork and funnelWork.Execute.
	// TODO(suspend-resume task #3): delete once the gates drive request
	// handles instead.
	tryAcquire() bool
	release()
	notifier() *workq.Notifier
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
	return &directRequest{
		sched:  s,
		amount: s.res.demand(a),
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

// Legacy acquire/release surface for the pre-handle gates.
// TODO(suspend-resume task #3): delete along with limiterImpl's legacy
// methods. Release notifies unconditionally (the new contract's
// HELD-release rule); the old "only if now under limit" check differed
// only while draining after a SetMaxConcurrency shrink, where the extra
// wakes are benign.
func (s *directScheduler) tryAcquire() bool {
	return s.res.tryAcquire(1)
}

func (s *directScheduler) release() {
	s.res.release(1)
	s.notifyAvailable(1)
}

func (s *directScheduler) notifier() *workq.Notifier {
	return &s.notify
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
// permits. Use n < 0 for unlimited (the same semantics as the
// pre-Wave-4 [NewTaskPool] default). n == 0 blocks all dispatches.
//
// scheduler must be nil (self-scheduled) in v0.x; the parameter exists
// so the scheduler's coordinating role is visible at construction. See
// [Scheduler].
//
// The Semaphore replaces what was a [TaskPool] property in the pre-
// Wave-4 API: WithMaxConcurrency on the pool moves to
// `WithLimits(NewSemaphore(nil, n))` on the op.
func NewSemaphore(scheduler *Scheduler, n int) Limiter {
	if scheduler != nil {
		panic("NewSemaphore: non-nil Scheduler is not yet supported")
	}
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
	inFlight          jobstate.InFlightCounter
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

// limiterScatterWork wraps an inner workq.Work with a Limiter
// acquire-on-enter / release-on-completion gate. Replaces the
// pre-Wave-4 taskPoolScatterWork, which was hard-coded to TaskPool's
// in-flight semaphore.
type limiterScatterWork struct {
	workq.Work
	job      *Pool
	limiter  Limiter
	deadline time.Time
	acquired bool
}

func newLimiterScatterWork(
	job *Pool, deadline time.Time, inner workq.Work, limiter Limiter,
) *limiterScatterWork {
	w := limiterScatterWorkPool.Get()
	w.Work = inner
	w.job = job
	w.limiter = limiter
	w.deadline = deadline
	w.acquired = false
	return w
}

func (w *limiterScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	wb := workq.WaitBehavior{
		BlockBehavior: w.job.protoBB,
		ShouldWait: func() bool {
			if w.acquired {
				return false
			}
			w.acquired = w.limiter.impl.tryAcquire()
			return !w.acquired
		},
	}

	defer func() {
		// If the work didn't start, release the permit we reserved.
		if !ex.Started() && w.acquired {
			w.limiter.impl.release()
			w.acquired = false
		}
	}()

	return workq.ExecuteOrWait(ctx, ex, w.deadline, w.limiter.impl.notifier(), wb,
		func(ctx context.Context, ex workq.Execution) error {
			return w.Work.Execute(ctx, ex)
		})
}

func (w *limiterScatterWork) Free() {
	w.Work.Free()
	limiterScatterWorkPool.Put(w)
}

var limiterScatterWorkPool = omnipool.For[limiterScatterWork]()

// limiterCompletedFn returns the per-task completion callback that
// releases this Limiter's permit. Wired into the task work's
// completedFn so the permit returns at the moment the worker finishes
// executing the user body. Returns nil if l has no impl.
func limiterCompletedFn(l Limiter) func() {
	if l.impl == nil {
		return nil
	}
	return l.impl.release
}
