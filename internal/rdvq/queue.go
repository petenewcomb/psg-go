// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

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
	fullOutboxes  nbcq.Queue[*Outbox[T]] // Queue of outboxes containing items
	outboxWaiters Waiters                // Notification system for new outbox items
}

// Init initializes the Queue for use. Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) Init() {
	traceRegion := "rdvq.Queue.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Queue=%p, fullOutboxes=%p, outboxWaiters=%p",
		q, &q.fullOutboxes, &q.outboxWaiters)

	q.inboxStackQueue.Init()
	q.fullOutboxes.Init()
	q.outboxWaiters.Init()
}

// PushSelectFunc is called when PushBackFunc needs to send a value when the outbox is full.
// It should wait for the outbox to become available, typically using a select statement
// to handle context cancellation and other events. The callback MUST call outbox.Filled()
// if the value was sent successfully.
type PushSelectFunc[T any] = func(outbox *Outbox[T])

// BasicPushSelect provides a standard implementation of PushSelectFunc that waits
// for the outbox to become available or the context to be cancelled.
//
// Parameters:
//   - ctx: Context for cancellation
//   - outbox: The outbox to send the value to
//   - value: The value to send
//
// Returns an error only if the context is cancelled. Automatically calls
// outbox.Filled() when the value is successfully sent.
func BasicPushSelect[T any](ctx context.Context, outbox *Outbox[T], value T) error {
	traceRegion := "rdvq.BasicPushSelect"
	outboxCh := outbox.Ch()
	trace.Logf(ctx, traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
	select {
	case outboxCh <- value:
		outbox.Filled()
		trace.Logf(ctx, traceRegion, "delivered value into outbox=%p, outboxCh=%p", outbox, outboxCh)
		return nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return ctx.Err()
	}
}

// PushBackFunc attempts to send a value using the two-tier delivery system:
//  1. Try immediate delivery to a waiting receiver
//  2. If no receiver available and outbox empty: put in outbox and return immediately
//  3. If outbox full: call selectFn to wait for outbox to become available
//
// This method provides "drop-and-go" semantics for the first overflow item
// per sender, dramatically improving performance under bursty workloads.
//
// The sender parameter manages per-sender outboxes. Each goroutine must use
// its own Sender instance to avoid races.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PushBackFunc(sender *Sender, value T, bufferedFn BufferedFunc, selectFn PushSelectFunc[T]) {
	traceRegion := "rdvq.Queue.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Queue=%p", q)

	// First try to deliver to a waiting inbox
	if q.inboxStackQueue.TryPushBack(value) {
		return
	}

	outbox := outboxFor(sender, q)
	outbox.fillPending()
	defer outbox.fillAttemptComplete()
	select {
	case outbox.ch <- value:
		outbox.Filled()
		trace.Logf(context.Background(), traceRegion,
			"outbox=%p was empty, delivered value into outboxCh=%p",
			outbox, outbox.ch)
		if bufferedFn != nil {
			bufferedFn()
		}
		q.fullOutboxes.PushBack(outbox)
		q.outboxWaiters.Notify(nil)
		return
	default:
	}

	// Outbox is full, wait for it to become available
	trace.Logf(context.Background(), traceRegion,
		"outbox=%p is full (outboxCh=%p), calling selectFn",
		outbox, outbox.ch)
	selectFn(outbox)
	if !outbox.wasFilled {
		// Value was not sent via outbox, so there's no further action to take
		trace.Logf(context.Background(), traceRegion,
			"selectFn returned without filling outbox=%p (outboxCh=%p)",
			outbox, outbox.ch)
		return
	}

	// Value was successfully sent to outbox, so queue it and notify waiters
	if bufferedFn != nil {
		bufferedFn()
	}
	q.fullOutboxes.PushBack(outbox)
	q.outboxWaiters.Notify(nil)
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
	q.PushBackFunc(sender, value, bufferedFn, func(outbox *Outbox[T]) {
		err = BasicPushSelect(ctx, outbox, value)
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) TryPushBack(sender *Sender, value T, bufferedFn BufferedFunc) bool {
	traceRegion := "rdvq.Queue.TryPushBack"

	sent := true
	q.PushBackFunc(sender, value, bufferedFn, func(outbox *Outbox[T]) {
		// Don't block waiting for outbox to become available
		outboxCh := outbox.Ch()
		trace.Logf(context.Background(), traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case outboxCh <- value:
			outbox.Filled()
			trace.Logf(context.Background(), traceRegion, "delivered value into outbox=%p, outboxCh=%p", outbox, outboxCh)
		default:
			trace.Logf(context.Background(), traceRegion, "outbox=%p, outboxCh=%p full, aborting", outbox, outboxCh)
			sent = false
		}
	})

	trace.Logf(context.Background(), traceRegion, "returning %v", sent)
	return sent
}

// Listeners returns the outbox's listeners for subscription to availability notifications.
func (q *Queue[T]) ListenersFor(s *Sender) *Listeners {
	return &outboxFor(s, q).listeners
}

// PopSelectFunc handles the select operation for PopFrontFunc when no
// outboxes are available. It should select on the inbox channel and outbox
// filled channel. If a value is received from the inbox the callback MUST call
// inbox.Emptied(). If a RenotifyFunc is received from the outboxWaiter the
// callback MUST call outboxWaiter.Notified() and SHOULD return the RenotifyFunc
// or otherwise ensure that it will be called if the notification cannot
// otherwise be productively used.
type PopSelectFunc[T any] = func(
	inbox *Inbox[T],
	outboxWaitInbox *WaitInbox,
) RenotifyFunc

// BasicPopSelect provides a standard implementation of PopSelectFunc that handles
// context cancellation and waits for either an inbox delivery or outbox notification.
//
// Parameters:
//   - ctx: Context for cancellation
//   - processFn: Function called if a value is received from the inbox
//   - inbox: The inbox to receive from
//   - outboxWaitInbox: Wait inbox for outbox notifications
//
// Returns a RenotifyFunc if an outbox notification was received, or an error
// if the context was cancelled. Calls processFn if a value is received from inbox.
func BasicPopSelect[T any](
	ctx context.Context,
	processFn ProcessValueFunc[T],
	inbox *Inbox[T],
	outboxWaitInbox *WaitInbox,
) (RenotifyFunc, error) {
	traceRegion := "rdvq.BasicPopSelect"
	inboxCh := inbox.Ch()
	outboxWaitInboxCh := outboxWaitInbox.Ch()
	trace.Logf(ctx, traceRegion, "entering select: inbox=%p, inboxCh=%p, outboxWaitInbox=%p, outboxWaitInboxCh=%p",
		inbox, inboxCh, outboxWaitInbox, outboxWaitInboxCh)
	select {
	case value := <-inboxCh:
		inbox.Emptied()
		trace.Logf(ctx, traceRegion, "received value from inbox=%p, inboxCh=%p", inbox, inboxCh)
		processFn(value)
	case renotifyFn := <-outboxWaitInboxCh:
		outboxWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received signal from outboxWaitInbox=%p, outboxWaitInboxCh=%p",
			outboxWaitInbox, outboxWaitInboxCh)
		return renotifyFn, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return nil, ctx.Err()
	}
	return nil, nil
}

func (q *Queue[T]) tryOutboxes(processFn ProcessValueFunc[T]) bool {
	if value, ok := q.TryPopFront(); ok {
		processFn(value)
		return true
	}
	return false
}

// PopFrontFunc receives values using the two-tier delivery system with custom
// select handling. This is the lower-level function that PopFront wraps.
//
// Parameters:
//   - receiver: Receiver instance managing inboxes for this goroutine
//   - processFn: Function called with each received value
//   - selectFn: Custom select function for handling blocking and cancellation
//
// It first tries to get items from outboxes, then calls selectFn to handle
// waiting for direct handoff from senders. May call processFn multiple times
// if multiple items are received (i.e., an item retrieved from an outbox plus
// an item delivered to the receiver's inbox).
//
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PopFrontFunc(
	receiver *Receiver,
	processFn ProcessValueFunc[T],
	selectFn PopSelectFunc[T],
) {
	traceRegion := "rdvq.Queue.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Queue=%p", q)

	if processFn == nil {
		panic("processFn is nil")
	}

	var ok bool
	processOrphanFn := func(value T) {
		ok = true
		processFn(value)
	}
	confirmFn := func() bool {
		ok = q.tryOutboxes(processFn)
		return !ok
	}

	inbox := inboxFor(receiver, q)
	var renotifyFn RenotifyFunc
	for {
		if q.tryOutboxes(processFn) {
			// We grabbed an outbox value. If we held a pending renotifyFn from
			// a prior iteration's outbox-wait notification, forward it — it
			// may have been for a different outbox than the one we just took.
			if renotifyFn != nil {
				renotifyFn()
			}
			return
		}

		if renotifyFn != nil {
			renotifyFn()
			renotifyFn = nil
		}

		q.inboxStackQueue.PopFrontFunc(inbox, processOrphanFn, func(inbox *Inbox[T]) {
			q.outboxWaiters.WaitFunc(&receiver.outboxWaiter, confirmFn, func(waitInbox *WaitInbox) {
				renotifyFn = selectFn(inbox, waitInbox)
			})
		})
		if ok {
			// Got a value. If selectFn also picked up an outbox-wait
			// notification, forward it so the next waiter isn't stalled.
			if renotifyFn != nil {
				renotifyFn()
			}
			return
		}
		if renotifyFn == nil {
			return
		}
	}
}

// PopFront receives values using the two-tier delivery system with context
// support. This is a convenience wrapper around PopFrontFunc that handles
// context cancellation.
//
// Parameters:
//   - ctx: Context for cancellation
//   - receiver: Receiver instance managing inboxes for this goroutine
//   - processFn: Function called with each received value
//
// It first tries to get items from outboxes, then waits for direct handoff
// from senders. May call processFn multiple times if multiple items are
// received (i.e., an item retrieved from an outbox plus an item delivered
// to the receiver's inbox). Returns an error only if the context is cancelled.
func (q *Queue[T]) PopFront(
	ctx context.Context,
	receiver *Receiver,
	processFn ProcessValueFunc[T],
) error {
	var err error
	q.PopFrontFunc(receiver, processFn, func(inbox *Inbox[T], outboxWaitInbox *WaitInbox) RenotifyFunc {
		var renotifyFn RenotifyFunc
		renotifyFn, err = BasicPopSelect(ctx, processFn, inbox, outboxWaitInbox)
		if err != nil && renotifyFn != nil {
			// Break out of PopFrontFunc retry loop
			renotifyFn()
			return nil
		}
		return renotifyFn
	})
	return err
}

// TryPopFront attempts to retrieve a value from the queue without blocking.
// It checks all full outboxes for available items.
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

		outboxCh := outbox.ch
		trace.Logf(context.Background(), traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case value := <-outboxCh:
			trace.Logf(context.Background(), traceRegion, "received value from outbox=%p outboxCh=%p, returning true",
				outbox, outboxCh)
			outbox.emptied()
			return value, true
		default:
			trace.Logf(context.Background(), traceRegion, "outbox=%p outboxCh=%p was empty, trying next", outbox, outboxCh)
		}
	}
}
