// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/petenewcomb/streampool/internal/workq"
)

// heldPermit is the native limiter handle a body carries through one admission — the
// permit-core replacement for the eager request handle. It pairs the body's own wave
// cache for the op's Limiter (ownCache = C_W^L, the reacquire target whose up-walk
// inherits an ancestor's idle permit, then free Resource, then steal) with the Permit
// currently occupying a slot.
//
// A zero permit (Held false) IS the suspended / not-yet-acquired state, so the
// nested-bracket re-entrancy no-op falls out with no separate state enum: suspend lends
// only a held permit; a second suspend on an already-lent handle reports false.
//
// Externally serialized exactly like the eager request: created at dispatch, acquired
// at the gate, suspended/reclaimed at framework park points on the body's goroutine,
// released at completion — never touched concurrently. Every cross-goroutine hand-off
// (dispatch → gate → body) carries a happens-before edge through the queue the handle
// travels on.
type heldPermit struct {
	ownCache *permits.Cache
	permit   permits.Permit

	// weight is the number of permits this admission acquires — 1 for a plain limiter,
	// or the op's weigher applied to the dispatched value for a weighted limiter. Set at
	// dispatch (newScatterWork); the acquire presents it to every Acquire on this handle.
	weight int

	// rest are the additional per-limiter holds for a MULTI-limiter body, in canonical
	// global acquisition order (ascending pool rank) AFTER this one — i.e. this handle is
	// the lowest-rank limiter and rest holds the higher-rank ones. nil for a single-limiter
	// body or a funnel (the 0-alloc common case). The composition operations (gate, release,
	// suspend/reclaim) iterate this handle then rest, in order; the per-limiter acquire/
	// gather/overdraft logic stays on the individual heldPermit. Joint admission in this
	// fixed order is what keeps the wait-for graph acyclic (weighted-acquisition.md
	// §"Multi-limiter: the FIFO under joint admission").
	rest []*heldPermit

	// demand is the caller-held demand identity this handle presents to every
	// Acquire (weighted-acquisition.md Decision 4): the postpone/retry cycle
	// re-presents the SAME identity, deduping to one FIFO entry once registration
	// lands. It persists across handle recycling — Reset retires the old episode's
	// identity by bumping its generation rather than reallocating.
	demand permits.Demand
	// releaseFn is the handle's own release method value, bound once and reused as a
	// task's per-completion callback (see Wave.newTaskWork). Storing the bound method
	// value lazily here — and preserving it across Reset — keeps a limited dispatch from
	// allocating a fresh method-value closure every time. Bound to the stable pooled
	// pointer, it always acts on the handle's current permit.
	releaseFn func()

	// confirmFn is the handle's bound confirm method value, reused as blockAcquire's
	// block-confirm callback (cached/preserved like releaseFn, so a blocking dispatch
	// does not allocate a fresh closure per call). It reads the per-call confirm state
	// below; blockAcquire is single-flighted per handle (one handle per dispatch), so
	// that state needs no synchronization.
	confirmFn      func() bool
	blockingFn     func() // ex.Blocking for the in-flight blockAcquire (nil if none)
	blockingCalled bool   // whether blockingFn has fired this blockAcquire

	// acquireErr latches an overdraft-refusal error surfaced by Acquire — terminal
	// for this dispatch (the unit fails with the resource's own reason; retrying a
	// refused demand would just re-register it). Sticky so every acquire loop
	// observes it and stops parking; cleared with the demand's episode at Reset.
	// Unreachable while streampool dispatches weight-1 only (a w=1 acquire can be
	// refused only through an overdraft-episode extension, which needs a weighted
	// head first — sequencing step 4), but the plumbing is the end-state API.
	acquireErr error

	// suspendTarget is the drive-target wave's cache for the in-flight suspend
	// bracket (§Overdraft resolution (c)): suspend counts this handle's lend on it,
	// and reclaim ends the suspension there before reacquiring.
	suspendTarget *permits.Cache
}

// acquire performs the non-blocking admission acquire through ownCache: own cache, then
// the ancestor up-walk (inherit in place), then the free Resource, then a forest steal.
// Reports whether a permit is now held. Idempotent (latched on Held): a handle already
// occupying a permit returns true without taking a second — so the block loop can call
// it as both its guard and its confirm without double-acquiring. A refusal error
// latches in acquireErr (terminal — see the field), and the loops surface it.
func (h *heldPermit) acquire() bool {
	if h.permit.Held() {
		return true
	}
	if h.acquireErr != nil {
		return false
	}
	pm, err := h.ownCache.Acquire(&h.demand, h.weight)
	if err != nil {
		h.acquireErr = err
		return false
	}
	if pm.Held() {
		h.permit = pm
	}
	return pm.Held()
}

// suspend lends the held permit back to its backing cache for the duration of a park
// episode (a drive call), reporting whether there was one to lend. False is the
// re-entrancy no-op: an enclosing episode already suspended this handle, so the reclaim
// belongs to that episode. target is the drive-target wave's cache for this handle's
// limiter: the suspension counts there BEFORE the permit frees, so no overdraft
// evaluation can observe the lent capacity without the suspension that produced it.
func (h *heldPermit) suspend(target *permits.Cache) bool {
	if !h.permit.Held() {
		return false
	}
	target.SuspendDriver()
	h.suspendTarget = target
	h.permit.Release()
	h.permit = permits.Permit{}
	return true
}

// reclaim is the suspend bracket's deferred reacquire at the end of a drive episode. It
// is help-shaped: while waiting for a permit it help-drains wv (the wave it was driving),
// so a permit-holder blocked posting a result keeps making progress — a PLAIN-wait
// reclaim deadlocks under shared limiters (the holder can't free its permit until its
// result is skimmed, and nobody else is skimming). When wv's help domain is exhausted
// (ErrWaveDone) the reclaim becomes vacuously plain — keep waiting on the Pool without
// help, since abandoning would let the body resume UNPERMITTED while a sibling holds the
// slot. On cancellation it leaves the handle un-acquired (the completion release no-ops).
// Void so it defers cleanly. Mirrors the eager reclaimRequest.
func (h *heldPermit) reclaim(ctx context.Context, wv *Wave) {
	// End the suspend bracket BEFORE reacquiring: the resumed holder becomes a
	// visible parked demand instead of a suspension, which is what lets a standing
	// overdraft evaluation waiting on this suspension proceed (resolution (c)).
	if t := h.suspendTarget; t != nil {
		h.suspendTarget = nil
		t.ResumeDriver()
	}
	confirmFn := func() bool { return h.acquireErr == nil && !h.acquire() } // block only while still un-acquired
	helping := true
	var m workq.Notification
	for !h.acquire() {
		if h.acquireErr != nil {
			// Overdraft refusal mid-reclaim: terminal — no wake follows a refusal,
			// so parking would wedge. Leave the handle un-acquired (the completion
			// release no-ops), like the cancellation path. Unreachable at weight-1
			// (see acquireErr); revisit error surfacing with the step-4 weighted
			// surface.
			return
		}
		if m.Received() {
			// Couldn't use the wake productively; pass it along (renotify conservation).
			m.Forward()
		}
		var err error
		if helping {
			m, err = wv.block(ctx, time.Time{}, h.ownCache.WaitersFor(&h.demand), confirmFn)
			switch {
			case err == nil:
			case ctx.Err() != nil:
				return // canceled: leave un-acquired; completion release no-ops
			case errors.Is(err, ErrWaveDone):
				helping = false // help domain exhausted — fall back to a plain park
			default:
				// A handler error surfaced by helped work. The work ran either way and
				// the reclaim must not abandon the permit; keep helping.
			}
		} else {
			m, err = h.ownCache.WaitersFor(&h.demand).Wait(ctx, confirmFn)
			if err != nil {
				return // canceled: leave un-acquired, as above
			}
		}
	}
	// Acquired: the last wake (if any) was used productively — drop it (do not
	// Forward). A CHAINED wake additionally owes the chain one probe (rule 2): its
	// multi-permit capacity may satisfy more waiters behind us.
	if m.Chained() {
		h.pool().ChainProbe()
	}
}

// reclaimJoint ends the suspend bracket for the whole joint set at drive-episode end,
// reacquiring each per-limiter hold in canonical global acquisition order (head first, then
// rest ascending) so the reacquire honors the same acyclic order as the original admission.
// Each hold reacquires against a distinct Pool, so the sequencing couples only latency, not
// liveness.
func (h *heldPermit) reclaimJoint(ctx context.Context, wv *Wave) {
	h.reclaim(ctx, wv)
	for _, r := range h.rest {
		r.reclaim(ctx, wv)
	}
}

// pool returns the Pool this handle draws from — the manager-listener target for the
// postpone path.
func (h *heldPermit) pool() *permits.Pool {
	return h.ownCache.Pool()
}

// suspendHeldPermit lends the enclosing body's held permit for the duration of a drive
// episode targeting wave wv, returning the handle to reclaim at episode end, or nil:
// no enclosing body holds a permit, or it is already suspended by an enclosing episode
// (re-entrancy — the reclaim belongs to that episode). The suspension is counted on
// wv's cache for the handle's limiter (mkdir-p'd through ensureCache when absent), the
// drive-target attribution the overdraft stranger check reads (resolution (c)). The
// native replacement for suspendForEpisode.
//
// A reference on wv is held across the ensureCache+SuspendDriver setup — the same guard
// sweepFunnels uses (see Wave.sweepFunnels). Unlike every other ensureCache caller, which
// targets a wave its dispatching goroutine keeps Open by construction, this one targets
// the wave it is DRIVING TO DONE: without the bracket wv can reach Done concurrently, run
// releaseCaches, and recycle the very cache SuspendDriver is pinning (a use-after-free the
// race detector catches on the recycled node's fields). The reference keeps wv Open so the
// cache's self-ref outlives SuspendDriver's ref-add; afterwards the cache survives on that
// pin (dropped by the matching ResumeDriver) independent of wv's own Done. If wv has
// already reached Done — the increment lost the race to the last DecrementReference — there
// is nothing to drive (skimAll returns ErrWaveDone), so suspend is a no-op rather than
// resurrecting a forest node on a dead wave.
func suspendHeldPermit(meta *ctxMeta, wv *Wave) *heldPermit {
	h := meta.currentHeldPermit()
	if h == nil || !h.permit.Held() {
		return nil
	}
	wv.state.IncrementReference()
	defer wv.state.DecrementReference()
	if wv.state.IsDone() {
		return nil
	}
	// Suspend the whole joint set: each per-limiter hold onto ITS OWN drive-target cache
	// (wv's cache for that limiter's Pool). The head is held (checked above) so its
	// suspend takes; the rest are held jointly, so they take too.
	if !h.suspend(wv.ensureCache(meta, h.pool())) {
		return nil
	}
	for _, r := range h.rest {
		r.suspend(wv.ensureCache(meta, r.pool()))
	}
	return h
}

// gateAcquire drives h to held under the dispatch context's discipline — the native
// replacement for acquireOrWait. It mirrors the three admission modes:
//
//   - one-shot (ex not blocking/postponing): a single non-blocking acquire.
//   - manager postpone (queued, non-top-level): register for the Pool's permit-free
//     wake and recheck — the work is re-invoked on a freed permit, and the recheck
//     closes the lost-wakeup window between the miss and the registration.
//   - top-level: block-and-help — wait on the Pool's waiters (woken by any freed permit)
//     while help-draining the driver's own wave, so a permit-holder blocked posting a
//     result keeps making progress (the correctness reason help is required, not just
//     utilization).
//
// Returns whether h holds a permit on return.
func gateAcquire(ctx context.Context, ex workq.Execution, wv *Wave, h *heldPermit) (bool, error) {
	if h.acquire() {
		return true, nil
	}
	if h.acquireErr != nil {
		return false, h.acquireErr // overdraft refusal: the unit fails, no retry
	}
	if !ex.ShouldBlockOrPostpone() {
		return false, nil
	}
	if wv.shouldBlock(ctx) == nil {
		// Non-top-level: postpone. Register for the demand's own wake — its
		// promotion, or the head-addressed capacity events once it is the head
		// (there is no general set under the unified queue) — then recheck, which
		// also covers a registration racing the wake.
		ex.AddToListeners(h.ownCache.ListenersFor(&h.demand))
		return h.acquire(), h.acquireErr
	}
	// Top-level: block-and-help.
	if err := blockAcquire(ctx, ex, wv, h); err != nil {
		return false, err
	}
	return true, nil
}

// acquireJoint drives this handle and all its rest holds to held, in canonical global
// acquisition order (this handle first — the lowest rank — then rest ascending), under the
// dispatch discipline. A mid-sequence block holds the earlier (lower-rank) limiters inUse:
// every wait-for edge points up the order, so no cycle can close. Returns whether ALL are
// held; a miss on any (one-shot or postpone) returns not-held, and the retry re-drives from
// the top where already-held handles pass through idempotently (gateAcquire's acquire is
// latched). An overdraft refusal on any is terminal for the whole admission.
func (h *heldPermit) acquireJoint(ctx context.Context, ex workq.Execution, wv *Wave) (bool, error) {
	held, err := gateAcquire(ctx, ex, wv, h)
	if err != nil || !held {
		return held, err
	}
	for _, r := range h.rest {
		if held, err = gateAcquire(ctx, ex, wv, r); err != nil || !held {
			return held, err
		}
	}
	return true, nil
}

// blockAcquire is the top-level blocking acquire: wait on the Pool's waiters (a freed
// permit wakes one) while wv.block help-drains the driver's own wave, retrying the
// acquire each round until it succeeds or ctx is cancelled. This is the eager
// block-and-help loop retargeted from the per-request notifier onto the permit Pool's
// waiters, with h.acquire (idempotent) as both the loop guard and the block confirm.
func blockAcquire(ctx context.Context, ex workq.Execution, wv *Wave, h *heldPermit) error {
	// Per-call confirm state, read by h.confirm (the cached, allocation-free callback).
	h.blockingCalled = false
	h.blockingFn = ex.Blocking
	if h.confirmFn == nil {
		h.confirmFn = h.confirm
	}
	defer func() { h.blockingFn = nil }() // don't pin the Execution past the call
	var m workq.Notification
	for !h.acquire() {
		if h.acquireErr != nil {
			return h.acquireErr // overdraft refusal: terminal, don't park
		}
		if m.Received() {
			// Couldn't use the wake productively; pass it along (renotify conservation).
			m.Forward()
		}
		var err error
		m, err = wv.block(ctx, time.Time{}, h.ownCache.WaitersFor(&h.demand), h.confirmFn)
		if err != nil {
			return err
		}
	}
	// Acquired: the last wake (if any) was used productively — drop it (do not
	// Forward). A CHAINED wake (weighted release / multi-permit drain) additionally
	// owes the chain one probe: its capacity may satisfy more waiters behind us.
	if m.Chained() {
		h.pool().ChainProbe()
	}
	return nil
}

// release returns the held permit, if any, to its backing cache (now borrowable, waking
// a parked waiter). Idempotent: the suspended/never-acquired handle holds nothing, so a
// completion backstop after a normal release is a safe no-op.
func (h *heldPermit) release() {
	if h.permit.Held() {
		h.permit.Release()
		h.permit = permits.Permit{}
	}
	for _, r := range h.rest {
		r.release()
	}
}

// Reset implements omnipool.Resetter for the handle pool. A recycled handle must hold
// no permit (release ran) and no cache reference. The bound releaseFn/confirmFn are
// preserved — they capture only the (stable) pooled pointer, so they stay valid across
// reuse and need not be re-bound (re-allocated) each cycle. The demand persists too:
// Invalidate retires the episode's identity (any reference a FIFO still holds goes
// stale by generation), and the same storage serves the next dispatch. Field-wise
// rather than a struct literal because the demand's atomic must not be copied over.
func (h *heldPermit) Reset() {
	h.demand.Invalidate()
	h.ownCache = nil
	h.permit = permits.Permit{}
	h.blockingFn = nil
	h.blockingCalled = false
	h.acquireErr = nil
	h.suspendTarget = nil // reclaim always brackets; nil'd here as recycling hygiene
	h.rest = nil          // the rest holds are recycled separately by their owner (taskWork.Free)
}

// confirm is blockAcquire's block-confirm: abort the wait if the permit is now held,
// otherwise fire ex.Blocking once (on the first real park) and proceed to block. Reads
// the per-call state set by blockAcquire. Cached as confirmFn so it costs no allocation.
func (h *heldPermit) confirm() bool {
	if h.acquire() {
		return false // acquired — abort the wait
	}
	if h.acquireErr != nil {
		return false // refusal is terminal — abort the wait; the loop surfaces it
	}
	if !h.blockingCalled {
		h.blockingCalled = true
		if h.blockingFn != nil {
			h.blockingFn()
		}
	}
	return true // proceed to block
}

// Init implements omnipool.Initer: one-time setup when the handle pool creates a
// fresh object — the embedded demand's mailbox outlives every recycle (Reset retires
// identities by generation, never by reallocation).
func (h *heldPermit) Init() {
	h.demand.Init()
}

var heldPermitPool = omnipool.For[heldPermit]()
