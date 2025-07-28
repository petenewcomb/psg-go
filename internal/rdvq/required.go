// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type Receiver[T any] struct {
	inbox        Inbox[T]
	outboxWaiter Waiter
}

func (ri *Receiver[T]) waiter() *Waiter {
	return &ri.outboxWaiter
}

type WaiterOrReceiver interface {
	waiter() *Waiter
}

// Required implements a rendezvous queue with guaranteed delivery semantics.
// It extends Optional with outbox buffering, ensuring that senders can always
// make progress (either immediately or with bounded per-sender waiting).
//
// Required provides a two-tier performance model:
//  1. Direct delivery to waiting receivers (fastest)
//  2. Outbox buffering for overflow items per sender (with backpressure)
//
// The outbox system provides "drop-and-go" semantics for the first overflow
// item from each sender, dramatically improving performance under bursty workloads
// while providing clean per-sender backpressure when outboxes are full.
type Required[T any] struct {
	Optional[T]
	fullOutboxes  nbcq.Queue[*Outbox[T]] // Queue of outboxes containing items
	outboxWaiters Waiters                // Notification system for new outbox items
}

// Init initializes the Required queue for use. Must be called before any other operations.
//
// The pool parameter provides channel allocation and recycling to minimize
// garbage collection overhead during high-throughput operations.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) Init() {
	traceRegion := "rdvq.Required.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Required=%p, fullOutboxes=%p, outboxWaiters=%p",
		q, &q.fullOutboxes, &q.outboxWaiters)

	q.Optional.Init()
	q.fullOutboxes.Init()
	q.outboxWaiters.Init()
}

// PushSelectFunc is called when PushBackFunc needs to send a value when the outbox is full.
// It should wait for the outbox to become available, typically using a select statement
// to handle context cancellation and other events. The callback MUST call outbox.Filled()
// if the value was sent successfully. For best scheduler monitoring accuracy, this call
// SHOULD be made immediately after the send.
type PushSelectFunc[T any] = func(outbox *Outbox[T])

// PushBackFunc attempts to send a value using the two-tier delivery system:
//  1. Try immediate delivery to a waiting receiver (via Optional.TryPushBack)
//  2. If no receiver available and outbox empty: put in outbox and return immediately
//  3. If outbox full: call selectFn to wait for outbox to become available
//
// This method provides "drop-and-go" semantics for the first overflow item
// per sender, dramatically improving performance under bursty workloads.
//
// The outbox parameter must be unique per sender. Sharing outboxes between
// senders will break the drop-and-go semantics and may cause data races.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) PushBackFunc(outbox *Outbox[T], value T, selectFn PushSelectFunc[T]) {
	traceRegion := "rdvq.Required.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Required=%p", q)

	// First try to deliver to a waiting inbox
	if q.Optional.TryPushBack(value) {
		return
	}

	if outbox.ch == nil {
		// Outbox is empty, use it for "drop-and-go" semantics
		outbox.ch = q.chanPool.Get()
	}

	outbox.fillPending()
	select {
	case outbox.ch <- value:
		outbox.Filled()
		trace.Logf(context.Background(), traceRegion,
			"outbox=%p was empty, delivered value into outboxCh=%p",
			outbox, outbox.ch)
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
	if !outbox.WasFilled() {
		// Value was not sent via outbox, so there's no further action to take
		trace.Logf(context.Background(), traceRegion,
			"selectFn returned without filling outbox=%p (outboxCh=%p)",
			outbox, outbox.ch)
		return
	}

	// Value was successfully sent to outbox, so queue it and notify waiters
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
func (q *Required[T]) PushBack(ctx context.Context, outbox *Outbox[T], value T) error {
	traceRegion := "rdvq.Required.PushBack"

	var err error
	q.PushBackFunc(outbox, value, func(outbox *Outbox[T]) {
		outboxCh := outbox.Ch()
		trace.Logf(ctx, traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case outboxCh <- value:
			outbox.Filled()
			trace.Logf(ctx, traceRegion, "delivered value into outbox=%p, outboxCh=%p", outbox, outboxCh)
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
		}
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) TryPushBack(outbox *Outbox[T], value T) bool {
	traceRegion := "rdvq.Required.TryPushBack"

	sent := true
	q.PushBackFunc(outbox, value, func(outbox *Outbox[T]) {
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

// RequiredPopSelectFunc handles the select operation for PopFrontFunc when no
// outboxes are available. It should select on the inbox channel and outbox
// filled channel. The callback MUST call inbox.Emptied() if a value is received
// from the inbox. The callback MUST call outboxWaiter's Notified method if a
// RenotifyFunc is received from its channel. For best scheduler monitoring
// accuracy, both calls SHOULD be made immediately after receiving their values.
type RequiredPopSelectFunc[T any] = func(
	inbox *Inbox[T],
	outboxWaiter *Waiter,
)

func (q *Required[T]) tryOutboxes(processFn ProcessValueFunc[T]) bool {
	if value, ok := q.TryPopFront(); ok {
		processFn(value)
		return true
	}
	return false
}

//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) PopFrontFunc(
	receiver *Receiver[T],
	processFn ProcessValueFunc[T],
	selectFn RequiredPopSelectFunc[T],
) {
	traceRegion := "rdvq.Required.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Required=%p", q)

	var ok bool
	processOrphanFn := func(value T) {
		ok = true
		processFn(value)
	}
	confirmFn := func() bool {
		ok = q.tryOutboxes(processFn)
		return !ok
	}

	waitingSelectFn := func(inbox *Inbox[T]) {
		q.outboxWaiters.WaitFunc(&receiver.outboxWaiter, confirmFn, func(waiter *Waiter) {
			selectFn(inbox, waiter)
		})
	}

	for {
		if q.tryOutboxes(processFn) {
			return
		}

		q.Optional.PopFrontFunc(&receiver.inbox, processOrphanFn, waitingSelectFn)
		if ok || !receiver.outboxWaiter.WasNotified() {
			return
		}
		renotifyFn := receiver.outboxWaiter.RenotifyFn()
		renotifyFn()
	}
}

func (q *Required[T]) PopFront(
	ctx context.Context,
	receiver *Receiver[T],
	processFn ProcessValueFunc[T],
) error {
	traceRegion := "rdvq.Required.PopFront"

	var err error
	q.PopFrontFunc(receiver, processFn,
		func(inbox *Inbox[T], outboxWaiter *Waiter) {
			inboxCh := inbox.Ch()
			outboxWaiterCh := outboxWaiter.Ch()
			trace.Logf(ctx, traceRegion, "entering select: inbox=%p, inboxCh=%p, outboxWaiter=%p, outboxWaiterCh=%p",
				inbox, inboxCh, outboxWaiter, outboxWaiterCh)
			select {
			case value := <-inboxCh:
				inbox.Emptied()
				trace.Logf(ctx, traceRegion, "received value from inbox=%p, inboxCh=%p", inbox, inboxCh)
				processFn(value)
			case renotifyFn := <-outboxWaiterCh:
				outboxWaiter.Notified(renotifyFn)
				trace.Logf(ctx, traceRegion, "received signal from outboxWaiter=%p, outboxWaiterCh=%p",
					outboxWaiter, outboxWaiterCh)
			case <-ctx.Done():
				trace.Logf(ctx, traceRegion, "received context done signal")
				err = ctx.Err()
			}
		},
	)
	return err
}

//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) TryPopFront() (T, bool) {
	traceRegion := "rdvq.Required.TryPopFront"

	for {
		outbox, ok := q.fullOutboxes.PopFront()
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no full outboxes to try, returning false")
			return *new(T), false // No more outboxes
		}

		outboxCh := outbox.ch
		trace.Logf(context.Background(), traceRegion, "entering select: outbox=%p, outboxCh=%p", outbox, outboxCh)
		select {
		case value := <-outboxCh:
			outbox.emptied()
			// Can't pool the outbox here, because another goroutine might
			// already be putting something into it
			trace.Logf(context.Background(), traceRegion, "received value from outbox=%p outboxCh=%p, returning true",
				outbox, outboxCh)
			return value, true
		default:
			// Can't pool the outbox even here, again because another goroutine
			// might be putting something into it
			trace.Logf(context.Background(), traceRegion, "outbox=%p outboxCh=%p was empty, trying next", outbox, outboxCh)
		}
	}
}

// PopFrontExcess processes excess work (outboxes) without registering for immediate delivery.
// This is designed for overflow consumers that should handle work that couldn't be
// immediately delivered to waiting consumers.
//
// Unlike PopFrontFunc, this method doesn't register for immediate delivery via
// the Optional queue, making it suitable for spare/overflow goroutines that
// should only process excess work to avoid competing with primary consumers.
//
// Returns an error only if the context is cancelled.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) PopFrontExcessFunc(outboxWaiter WaiterOrReceiver, selectFn WaitSelectFunc) (T, bool) {
	traceRegion := "rdvq.Required.PopFrontExcessFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Required=%p", q)

	waiter := outboxWaiter.waiter()
	for {
		value, ok := q.TryPopFront()
		if ok {
			return value, true
		}
		if waiter.WasNotified() {
			// We could not productively use the notification, so pass it on. Do so
			// before starting wait to avoid an infinite renotification loop.
			renotifyFn := waiter.RenotifyFn()
			renotifyFn()
		}

		confirmFn := func() bool {
			value, ok = q.TryPopFront()
			return !ok
		}
		q.outboxWaiters.WaitFunc(waiter, confirmFn, selectFn)
		if ok || !waiter.WasNotified() {
			return value, ok
		}
	}
}

// PopFrontExcess processes excess work (outboxes) with context support
// without registering for immediate delivery. This is a convenience wrapper around
// PopFrontExcessFunc that handles context cancellation.
//
// Returns the received value and an error. The error is non-nil only if the context
// is cancelled before a value can be received. This method only processes excess work
// that couldn't be immediately delivered to waiting consumers.
func (q *Required[T]) PopFrontExcess(ctx context.Context, outboxWaiter WaiterOrReceiver) (T, error) {
	traceRegion := "rdvq.Required.PopFrontExcessFunc"

	var err error
	value, _ := q.PopFrontExcessFunc(outboxWaiter, func(waiter *Waiter) {
		outboxWaiterCh := waiter.Ch()
		trace.Logf(ctx, traceRegion, "entering select: waiter=%p, outboxWaiterCh=%p", waiter, outboxWaiterCh)
		select {
		case renotifyFn := <-outboxWaiterCh:
			waiter.Notified(renotifyFn)
			trace.Logf(ctx, traceRegion, "received signal from waiter=%p, outboxWaiterCh=%p", waiter, outboxWaiterCh)
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
		}
	})
	return value, err
}
