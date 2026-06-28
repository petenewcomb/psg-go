// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// Handoff is a lock-free unbuffered rendezvous: a value PushBack'd blocks the sender
// until a receiver takes it directly, with LIFO (warmest-first) consumer selection. It
// is the inbox tier of [Queue] with NO outbox buffering — the scheduler→executor handoff
// in the dispatch/execution split, where a runnable body must rendezvous with an executor
// (or park the scheduler as demand) rather than dwell in a buffer.
//
// It pairs the inbox tier (inboxStackQueue — receiver-owned mailboxes, LIFO) with
// inboxWaiters, the sender-side park that mirrors [Queue.outboxWaiters] (the receiver-side
// park for a filled outbox). The asymmetry is deliberate: there is no outbox, so a send
// to no waiting receiver does not buffer-and-go — it blocks the sender. The whole
// generation-stamped outbox machinery (the hairiest lock-free code in rdvq) is simply
// absent, and there is no TryPopFront (nothing buffered to poll): "unbuffered" ⟺ blocking
// send + no buffer dwell.
//
// LIFO consumer selection (inboxStack) is load-bearing for scale-to-zero: work lands on
// the most-recently-parked (warmest) receiver, so receivers deeper in the stack go idle
// and time out. Item order to any one receiver is unaffected.
type Handoff[T any] struct {
	inboxes inboxStackQueue[T]
	// inboxWaiters parks a sender whose PushBack found no waiting receiver. A receiver
	// wakes one when it registers its inbox (PopFront below). Mirror of
	// Queue.outboxWaiters (which parks a receiver waiting for a filled outbox).
	inboxWaiters Waiters
}

// Init initializes the Handoff. Must be called before any other operation.
func (h *Handoff[T]) Init() {
	h.inboxes.Init()
	h.inboxWaiters.Init()
}

// BorrowInbox returns a receiver's reusable mailbox; ReclaimInbox returns it. A
// long-lived receiver borrows once and re-passes the same inbox to each PopFront (which
// reuses it whether or not the prior call left it clean), reclaiming only at exit and
// only when the last PopFront reported clean.
func (h *Handoff[T]) BorrowInbox() *inbox[T] {
	return h.inboxes.borrowInbox()
}

// ReclaimInbox returns a drained inbox (one PopFront reported clean) to the pool.
func (h *Handoff[T]) ReclaimInbox(ib *inbox[T]) {
	h.inboxes.reclaimInbox(ib)
}

// PushBack blocks until value is taken by a receiver, or ctx is cancelled. It attempts a
// direct handoff and, on a miss, parks on inboxWaiters until a receiver registers (which
// wakes it) — the standard register-then-recheck park: Wait runs the confirm AFTER
// registering as a waiter, so a receiver that appears between the failed handoff and the
// park is taken immediately rather than lost. A woken sender that loses the rendezvous to
// a peer simply re-parks for the next receiver — senders are never stale, so no renotify
// conservation is needed (unlike a permit pool's listeners).
//
//nolint:contextcheck // background context used only for tracing
func (h *Handoff[T]) PushBack(ctx context.Context, value T) error {
	traceRegion := "rdvq.Handoff.PushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Handoff=%p", h)

	for {
		if h.inboxes.TryPushBack(value) {
			return nil
		}
		var delivered bool
		_, err := h.inboxWaiters.Wait(ctx, func() bool {
			delivered = h.inboxes.TryPushBack(value)
			return !delivered // park only if the recheck handoff still found no receiver
		})
		if delivered {
			return nil
		}
		if err != nil {
			return err // ctx cancelled while parked
		}
		// Woken by a registering receiver; loop and retry the handoff.
	}
}

// PopFront registers ib as a waiting receiver and blocks until a sender hands off a value
// (processed by processFn) or ctx is cancelled. It wakes one parked sender right after
// registering ib — the inbox is in the collection by then, so the woken sender's handoff
// finds it. Returns clean = true when ib is drained and out of the collection (safe to
// ReclaimInbox or re-pass); clean = false when ib was left abandoned (do not reclaim, but
// safe to re-pass to a later PopFront, which drains the stale marker).
//
//nolint:contextcheck // background context used only for tracing
func (h *Handoff[T]) PopFront(
	ctx context.Context, ib *inbox[T], processFn ProcessValueFunc[T],
) (clean bool, err error) {
	traceRegion := "rdvq.Handoff.PopFront"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Handoff=%p, inbox=%p", h, ib)

	clean = h.inboxes.PopFrontFunc(ib, processFn, func(ib *inbox[T]) {
		// ib is registered (in the collection); wake a parked sender so its handoff can
		// take it. A no-op if no sender is parked — a sender parking later rechecks and
		// finds ib via its confirm, so nothing is lost.
		h.inboxWaiters.Notify(nil)
		err = basicInboxOnlyPopSelect(ctx, ib, processFn)
	})
	return clean, err
}
