// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// BufferedFunc is called when a value is buffered in an outbox rather than
// delivered directly to a receiver. This allows senders to track when their
// values are stored for later pickup.
//
// Ordering guarantee: BufferedFunc runs synchronously on the sender's
// goroutine and completes before the buffered value can be observed by any
// receiver. Callers may therefore rely on side effects of BufferedFunc being
// visible to whichever receiver eventually picks up the value.
type BufferedFunc func()

// outboxHint is a generation-stamped reference to a drained outbox, published on
// emptyOutboxes when a receiver marks an outbox empty. The generation is the one
// the outbox held when the hint was minted; a claimer must claim at exactly that
// generation, so a hint that outlived its incarnation (the outbox refilled, or
// reclaimed to the pool and reused) fails its CAS and is dropped. A bare pointer
// would not suffice: claiming at the outbox's current generation would let a
// stale hint claim a reclaimed outbox sitting in the pool.
type outboxHint[T any] struct {
	ob  *outbox[T]
	gen uint64
}

// Queue implements a rendezvous queue that provides direct handoff between
// senders and receivers with limited buffering, ensuring that senders can always
// make progress (either immediately or with bounded per-sender waiting).
// It can be thought of as a lock-free channel with buffer length 1 per sender.
//
// Queue provides a two-tier performance model:
//  1. Immediate delivery to a waiting receiver's inbox
//  2. Deferred delivery via sender's outbox
//
// The outbox system provides "drop-and-go" semantics for one item at a time
// from each sender, dramatically improving performance under bursty workloads
// while providing clean per-sender backpressure when a sender's outbox is
// already full.
//
// Waiting inboxes have last-in-first-out (LIFO) semantics to enable natural
// worker scaling: the most recently active receiver gets the next item,
// allowing unneeded receivers to remain idle and time out naturally. Items
// themselves are always delivered in first-in-first-out (FIFO) order.
type Queue[T any] struct {
	inboxStackQueue[T]
	outboxes      nbcq.Queue[*outbox[T]]    // All live outboxes (in place; a fill never removes, a blocking pop re-adds)
	emptyOutboxes nbcq.Queue[outboxHint[T]] // Hints to drained outboxes (lossy; validated by a gen-guarded claimEmpty)
	fullOutboxes  nbcq.Queue[*outbox[T]]    // Drain source: outboxes currently holding a value
	outboxPool    *omnipool.Pool[outbox[T]] // Free list for fresh outboxes
	outboxFreed   Listeners                 // Queue-level "an outbox freed" wakeup for postponed producers
	outboxWaiters Waiters                   // Notification system for new outbox items (receiver side)
}

// Init initializes the Queue for use. Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) Init() {
	traceRegion := "rdvq.Queue.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Queue=%p, outboxes=%p, fullOutboxes=%p, outboxFreed=%p, outboxWaiters=%p",
		q, &q.outboxes, &q.fullOutboxes, &q.outboxFreed, &q.outboxWaiters)

	q.inboxStackQueue.Init()
	q.outboxes.Init()
	q.emptyOutboxes.Init()
	q.fullOutboxes.Init()
	q.outboxPool = omnipool.For[outbox[T]]()
	q.outboxFreed.Init()
	q.outboxWaiters.Init()
}

// PushSelectFunc handles the select operation for PushBackFunc when the
// outbox is full. It receives the outbox channel and returns true if it
// successfully sent the value; PushBackFunc handles the post-send bookkeeping
// based on that return.
type PushSelectFunc[T any] = func(outboxCh chan<- T) bool

// BasicPushSelect provides a standard implementation of [PushSelectFunc]
// that selects on the outbox channel and ctx.Done(). Returns (true, nil) on
// successful send; returns (false, err) if the context was cancelled.
func BasicPushSelect[T any](ctx context.Context, outboxCh chan<- T, value T) (bool, error) {
	traceRegion := "rdvq.BasicPushSelect"
	trace.Logf(ctx, traceRegion, "entering select: outboxCh=%p", outboxCh)
	select {
	case outboxCh <- value:
		trace.Logf(ctx, traceRegion, "delivered value into outboxCh=%p", outboxCh)
		return true, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return false, ctx.Err()
	}
}

// PushBackFunc attempts to send a value using the two-tier delivery system:
//  1. Try immediate delivery to a waiting receiver
//  2. If no receiver available and outbox empty: put in outbox and return immediately
//  3. If outbox full: call selectFn to wait for outbox to become available
//
// This method provides "drop-and-go" semantics whenever the destination-owned
// outbox pool has slack, dramatically improving performance under bursty
// workloads.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PushBackFunc(value T, bufferedFn BufferedFunc, selectFn PushSelectFunc[T]) {
	traceRegion := "rdvq.Queue.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Queue=%p", q)

	// First try to deliver to a waiting inbox.
	if q.inboxStackQueue.TryPushBack(value) {
		return
	}

	// Borrow an outbox to fill, claimed in state `filling` at generation g. A full
	// one (block-fill paces); otherwise a freshly allocated empty (drop-and-go).
	ob, g, full := q.borrowToFill()
	if !full {
		ob.ch <- value // fresh empty, known not to block
		trace.Logf(context.Background(), traceRegion,
			"outbox=%p was empty, delivered value into outboxCh=%p", ob, ob.ch)
		q.publishFull(ob, g, bufferedFn, true)
		return
	}

	// Full: selectFn block-fills (paces) or declines.
	trace.Logf(context.Background(), traceRegion,
		"outbox=%p is full (outboxCh=%p), calling selectFn", ob, ob.ch)
	if !selectFn(ob.ch) {
		// Declined (e.g. ctx cancel) without filling. Revert the claim
		// (filling→full at g) and return the outbox to the borrow pool; it is
		// still drainable via fullOutboxes.
		trace.Logf(context.Background(), traceRegion,
			"selectFn returned without filling outbox=%p (outboxCh=%p)", ob, ob.ch)
		ob.state.Store(packState(g, outboxFull))
		q.outboxes.PushBack(ob)
		return
	}
	q.publishFull(ob, g, bufferedFn, true)
}

// PushBack sends a value using the two-tier delivery system with context support.
// This is a convenience wrapper around PushBackFunc that handles context cancellation.
//
// Returns an error only if the context is cancelled before the value can be sent.
// The first overflow item per sender will not block (goes to outbox), subsequent
// overflow items will block waiting for the outbox to become available.
//
// The first overflow item is buffered in the outbox without blocking, allowing
// "drop-and-go" semantics for senders.
func (q *Queue[T]) PushBack(ctx context.Context, value T, bufferedFn BufferedFunc) error {
	var err error
	q.PushBackFunc(value, bufferedFn, func(outboxCh chan<- T) bool {
		var sent bool
		sent, err = BasicPushSelect(ctx, outboxCh, value)
		return sent
	})
	return err
}

// TryPushBack attempts to send a value without blocking. Returns true if the
// value was delivered — to a waiting receiver, or into an outbox that could
// accept it immediately — and false if neither was possible right now. A false
// return is the backpressure signal callers use to postpone; the value is
// re-driven when the queue-level "outbox freed" wakeup fires (see ListenersFor).
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) TryPushBack(value T, bufferedFn BufferedFunc) bool {
	traceRegion := "rdvq.Queue.TryPushBack"

	// First try to deliver to a waiting inbox.
	if q.inboxStackQueue.TryPushBack(value) {
		trace.Logf(context.Background(), traceRegion, "delivered to waiting inbox")
		return true
	}

	// Claim a free outbox via a hint from emptyOutboxes — O(1), no scan. Hints are
	// lossy: a stale one (slot refilled, or the outbox reclaimed and reused) fails
	// the generation-guarded claimEmpty and is dropped.
	for {
		hint, ok := q.emptyOutboxes.TryPopFront()
		if !ok {
			break
		}
		// Claim at the hint's MINTED generation, not the current one. If the outbox
		// has been refilled or reclaimed-and-reused since the hint was minted, its
		// generation has advanced and this CAS fails, dropping the stale hint.
		// Claiming at the current generation would defeat the guard entirely — a
		// hint to a reclaimed outbox sitting in the pool would claim and fill it
		// there, putting it in two places at once.
		if hint.ob.claimEmpty(hint.gen) {
			// Claimed the exact empty incarnation the hint named; its channel is
			// drained (empty at that gen, not refilled), so the send cannot block.
			hint.ob.ch <- value
			q.publishFull(hint.ob, hint.gen, bufferedFn, false)
			// A free outbox was just found and used (slack, not backpressure), so
			// opportunistically reclaim an idle surplus outbox to keep the live set
			// tracking concurrency. Runs after delivery, off the receiver-visible
			// path.
			q.reclaimProbe(hint.ob)
			return true
		}
	}
	if !q.outboxes.Empty() {
		// A live outbox exists but no hint surfaced a free one: genuine
		// backpressure. Refuse so the caller postpones and is re-driven on the
		// outboxFreed wakeup.
		return false
	}

	// No free outbox and none live: allocate one (the concurrency bound).
	fresh := q.obtainOutbox()
	g, _ := fresh.loadState()
	fresh.claimEmpty(g) // uncontended
	fresh.ch <- value
	q.publishFull(fresh, g, bufferedFn, true)
	return true
}

// ListenersFor returns the queue-level "an outbox freed" listener set. A
// producer that could not push (TryPushBack refused) subscribes here and is
// re-driven when a receiver drains an outbox, freeing a buffered slot. This
// replaces the former per-outbox listeners: with destination-owned outboxes
// there is no per-sender outbox to wait on, only the pool as a whole.
func (q *Queue[T]) ListenersFor() *Listeners {
	return &q.outboxFreed
}

// PopSelectResult is the result returned by a PopSelectFunc to communicate
// what happened during the wait. Use [PopSelectResult.InboxEmptied] /
// [PopSelectResult.OutboxReady] to record what fired; the zero value means
// "neither fired" (e.g., context cancellation or idle timeout).
//
// Idiomatic usage is to declare a named return variable of this type and
// call its methods from the matching select case, then bare-return:
//
//	func selectFn(inboxCh <-chan T, outboxWaitCh <-chan Notification) (result PopSelectResult[T]) {
//	    select {
//	    case v := <-inboxCh:
//	        result.InboxEmptied(v)
//	    case m := <-outboxWaitCh:
//	        result.OutboxReady(m)
//	    case <-ctx.Done():
//	        // result stays zero
//	    }
//	    return
//	}
type PopSelectResult[T any] struct {
	inboxValue         T
	inboxEmptied       bool
	outboxNotification Notification
}

// InboxEmptied records that the selectFn received the given value from the
// inbox channel. PopFrontFunc will mark the inbox as emptied on the
// caller's behalf.
//
// Panics if InboxEmptied or OutboxReady was already called on this result.
func (r *PopSelectResult[T]) InboxEmptied(value T) {
	if r.inboxEmptied {
		panic("rdvq.PopSelectResult.InboxEmptied: already called")
	}
	if r.outboxNotification.Received() {
		panic("rdvq.PopSelectResult.InboxEmptied: OutboxReady already called")
	}
	r.inboxValue = value
	r.inboxEmptied = true
}

// OutboxReady records that the selectFn received a notification from the
// outbox-wait channel that an outbox is ready. PopFrontFunc will mark the
// outbox-wait inbox as emptied on the caller's behalf and chain the
// notification.
//
// Panics if the notification is empty, or if InboxEmptied or OutboxReady was
// already called on this result.
func (r *PopSelectResult[T]) OutboxReady(m Notification) {
	if !m.Received() {
		panic("rdvq.PopSelectResult.OutboxReady: notification not received")
	}
	if r.inboxEmptied {
		panic("rdvq.PopSelectResult.OutboxReady: InboxEmptied already called")
	}
	if r.outboxNotification.Received() {
		panic("rdvq.PopSelectResult.OutboxReady: already called")
	}
	r.outboxNotification = m
}

// PopSelectFunc handles the select operation for PopFrontFunc when no outbox
// is immediately available. It receives the inbox and outbox-wait channels
// and returns a [PopSelectResult] describing what was received.
type PopSelectFunc[T any] = func(
	inboxCh <-chan T,
	outboxWaitCh <-chan Notification,
) PopSelectResult[T]

// BasicPopSelect provides a standard implementation of [PopSelectFunc] that
// selects on the inbox, the outbox-wait channel, and ctx.Done(). Returns the
// result and an error if the context was cancelled.
func BasicPopSelect[T any](
	ctx context.Context,
	inboxCh <-chan T,
	outboxWaitCh <-chan Notification,
) (result PopSelectResult[T], err error) {
	traceRegion := "rdvq.BasicPopSelect"
	trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p, outboxWaitCh=%p", inboxCh, outboxWaitCh)
	select {
	case value := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received value from inboxCh=%p", inboxCh)
		result.InboxEmptied(value)
	case m := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received signal from outboxWaitCh=%p", outboxWaitCh)
		result.OutboxReady(m)
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		err = context.Cause(ctx)
	}
	return
}

// PopFrontFunc receives a value using the two-tier delivery system with
// custom select handling. This is the lower-level function that PopFront
// wraps.
//
// First tries to grab a value from any waiting outbox, then registers as an
// inbox waiter and calls selectFn to handle blocking. Returns the value and
// true if one was received via any path; returns the zero value and false
// only if selectFn signalled completion without a value.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PopFrontFunc(
	selectFn PopSelectFunc[T],
) (T, bool) {
	traceRegion := "rdvq.Queue.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Queue=%p", q)

	var value T
	var ok bool
	processOrphanFn := func(v T) {
		if ok {
			panic(traceRegion + ": ok already true in processOrphanFn")
		}
		value = v
		ok = true
	}
	confirmFn := func() bool {
		if ok {
			panic(traceRegion + ": ok already true in confirmFn")
		}
		value, ok = q.TryPopFront()
		return !ok
	}

	// Borrow a data inbox lazily — only when we are actually about to wait — so
	// the fast path (an outbox value is immediately available) touches no inbox
	// at all. The same inbox is reused across retry iterations (each iteration
	// re-registers it free@g → waiting@g; the generation-stamped protocol leaves it
	// free after every PopFrontFunc, so it is always safe to reclaim at the end).
	var ib *inbox[T]
	defer func() {
		if ib != nil {
			q.reclaimInbox(ib)
		}
	}()

	var m Notification
	for {
		if value, ok = q.TryPopFront(); ok {
			return value, true
		}

		if m.Received() {
			m.Forward()
		}

		// Register as outbox waiter first; only register ib in the inbox stack
		// if confirmFn says we should actually wait. This makes ib visible to
		// senders only during the wait window itself, eliminating the race
		// where confirmFn consumes an outbox value AND a parallel sender
		// direct-delivers to ib (which would otherwise produce a second
		// orphan value via post-cleanup that the (T, bool) return cannot
		// carry). The inbox is borrowed inside the selectFn (only reached once
		// confirmFn confirms we will wait), so a confirmFn-grab borrows nothing.
		m = q.outboxWaiters.WaitFunc(
			confirmFn,
			func(waitCh <-chan Notification) Notification {
				if ib == nil {
					ib = q.borrowInbox()
				}
				var outboxN Notification
				q.inboxStackQueue.PopFrontFunc(ib, processOrphanFn, func(ib *inbox[T]) {
					result := selectFn(ib.channel(), waitCh)
					if result.inboxEmptied {
						ib.emptied()
						value = result.inboxValue
						ok = true
					}
					outboxN = result.outboxNotification
				})
				return outboxN
			},
		)
		if ok {
			// Got a value via confirmFn (outbox grab during waiter registration),
			// processOrphanFn (post-selectFn inbox drain), or selectFn (direct
			// inbox receive). If selectFn also returned an outbox-wait
			// notification, forward it so the next waiter isn't stalled — the
			// inbox-drain and direct-inbox paths did NOT consume an outbox, so
			// the notification is still unfulfilled. (For the confirmFn path,
			// m is never received since selectFn never ran.)
			if m.Received() {
				m.Forward()
			}
			return value, true
		}
		if !m.Received() {
			return value, false
		}
	}
}

// PopFront receives a value using the two-tier delivery system with context
// support. This is a convenience wrapper around PopFrontFunc that handles
// context cancellation.
//
// First tries to grab a value from any waiting outbox, then waits for direct
// handoff from senders. Returns the value and a nil error on success; returns
// the zero value and a non-nil error if the context was cancelled before a
// value was available.
func (q *Queue[T]) PopFront(
	ctx context.Context,
) (T, error) {
	var err error
	value, ok := q.PopFrontFunc(func(inboxCh <-chan T, outboxWaitCh <-chan Notification) PopSelectResult[T] {
		var result PopSelectResult[T]
		result, err = BasicPopSelect(ctx, inboxCh, outboxWaitCh)
		return result
	})
	if ok {
		// If both a value and a context error happened, prefer the value: ctx
		// cancellation can only affect future operations, and we already have
		// what the caller asked for.
		return value, nil
	}
	return value, err
}

// TryPopFront attempts to retrieve a value from the queue without blocking.
// It drains the full outboxes for available items.
// Returns the value and true if an item was retrieved, or zero value and false if no items were available.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) TryPopFront() (T, bool) {
	traceRegion := "rdvq.Queue.TryPopFront"

	for {
		outbox, ok := q.fullOutboxes.TryPopFront()
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no full outboxes to try, returning false")
			return *new(T), false // No more outboxes
		}

		// Capture the generation before receiving: markEmpty below sticks only if
		// no producer has refilled (bumping the generation) or is mid-block-fill
		// (claimed filling) since.
		g, _ := outbox.loadState()
		outboxCh := outbox.ch
		trace.Logf(context.Background(), traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case value := <-outboxCh:
			if outbox.markEmpty(g) {
				// A buffered slot truly opened up (full→empty stuck): publish a
				// generation-stamped hint and wake one postponed producer. A failed
				// CAS means a blocking producer is refilling this slot (claimed
				// filling), so it is taken — no hint, no wakeup owed.
				q.emptyOutboxes.PushBack(outboxHint[T]{ob: outbox, gen: g})
				q.outboxFreed.Notify(nil)
			}
			return value, true
		default:
			trace.Logf(context.Background(), traceRegion, "outbox=%p outboxCh=%p was empty, trying next", outbox, outboxCh)
		}
	}
}
