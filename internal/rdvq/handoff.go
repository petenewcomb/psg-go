// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// Handoff is a lock-free unbuffered rendezvous: a value pushed blocks the sender until a
// receiver takes it directly, with LIFO (warmest-first) consumer selection. It is the
// inbox tier of [Queue] with NO outbox buffering — the scheduler→executor handoff in the
// dispatch/execution split, where a runnable body must rendezvous with an executor (or
// park the scheduler as demand) rather than dwell in a buffer.
//
// It pairs the inbox tier (inboxStackQueue — receiver-owned mailboxes, LIFO) with
// inboxWaiters, the sender-side park that mirrors [Queue.outboxWaiters] (the receiver-side
// park for a filled outbox). The asymmetry is deliberate: there is no outbox, so a send
// to no waiting receiver does not buffer-and-go — it blocks the sender. The whole
// generation-stamped outbox machinery (the hairiest lock-free code in rdvq) is simply
// absent, and there is no TryPopFront (nothing buffered to poll): "unbuffered" ⟺ blocking
// send + no buffer dwell.
//
// Both sides expose a *Func form whose selectFn is the composable "about to park" seam,
// mirroring [Queue.PushBackFunc]/[Queue.PopFrontFunc]: Handoff supplies the rendezvous
// bookkeeping (direct handoff, register-then-recheck park, wake-on-register, inbox
// borrow/reclaim) and calls selectFn only when the operation must block, so the caller
// layers in its own select cases (ctx cancellation, an idle timeout) and side effects
// (spawn-on-demand) without Handoff knowing about any of them. [Handoff.PushBack],
// [Handoff.PopFront], and [Handoff.TryPushBack] are the ctx-only conveniences over those
// seams — both directly useful and worked examples of the *Func forms.
//
// LIFO consumer selection (inboxStack) is load-bearing for scale-to-zero: work lands on
// the most-recently-parked (warmest) receiver, so receivers deeper in the stack go idle
// and time out. Item order to any one receiver is unaffected.
type Handoff[T any] struct {
	inboxes inboxStackQueue[T]
	// inboxWaiters parks a sender whose push found no waiting receiver. A receiver wakes
	// one when it registers its inbox (PopFrontFunc below). Mirror of Queue.outboxWaiters
	// (which parks a receiver waiting for a filled outbox).
	inboxWaiters Waiters
}

// Init initializes the Handoff. Must be called before any other operation.
func (h *Handoff[T]) Init() {
	h.inboxes.Init()
	h.inboxWaiters.Init()
}

// TryPushBack attempts a direct handoff to a waiting receiver without blocking, returning
// false if none is waiting (the non-blocking tier of the rendezvous — the inbox stack with
// no sender park).
func (h *Handoff[T]) TryPushBack(value T) bool {
	return h.inboxes.TryPushBack(value)
}

// PushBackFunc sends value, blocking via selectFn until a receiver takes it. It attempts a
// direct handoff and, on a miss, parks the sender on inboxWaiters — registering this
// sender, then running selectFn (a [WaitSelectFunc]) to do the actual blocking. selectFn
// is the "about to park" seam: it runs only when there was no waiting receiver, so a
// caller fires its spawn-on-demand there and composes the wait (waitCh + ctx + …). The
// park is the standard register-then-recheck: the confirm re-attempts the handoff AFTER
// registering, so a receiver that appears between the failed handoff and the park is taken
// immediately rather than lost.
//
// Returns true once value is delivered; false when selectFn returned without a wake (e.g.
// the caller's ctx was cancelled). A woken sender that loses the rendezvous to a peer
// simply re-parks for the next receiver — senders are never stale, so the wake carries
// no obligation.
//
//nolint:contextcheck // background context used only for tracing
func (h *Handoff[T]) PushBackFunc(value T, selectFn WaitSelectFunc) (delivered bool) {
	traceRegion := "rdvq.Handoff.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Handoff=%p", h)

	for {
		if h.inboxes.TryPushBack(value) {
			return true
		}
		// No waiting receiver: park. WaitFunc registers this sender, runs the confirm
		// (the recheck that closes the miss→register race), then calls selectFn to block.
		received := h.inboxWaiters.WaitFunc(
			func() bool {
				delivered = h.inboxes.TryPushBack(value)
				return !delivered // park only if the recheck still found no receiver
			},
			selectFn,
		)
		if delivered {
			return true
		}
		if !received {
			return false // selectFn exited without a wake (e.g. ctx cancelled)
		}
		// Woken by a registering receiver; loop and retry the handoff. m discarded —
		// senders are never stale, so the wake needs no conservation.
	}
}

// PushBack sends value, blocking until a receiver takes it or ctx is cancelled. The
// ctx-only convenience over [Handoff.PushBackFunc]: its selectFn just blocks on the wait
// channel and ctx. An executor pool layers a spawn-on-demand into its own selectFn instead
// (the block-as-demand hook), which is exactly why the seam exists.
func (h *Handoff[T]) PushBack(ctx context.Context, value T) error {
	var err error
	if h.PushBackFunc(value, func(waitCh <-chan struct{}) bool {
		var received bool
		received, err = BasicWaitSelect(ctx, waitCh)
		return received
	}) {
		return nil
	}
	return err
}

// HandoffPopSelectFunc handles the receiver-side select for [Handoff.PopFrontFunc]. It
// receives the registered inbox's channel and returns the value plus whether one was
// received. received=false means selectFn took some other case (e.g. ctx.Done or an idle
// timeout) and PopFrontFunc reports no value. Mirrors [Queue]'s PopSelectFunc for the
// outbox-free inbox tier.
type HandoffPopSelectFunc[T any] = func(inboxCh <-chan T) (value T, received bool)

// BasicHandoffPopSelect is the stock ctx-only [HandoffPopSelectFunc] body: it selects on
// the inbox channel and ctx.Done(), reporting received=false and ctx.Err() on
// cancellation. A receiver that also wants an idle timeout composes its own selectFn that
// adds the timer case (the executor pool's scale-to-zero), so Handoff needs no idle param.
func BasicHandoffPopSelect[T any](ctx context.Context, inboxCh <-chan T) (value T, received bool, err error) {
	traceRegion := "rdvq.BasicHandoffPopSelect"
	trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p", inboxCh)
	select {
	case v := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received value from inboxCh=%p", inboxCh)
		return v, true, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return value, false, ctx.Err()
	}
}

// PopFrontFunc registers a receiver inbox (borrowed and reclaimed internally) and blocks
// via selectFn until a sender hands off a value or selectFn takes another case. It wakes
// one parked sender right after registering the inbox — the inbox is in the collection by
// then, so the woken sender's handoff finds it (a no-op if none is parked: a sender
// parking later rechecks and finds the inbox via its confirm, so nothing is lost).
//
// Returns the value and true on receipt — including an orphan a racing sender left behind
// — or the zero value and false when selectFn signalled completion without one (e.g. ctx
// cancel or idle timeout). An abandoned inbox is dropped (left in the collection with a
// marker a later sender's TryPushBack drains), exactly as [Queue.PopFrontFunc] does.
//
//nolint:contextcheck // background context used only for tracing
func (h *Handoff[T]) PopFrontFunc(selectFn HandoffPopSelectFunc[T]) (value T, ok bool) {
	traceRegion := "rdvq.Handoff.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Handoff=%p", h)

	ib := h.inboxes.borrowInbox()
	h.inboxes.PopFrontFunc(
		ib,
		func(v T) { value, ok = v, true }, // orphan drain: a racing sender delivered late
		func(ib *inbox[T]) {
			// ib is registered; wake a parked sender so its handoff can take it.
			h.inboxWaiters.Notify(nil)
			if v, received := selectFn(ib.channel()); received {
				ib.emptied()
				value, ok = v, true
			}
		},
	)
	// PopFrontFunc always leaves ib free; the receiver always reclaims it.
	h.inboxes.reclaimInbox(ib)
	return value, ok
}

// PopFront receives a value, blocking until a sender hands one off or ctx is cancelled.
// The ctx-only convenience over [Handoff.PopFrontFunc]: its selectFn just blocks on the
// inbox channel and ctx. An executor pool layers an idle timeout into its own selectFn
// instead (scale-to-zero), which is exactly why the seam exists.
func (h *Handoff[T]) PopFront(ctx context.Context) (T, error) {
	var err error
	value, ok := h.PopFrontFunc(func(inboxCh <-chan T) (T, bool) {
		var v T
		var received bool
		v, received, err = BasicHandoffPopSelect(ctx, inboxCh)
		return v, received
	})
	if ok {
		// A value won the race even if ctx also fired; ctx cancel only affects future ops.
		return value, nil
	}
	var zero T
	return zero, err
}
