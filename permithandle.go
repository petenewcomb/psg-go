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
}

// acquire performs the non-blocking admission acquire through ownCache: own cache, then
// the ancestor up-walk (inherit in place), then the free Resource, then a forest steal.
// Reports whether a permit is now held. Idempotent (latched on Held): a handle already
// occupying a permit returns true without taking a second — so the block loop can call
// it as both its guard and its confirm without double-acquiring.
func (h *heldPermit) acquire() bool {
	if h.permit.Held() {
		return true
	}
	pm, ok := h.ownCache.Acquire()
	if ok {
		h.permit = pm
	}
	return ok
}

// suspend lends the held permit back to its backing cache for the duration of a park
// episode (a drive call), reporting whether there was one to lend. False is the
// re-entrancy no-op: an enclosing episode already suspended this handle, so the reclaim
// belongs to that episode.
func (h *heldPermit) suspend() bool {
	if !h.permit.Held() {
		return false
	}
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
	confirmFn := func() bool { return !h.acquire() } // block only while still un-acquired
	helping := true
	var renotifyFn workq.RenotifyFunc
	for !h.acquire() {
		if renotifyFn != nil {
			// Couldn't use the wake productively; pass it along (renotify conservation).
			renotifyFn()
		}
		var err error
		if helping {
			renotifyFn, err = wv.block(ctx, time.Time{}, h.pool().Waiters(), confirmFn)
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
			renotifyFn, err = h.pool().Waiters().Wait(ctx, confirmFn)
			if err != nil {
				return // canceled: leave un-acquired, as above
			}
		}
	}
}

// pool returns the Pool this handle draws from — the manager-listener target for the
// postpone path.
func (h *heldPermit) pool() *permits.Pool {
	return h.ownCache.Pool()
}

// suspendHeldPermit lends the enclosing body's held permit for the duration of a drive
// episode, returning the handle to reclaim at episode end, or nil: no enclosing body
// holds a permit, or it is already suspended by an enclosing episode (re-entrancy — the
// reclaim belongs to that episode). The native replacement for suspendForEpisode.
func suspendHeldPermit(meta *ctxMeta) *heldPermit {
	if h := meta.currentHeldPermit(); h != nil && h.suspend() {
		return h
	}
	return nil
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
	if !ex.ShouldBlockOrPostpone() {
		return false, nil
	}
	if wv.shouldBlock(ctx) == nil {
		// Non-top-level: postpone. Register for a freed permit, then recheck.
		ex.AddToListeners(h.pool().ListenersFor())
		return h.acquire(), nil
	}
	// Top-level: block-and-help.
	if err := blockAcquire(ctx, ex, wv, h); err != nil {
		return false, err
	}
	return true, nil
}

// blockAcquire is the top-level blocking acquire: wait on the Pool's waiters (a freed
// permit wakes one) while wv.block help-drains the driver's own wave, retrying the
// acquire each round until it succeeds or ctx is cancelled. This is the eager
// block-and-help loop retargeted from the per-request notifier onto the permit Pool's
// waiters, with h.acquire (idempotent) as both the loop guard and the block confirm.
func blockAcquire(ctx context.Context, ex workq.Execution, wv *Wave, h *heldPermit) error {
	blockingCalled := false
	confirmFn := func() bool {
		if h.acquire() {
			return false // acquired — abort the wait
		}
		if !blockingCalled {
			blockingCalled = true
			if ex.Blocking != nil {
				ex.Blocking()
			}
		}
		return true // proceed to block
	}
	var renotifyFn workq.RenotifyFunc
	for !h.acquire() {
		if renotifyFn != nil {
			// Couldn't use the wake productively; pass it along (renotify conservation).
			renotifyFn()
		}
		var err error
		renotifyFn, err = wv.block(ctx, time.Time{}, h.pool().Waiters(), confirmFn)
		if err != nil {
			return err
		}
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
}

// Reset implements omnipool.Resetter for the handle pool. A recycled handle must hold
// no permit (release ran) and no cache reference.
func (h *heldPermit) Reset() {
	*h = heldPermit{}
}

var heldPermitPool = omnipool.For[heldPermit]()
