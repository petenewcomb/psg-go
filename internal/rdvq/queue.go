// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
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
	outboxes      nbcq.Queue[*outbox[T]] // Borrow source: every live outbox except while checked out
	fullOutboxes  nbcq.Queue[*outbox[T]] // Drain source: outboxes currently holding a value
	outboxFree    sync.Pool              // Reclaimed drained outboxes (scale-to-zero via GC)
	outboxFreed   Listeners              // Queue-level "an outbox freed" wakeup for postponed producers
	outboxWaiters Waiters                // Notification system for new outbox items (receiver side)
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
	q.fullOutboxes.Init()
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
// The sender parameter is vestigial — outbox ownership now lives on the Queue,
// so the destination self-sizes its pool to actual concurrency (see the "rdvq
// Sender redesign" notes). It is retained on the signature pending the
// mechanical removal pass.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PushBackFunc(_ *Sender, value T, bufferedFn BufferedFunc, selectFn PushSelectFunc[T]) {
	traceRegion := "rdvq.Queue.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Queue=%p", q)

	// First try to deliver to a waiting inbox.
	if q.inboxStackQueue.TryPushBack(value) {
		return
	}

	// Borrow an outbox from the destination-owned pool, preferring a full one to
	// block-fill (pacing). An empty one is a drop-and-go.
	ob, full := q.borrowToFill()
	if !full {
		ob.ch <- value // empty, known not to block
		trace.Logf(context.Background(), traceRegion,
			"outbox=%p was empty, delivered value into outboxCh=%p", ob, ob.ch)
		q.publishFilled(ob, bufferedFn)
		return
	}

	// Full: selectFn waits for it to drain (or declines).
	trace.Logf(context.Background(), traceRegion,
		"outbox=%p is full (outboxCh=%p), calling selectFn", ob, ob.ch)
	if !selectFn(ob.ch) {
		// Value was not sent. Return the still-full outbox to the borrow pool;
		// it remains drainable via fullOutboxes (and, if a receiver drained it
		// meanwhile, it is now marked reclaimable and will be treated as empty).
		trace.Logf(context.Background(), traceRegion,
			"selectFn returned without filling outbox=%p (outboxCh=%p)", ob, ob.ch)
		q.outboxes.PushBack(ob)
		return
	}
	q.publishFilled(ob, bufferedFn)
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
func (q *Queue[T]) PushBack(ctx context.Context, sender *Sender, value T, bufferedFn BufferedFunc) error {
	var err error
	q.PushBackFunc(sender, value, bufferedFn, func(outboxCh chan<- T) bool {
		var sent bool
		sent, err = BasicPushSelect(ctx, outboxCh, value)
		return sent
	})
	return err
}

// TryPushBack attempts to send a value without blocking. Returns true if the
// value was delivered (to a waiting receiver, or dropped into an empty outbox),
// false if there was no slack — no waiting receiver and every outbox full. A
// false return is the backpressure signal callers use to postpone; the value is
// re-driven when the queue-level "outbox freed" wakeup fires (see ListenersFor).
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) TryPushBack(_ *Sender, value T, bufferedFn BufferedFunc) bool {
	traceRegion := "rdvq.Queue.TryPushBack"

	// First try to deliver to a waiting inbox.
	if q.inboxStackQueue.TryPushBack(value) {
		trace.Logf(context.Background(), traceRegion, "delivered to waiting inbox")
		return true
	}

	ob, ok := q.tryBorrowEmpty()
	if !ok {
		trace.Logf(context.Background(), traceRegion, "no slack, refusing")
		return false
	}
	ob.ch <- value // empty, known not to block
	trace.Logf(context.Background(), traceRegion, "dropped value into outbox=%p outboxCh=%p", ob, ob.ch)
	q.publishFilled(ob, bufferedFn)
	return true
}

// ListenersFor returns the queue-level "an outbox freed" listener set. A
// producer that could not push (TryPushBack refused) subscribes here and is
// re-driven when a receiver drains an outbox, freeing a buffered slot. This
// replaces the former per-outbox listeners: with destination-owned outboxes
// there is no per-sender outbox to wait on, only the pool as a whole.
//
// The sender parameter is vestigial (see PushBackFunc).
func (q *Queue[T]) ListenersFor(_ *Sender) *Listeners {
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
//	func selectFn(inboxCh <-chan T, outboxWaitCh <-chan RenotifyFunc) (result PopSelectResult[T]) {
//	    select {
//	    case v := <-inboxCh:
//	        result.InboxEmptied(v)
//	    case rf := <-outboxWaitCh:
//	        result.OutboxReady(rf)
//	    case <-ctx.Done():
//	        // result stays zero
//	    }
//	    return
//	}
type PopSelectResult[T any] struct {
	inboxValue       T
	inboxEmptied     bool
	outboxRenotifyFn RenotifyFunc
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
	if r.outboxRenotifyFn != nil {
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
// Panics if renotifyFn is nil, or if InboxEmptied or OutboxReady was
// already called on this result.
func (r *PopSelectResult[T]) OutboxReady(renotifyFn RenotifyFunc) {
	if renotifyFn == nil {
		panic("rdvq.PopSelectResult.OutboxReady: nil renotifyFn")
	}
	if r.inboxEmptied {
		panic("rdvq.PopSelectResult.OutboxReady: InboxEmptied already called")
	}
	if r.outboxRenotifyFn != nil {
		panic("rdvq.PopSelectResult.OutboxReady: already called")
	}
	r.outboxRenotifyFn = renotifyFn
}

// PopSelectFunc handles the select operation for PopFrontFunc when no outbox
// is immediately available. It receives the inbox and outbox-wait channels
// and returns a [PopSelectResult] describing what was received.
type PopSelectFunc[T any] = func(
	inboxCh <-chan T,
	outboxWaitCh <-chan RenotifyFunc,
) PopSelectResult[T]

// BasicPopSelect provides a standard implementation of [PopSelectFunc] that
// selects on the inbox, the outbox-wait channel, and ctx.Done(). Returns the
// result and an error if the context was cancelled.
func BasicPopSelect[T any](
	ctx context.Context,
	inboxCh <-chan T,
	outboxWaitCh <-chan RenotifyFunc,
) (result PopSelectResult[T], err error) {
	traceRegion := "rdvq.BasicPopSelect"
	trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p, outboxWaitCh=%p", inboxCh, outboxWaitCh)
	select {
	case value := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received value from inboxCh=%p", inboxCh)
		result.InboxEmptied(value)
	case renotifyFn := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received signal from outboxWaitCh=%p", outboxWaitCh)
		result.OutboxReady(renotifyFn)
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
// The receiver parameter is vestigial — inbox storage now lives on the Queue,
// which pools inboxes (see borrowInbox). It is retained on the signature pending
// the mechanical removal pass.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PopFrontFunc(
	_ *Receiver,
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
	// at all. The same inbox is reused across retry iterations (preserving the
	// reuse-without-requeue path for its own abandonment marker); reclaim it only
	// when PopFrontFunc last reported it clean (drained and out of the stack).
	var ib *inbox[T]
	ibClean := true
	defer func() {
		if ib != nil && ibClean {
			q.reclaimInbox(ib)
		}
	}()

	var renotifyFn RenotifyFunc
	for {
		if value, ok = q.TryPopFront(); ok {
			return value, true
		}

		if renotifyFn != nil {
			renotifyFn()
		}

		// Register as outbox waiter first; only register ib in the inbox stack
		// if confirmFn says we should actually wait. This makes ib visible to
		// senders only during the wait window itself, eliminating the race
		// where confirmFn consumes an outbox value AND a parallel sender
		// direct-delivers to ib (which would otherwise produce a second
		// orphan value via post-cleanup that the (T, bool) return cannot
		// carry). The inbox is borrowed inside the selectFn (only reached once
		// confirmFn confirms we will wait), so a confirmFn-grab borrows nothing.
		renotifyFn = q.outboxWaiters.WaitFunc(
			nil,
			confirmFn,
			func(waitCh <-chan RenotifyFunc) RenotifyFunc {
				if ib == nil {
					ib = q.borrowInbox()
				}
				var rf RenotifyFunc
				ibClean = q.inboxStackQueue.PopFrontFunc(ib, processOrphanFn, func(ib *inbox[T]) {
					result := selectFn(ib.channel(), waitCh)
					if result.inboxEmptied {
						ib.emptied()
						value = result.inboxValue
						ok = true
					}
					rf = result.outboxRenotifyFn
				})
				return rf
			},
		)
		if ok {
			// Got a value via confirmFn (outbox grab during waiter registration),
			// processOrphanFn (post-selectFn inbox drain), or selectFn (direct
			// inbox receive). If selectFn also returned an outbox-wait
			// notification, forward it so the next waiter isn't stalled — the
			// inbox-drain and direct-inbox paths did NOT consume an outbox, so
			// the notification is still unfulfilled. (For the confirmFn path,
			// renotifyFn is always nil since selectFn never ran.)
			if renotifyFn != nil {
				renotifyFn()
			}
			return value, true
		}
		if renotifyFn == nil {
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
	receiver *Receiver,
) (T, error) {
	var err error
	value, ok := q.PopFrontFunc(receiver, func(inboxCh <-chan T, outboxWaitCh <-chan RenotifyFunc) PopSelectResult[T] {
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

		// Capture the generation before receiving: markReclaimable below sticks
		// only if no producer has refilled (and bumped the generation) since.
		g := outbox.genNow()
		outboxCh := outbox.ch
		trace.Logf(context.Background(), traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case value := <-outboxCh:
			trace.Logf(context.Background(), traceRegion, "received value from outbox=%p outboxCh=%p, returning true",
				outbox, outboxCh)
			if outbox.markReclaimable(g) {
				// A buffered slot truly opened up (no concurrent refill): wake one
				// postponed producer to retry. A failed CAS means a producer
				// refilled the slot, so nothing was freed and no wakeup is owed.
				q.outboxFreed.Notify(nil)
			}
			return value, true
		default:
			trace.Logf(context.Background(), traceRegion, "outbox=%p outboxCh=%p was empty, trying next", outbox, outboxCh)
		}
	}
}
