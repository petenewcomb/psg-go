// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/petenewcomb/streampool/internal/trace"
	"github.com/petenewcomb/streampool/internal/workq"
)

// heldPermit is the native limiter handle a body carries through one admission. It
// pairs the body's own wave
// cache for the op's Limiter (ownCache = C_W^L, the reacquire target whose up-walk
// inherits an ancestor's idle permit, then free Resource, then steal) with the Permit
// currently occupying a slot.
//
// A zero permit (Held false) IS the suspended / not-yet-acquired state, so the
// nested-bracket re-entrancy no-op falls out with no separate state enum: suspend lends
// only a held permit; a second suspend on an already-lent handle reports false.
//
// Externally serialized: created at dispatch, acquired
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
	confirmFn func() bool

	// withdrawFn is the handle's bound withdraw-before-going-deep callback
	// (cached/preserved like confirmFn); see [heldPermit.withdraw].
	withdrawFn     func()
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

	// lent marks a permit given back by a SIBLING hold's reclaim park (see
	// reclaim's lend rule): unlike a suspend it carries no drive-target
	// attribution — the permit is plainly released — and it exists so the joint
	// reclaim's fixpoint loop (reclaimJoint) knows this hold needs reacquiring.
	// Cleared by the hold's own reclaim.
	lent bool
}

// waiterPool recycles the park points blocking acquires and reclaims borrow —
// one per park frame, NOT per handle: a reclaim's help can re-enter a reclaim
// of the same handle (the inner bracket's lend rule lends a mid-park-reacquired
// hold away again), and each frame needs its own wait in flight. The armed
// attendant always points at the innermost frame's waiter; a stale wake into a
// recycled waiter is a spurious confirm for its next borrower, harmless by the
// generation protocol.
var waiterPool = omnipool.For[rdvq.Waiter]()

// acquire performs the non-blocking admission acquire through ownCache: own cache, then
// the ancestor up-walk (inherit in place), then the free Resource, then a forest steal.
// Reports whether a permit is now held. Idempotent (latched on Held): a handle already
// occupying a permit returns true without taking a second — so the block loop can call
// it as both its guard and its confirm without double-acquiring. A refusal error
// latches in acquireErr (terminal — see the field), and the loops surface it.
//
//nolint:contextcheck // background context used only for tracing
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
		if trace.IsEnabled() {
			trace.Logf(context.Background(), "heldPermit.acquire",
				"h=%p Pool=%p Demand=%p w=%d acquired", h, h.pool(), &h.demand, h.weight)
		}
	}
	return pm.Held()
}

// suspend lends the held permit back to its backing cache for the duration of a park
// episode (a sub-wave drain), reporting whether there was one to lend. False is the
// re-entrancy no-op: an enclosing episode already suspended this handle, so the reclaim
// belongs to that episode. target is the drained wave's cache for this handle's
// limiter: the suspension counts there BEFORE the permit frees, so no overdraft
// evaluation can observe the lent capacity without the suspension that produced it.
//
//nolint:contextcheck // background context used only for tracing
func (h *heldPermit) suspend(target *permits.Cache) bool {
	if !h.permit.Held() {
		return false
	}
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "heldPermit.suspend", "h=%p Pool=%p target=%p", h, h.pool(), target)
	}
	target.Suspend()
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
// Void so it defers cleanly.
//
// above holds the joint set's HIGHER-rank siblings (nil for a single-limiter handle).
// Before every park, any of them still held is LENT — plainly released, marked for the
// joint fixpoint (reclaimJoint) to reacquire. The park may then hold only the canonical
// BELOW-prefix of the rank order, the same posture a joint admission waits in, so every
// wait-for edge points up-rank and no reclaim can close a deadlock cycle. Without the
// lend, a sibling reacquired out of order (a help-block's confirm lands it while this
// hold was suspended) would be held across a wait for a LOWER rank — the inversion that
// deadlocks against an admitter parked in the canonical posture.
func (h *heldPermit) reclaim(ctx context.Context, wv *waveImpl, above []*heldPermit) {
	if trace.IsEnabled() {
		trace.Logf(ctx, "heldPermit.reclaim", "h=%p Pool=%p Demand=%p wv=%p lent=%v suspended=%v",
			h, h.pool(), &h.demand, wv, h.lent, h.suspendTarget != nil)
	}
	// End the suspend bracket BEFORE reacquiring: the resumed holder becomes a
	// visible parked demand instead of a suspension, which is what lets a standing
	// overdraft evaluation waiting on this suspension proceed (resolution (c)).
	if t := h.suspendTarget; t != nil {
		h.suspendTarget = nil
		t.Resume()
	}
	h.lent = false
	confirmFn := func() bool { return h.acquireErr == nil && !h.acquire() } // block only while still un-acquired
	helping := true
	waiter := waiterPool.Get()
	defer waiterPool.Release(waiter)
	for !h.acquire() {
		if h.acquireErr != nil {
			// Overdraft refusal mid-reclaim: terminal — no wake follows a refusal,
			// so parking would wedge. Leave the handle un-acquired (the completion
			// release no-ops), like the cancellation path. Unreachable at weight-1
			// (see acquireErr); revisit error surfacing with the step-4 weighted
			// surface.
			return
		}
		// The lend rule (see the doc comment): never park holding a rank above the
		// one being waited on — where "holding" covers BOTH a permit and a standing
		// queue registration. A held sibling is lent (released + marked for the
		// joint fixpoint); each Release wakes the sibling pool's head, so a joint
		// admitter parked on that pool proceeds. A registered-but-unheld sibling —
		// an outer frame's reclaim or admission mid-wait — has its demand
		// WITHDRAWN (Invalidate: the FIFO's lazy dequeue; a head's withdrawal
		// promotes and wakes the successor): its queue position, ultimately the
		// pool's headship, reserves capacity just like a permit, and parking here
		// while it stands closes the same inversion cycle through the FIFO — the
		// admitter holding OUR pool's permit waits for THAT slot, while the slot
		// waits for us (trace-confirmed wedge shape). The owning loop re-registers
		// on its next confirm (Acquire re-enqueues an invalidated demand), losing
		// only its queue position — the price of the surrendered slot; each
		// surrender lets a canonical-posture admitter complete, so global progress
		// is preserved.
		for _, y := range above {
			if y.permit.Held() {
				if trace.IsEnabled() {
					trace.Logf(ctx, "heldPermit.reclaim", "h=%p lends y=%p Pool=%p", h, y, y.pool())
				}
				y.permit.Release()
				y.permit = permits.Permit{}
				y.lent = true
			} else if y.demand.Registered() {
				if trace.IsEnabled() {
					trace.Logf(ctx, "heldPermit.reclaim", "h=%p withdraws y=%p Pool=%p", h, y, y.pool())
				}
				y.demand.Invalidate()
			}
		}
		if helping {
			ch := waiter.Prepare()
			h.demand.SetAttendant(waiter)
			woken, err := wv.blockAndHelp(ctx, time.Time{}, ch, h.withdraw(), confirmFn)
			waiter.Finish(woken)
			switch {
			case err == nil:
			case ctx.Err() != nil:
				// Canceled: leave un-acquired (the completion release no-ops) and
				// withdraw the walked-away registration — the owner has stopped
				// attending it, so its reservation must not stand.
				h.demand.Invalidate()
				return
			case errors.Is(err, ErrWaveDone):
				helping = false // help domain exhausted — fall back to a plain park
			default:
				// A handler error surfaced by helped work. The work ran either way and
				// the reclaim must not abandon the permit; keep helping.
			}
		} else {
			ch := waiter.Prepare()
			h.demand.SetAttendant(waiter)
			if !confirmFn() {
				waiter.Finish(false)
				continue
			}
			select {
			case <-ch:
				waiter.Finish(true)
			case <-ctx.Done():
				waiter.Finish(false)
				h.demand.Invalidate() // canceled: walked away, as above
				return
			}
		}
	}
	if trace.IsEnabled() {
		trace.Logf(ctx, "heldPermit.reclaim", "h=%p Pool=%p reacquired", h, h.pool())
	}
}

// reclaimJoint ends the suspend bracket for the whole joint set at drive-episode end,
// reacquiring in canonical global acquisition order (head first, then rest ascending) so
// the reacquire honors the same acyclic order as the original admission.
//
// A bracket reclaims EXACTLY the holds it suspended — each hold's own suspendTarget is
// the record (set by its suspend, cleared by its reclaim) — plus any holds its own
// reclaims LEND along the way (the lent mark; see reclaim's lend rule). The scoping
// matters because brackets nest around a joint set with per-hold state: a reclaim's own
// interior waits (reclaim's help-block, an admission's block-and-help) open inner
// brackets that find some holds held and others mid-reclaim or mid-admission —
// suspending only the held ones. An unconditional joint reclaim on the inner unwind
// would also re-drive holds the outer frame owns, competing with the outer reclaim (or
// the admission loop, which owns a never-suspended hold's acquisition) for the same
// demand — and a demand's mailbox is single-consumer, so the second waiter is a
// dropped-wake wedge.
//
// The loop runs to a fixpoint rather than a single pass: reclaiming a low-rank hold may
// lend a higher one (its parks release every held rank above it), so the scan repeats —
// always resuming from the LOWEST marked hold — until no hold is marked. It terminates
// because each reclaim clears its own mark and a park can mark only ranks above the one
// being reclaimed.
func (h *heldPermit) reclaimJoint(ctx context.Context, wv *waveImpl) {
	needs := func(x *heldPermit) bool { return x.suspendTarget != nil || x.lent }
	for {
		if needs(h) {
			h.reclaim(ctx, wv, h.rest)
			continue
		}
		reclaimed := false
		for i, r := range h.rest {
			if needs(r) {
				r.reclaim(ctx, wv, h.rest[i+1:])
				reclaimed = true
				break // rescan from the lowest rank — this reclaim may have lent
			}
		}
		if !reclaimed {
			return
		}
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
// drive-target attribution the overdraft stranger check reads (resolution (c)).
//
// A reference on wv is held across the ensureCache+SuspendDriver setup — the same guard
// sweepFunnels uses (see Wave.sweepFunnels). Unlike every other ensureCache caller, which
// targets a wave its dispatching goroutine keeps Open by construction, this one targets
// the wave it is DRIVING TO DONE: without the bracket wv can reach Done concurrently, run
// releaseCaches, and recycle the very cache SuspendDriver is pinning (a use-after-free the
// race detector catches on the recycled node's fields). The reference keeps wv Open so the
// cache's self-ref outlives SuspendDriver's ref-add; afterwards the cache survives on that
// pin (dropped by the matching ResumeDriver) independent of wv's own Done.
//
// The pin must be the CONDITIONAL TryIncrementReference, not increment-then-IsDone: an
// increment that resurrects the count from zero cannot stop a Flushing→Done transition
// already in flight on another goroutine (the Done CAS and releaseCaches run regardless),
// while IsDone here still reads the pre-CAS stage — so the suspend would proceed onto a
// cache mid-teardown, and SuspendDriver's ref-add would resurrect a destroyed node into a
// double-destroy that corrupts its next (recycled) owner. TryIncrementReference
// serializes against that transition through the reference count itself (the transition
// claims the zero count before committing), so a failed pin means wv's Done is reached or
// committed: there is nothing to drive (skimAll returns ErrWaveDone), and suspend is a
// no-op rather than resurrecting a forest node on a dead wave. A pin on an idle-but-open
// wave succeeds and holds the wave open — a parked skim driver must keep lending its
// permit there, since work dispatched later may need it.
func suspendHeldPermit(meta *ctxMeta, wv *waveImpl) *heldPermit {
	h := meta.currentHeldPermit()
	if h == nil {
		return nil
	}
	// Engage if ANY hold of the joint set is held — not just the head. The gate must
	// not key on the head alone: mid-reclaimJoint a rest hold's help-block can
	// reacquire that rest hold (its confirm is the acquire) while the head is still
	// un-held, and the unwind's head reclaim then parks again. If that park could not
	// lend the held rest hold, the goroutine would wait for a LOWER-rank permit while
	// holding a HIGHER-rank one — the inversion of the canonical acquisition order —
	// and close a deadlock cycle with any joint admitter parked in the canonical
	// posture (holding the lower rank, waiting on the higher). Lending every held
	// hold makes every reclaim-time wait hold nothing, which is cycle-free
	// unconditionally. (An ADMISSION's mid-sequence holds are unaffected: the gate
	// runs before the body starts, so currentHeldPermit never resolves the set being
	// admitted — mid-sequence holds stay inUse by design.)
	anyHeld := h.permit.Held()
	for _, r := range h.rest {
		anyHeld = anyHeld || r.permit.Held()
	}
	if !anyHeld {
		return nil
	}
	if !wv.state.TryIncrementReference() {
		return nil
	}
	defer wv.state.DecrementReference()
	// Suspend each HELD hold onto ITS OWN drive-target cache (wv's cache for that
	// limiter's Pool). An un-held hold — mid-admission, or mid-reclaim by an outer
	// bracket — is skipped, leaving its suspendTarget nil, which is what scopes the
	// matching reclaimJoint to exactly the holds THIS bracket suspended (see
	// reclaimJoint).
	if h.permit.Held() {
		h.suspend(wv.ensureCache(meta, h.pool()))
	}
	for _, r := range h.rest {
		if r.permit.Held() {
			r.suspend(wv.ensureCache(meta, r.pool()))
		}
	}
	return h
}

// gateAcquire drives h to held under the dispatch context's discipline. It
// implements the three admission modes:
//
//   - one-shot (ex not blocking/postponing): a single non-blocking acquire —
//     a nested dispatch's inline attempt. A miss leaves the demand registered
//     in the pool's arrival-order queue; the work's own queue retries it.
//   - manager postpone (queued, non-top-level): register the work's queue as
//     a listener with the pool's notification domain, then recheck
//     (register-then-confirm closes the lost-wakeup window). A freed permit's
//     token then reaches a worker of the queue holding this work, whose retry
//     sweep re-runs it — or spawns one, on a spawn-capable queue.
//   - top-level: block-and-help — park in the pool's waiter set composed with
//     the wave's own queue, help-executing wave work while waiting, so a
//     permit-holder blocked posting a result keeps making progress (the
//     correctness reason help is required, not just utilization).
//
// Returns whether h holds a permit on return.
func gateAcquire(ctx context.Context, ex workq.Execution, wv *waveImpl, h *heldPermit) (bool, error) {
	if h.acquire() {
		return true, nil
	}
	if trace.IsEnabled() {
		trace.Logf(ctx, "gateAcquire", "h=%p Pool=%p Demand=%p miss", h, h.pool(), &h.demand)
	}
	if h.acquireErr != nil {
		return false, h.acquireErr // overdraft refusal: the unit fails, no retry
	}
	if !ex.ShouldBlockOrPostpone() {
		// One-shot: nothing attends the registration the miss left behind, and
		// this worker may go deep before any retry sweep. A demand may stand
		// only while attended, so withdraw it; the next listen-capable or
		// blocking attempt re-registers. Under a controller (ex.Listener set),
		// plant the queue's interest with the pool's fallback set and re-attempt
		// once (plant-then-recheck), so a capacity event arriving with no
		// standing head wakes a worker to retry this queue's postponed work. An
		// inline nested try (no Listener) leaves the work fresh on the actively
		// driving queue — the driver itself is the attendance.
		h.demand.Invalidate()
		if ex.Listener != nil {
			ex.Listener.AddTo(h.pool().Fallback())
			if h.acquire() {
				return true, nil
			}
			h.demand.Invalidate()
			if h.acquireErr != nil {
				return false, h.acquireErr
			}
		}
		return false, nil
	}
	if wv.shouldBlock(ctx) == nil {
		// Non-top-level: postpone. Arm the driving queue's relay as the
		// registered demand's attendant, then recheck (arm-then-confirm closes
		// the lost-wakeup window). A freed permit's head-directed delivery
		// wakes one worker of the queue holding this work, whose retry sweep
		// re-runs it.
		h.demand.SetAttendant(ex.Listener)
		if h.acquire() {
			return true, nil
		}
		return false, h.acquireErr
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
func (h *heldPermit) acquireJoint(ctx context.Context, ex workq.Execution, wv *waveImpl) (bool, error) {
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

// blockAcquire is the top-level blocking acquire: prepare the handle's waiter,
// arm it as the registered demand's attendant (head-directed delivery fires it
// when capacity frees), and park via wv.blockAndHelp — help-executing the
// caller's own wave, retrying the acquire each round until it succeeds or ctx
// is cancelled. h.acquire (idempotent) is both the loop guard and the block
// confirm, re-run before every inner park.
func blockAcquire(ctx context.Context, ex workq.Execution, wv *waveImpl, h *heldPermit) error {
	// Per-call confirm state, read by h.confirm (the cached, allocation-free callback).
	h.blockingCalled = false
	h.blockingFn = ex.Blocking
	if h.confirmFn == nil {
		h.confirmFn = h.confirm
	}
	defer func() { h.blockingFn = nil }() // don't pin the Execution past the call
	waiter := waiterPool.Get()
	defer waiterPool.Release(waiter)
	for !h.acquire() {
		if h.acquireErr != nil {
			return h.acquireErr // overdraft refusal: terminal, don't park
		}
		ch := waiter.Prepare()
		h.demand.SetAttendant(waiter)
		woken, err := wv.blockAndHelp(ctx, time.Time{}, ch, h.withdraw(), h.confirmFn)
		waiter.Finish(woken)
		if err != nil {
			// Walking away (cancellation, wave done, a helped handler's error
			// surfacing through the help): the owner stops attending, so the
			// registration is withdrawn — its reservation must not outlive its
			// only attendant. A later retry re-registers.
			h.demand.Invalidate()
			return err
		}
	}
	return nil
}

// withdrawDemands invalidates every registered demand of the joint set — the
// withdraw-before-going-deep discipline: a queue position reserves capacity
// just like a permit, so a goroutine must not park while a demand only it can
// attend stands registered. The owning loop's next acquire re-registers,
// losing only queue position (and any gathered hoard, drained by the
// withdrawal).
func (h *heldPermit) withdrawDemands() {
	if h.demand.Registered() {
		h.demand.Invalidate()
	}
	for _, r := range h.rest {
		if r.demand.Registered() {
			r.demand.Invalidate()
		}
	}
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
	h.lent = false
	h.rest = nil // the rest holds are recycled separately by their owner (taskWork.Free)
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

// withdraw returns the handle's cached withdraw-before-going-deep callback for
// [waveImpl.blockAndHelp]: it fires just before the first help item a wait
// executes, invalidating every registered demand of the joint set (the next
// acquire re-registers).
func (h *heldPermit) withdraw() func() {
	if h.withdrawFn == nil {
		h.withdrawFn = h.withdrawDemands
	}
	return h.withdrawFn
}

var heldPermitPool = omnipool.For[heldPermit]()
