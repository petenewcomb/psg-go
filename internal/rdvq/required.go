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
	fullOutboxes  nbcq.Queue[chan T] // Queue of channels containing outboxed items
	outboxWaiters Waiters            // Notification system for new outbox items
}

// Init initializes the Required queue for use. Must be called before any other operations.
//
// The pool parameter provides channel allocation and recycling to minimize
// garbage collection overhead during high-throughput operations.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) Init(p *Pool[T]) {
	traceRegion := "rdvq.Required.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Required=%p, fullOutboxes=%p, outboxWaiters=%p",
		q, &q.fullOutboxes, &q.outboxWaiters)

	q.Optional.Init(p)
	q.fullOutboxes.Init(&p.nodePool)
	q.outboxWaiters.Init()
}

// PushSelectFunc is called when PushBackFunc needs to send a value when the outbox is full.
// It should wait for the outbox to become available, typically using a select statement
// to handle context cancellation and other events. Must return SelectOutboxFilled if the value was sent,
// SelectAborted if not.
type PushSelectFunc[T any] = func(outboxCh chan<- T) SelectResult

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
func (q *Required[T]) PushBackFunc(p *Pool[T], outbox *Outbox[T], value T, selectFn PushSelectFunc[T]) {
	traceRegion := "rdvq.Required.PushBackFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Required=%p", q)

	// First try to deliver to a waiting inbox
	if q.Optional.TryPushBack(p, value) {
		return
	}

	if outbox.ch == nil {
		// Outbox is empty, use it for "drop-and-go" semantics
		outbox.ch = p.getChan()
		outbox.ch <- value
		trace.Logf(context.Background(), traceRegion,
			"outbox=%p was empty, delivered value into outboxCh=%p",
			outbox, outbox.ch)
		q.fullOutboxes.PushBack(&p.nodePool, outbox.ch)
		q.outboxWaiters.Notify(nil)
		return
	}

	// Outbox is full, wait for it to become available
	trace.Logf(context.Background(), traceRegion,
		"outbox=%p is full (outboxCh=%p), calling selectFn",
		outbox, outbox.ch)
	if selectFn(outbox.ch) != SelectOutboxFilled {
		// Value was not sent via outbox, so there's no further action to take
		trace.Logf(context.Background(), traceRegion,
			"selectFn returned without filling outbox=%p (outboxCh=%p)",
			outbox, outbox.ch)
		return
	}

	// Value was successfully sent to outbox, so queue it and notify waiters
	q.fullOutboxes.PushBack(&p.nodePool, outbox.ch)
	q.outboxWaiters.Notify(nil)
}

// PushBack sends a value using the two-tier delivery system with context support.
// This is a convenience wrapper around PushBackFunc that handles context cancellation.
//
// Returns an error only if the context is cancelled before the value can be sent.
// The first overflow item per sender will not block (goes to outbox), subsequent
// overflow items will block waiting for the outbox to become available.
//
// Important: After calling PushBack, callers should typically call outbox.Wait()
// to ensure their outboxed item has been processed before the sender exits.
func (q *Required[T]) PushBack(ctx context.Context, p *Pool[T], outbox *Outbox[T], value T) error {
	traceRegion := "rdvq.Required.PushBack"

	var err error
	q.PushBackFunc(p, outbox, value, func(outboxCh chan<- T) SelectResult {
		trace.Logf(ctx, traceRegion, "entering select: outboxCh=%p", outboxCh)
		select {
		case outboxCh <- value:
			trace.Logf(ctx, traceRegion, "delivered value into outboxCh=%p", outboxCh)
			return SelectOutboxFilled
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
//
//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) TryPushBack(p *Pool[T], outbox *Outbox[T], value T) bool {
	traceRegion := "rdvq.Required.TryPushBack"

	sent := true
	q.PushBackFunc(p, outbox, value, func(outboxCh chan<- T) SelectResult {
		// Don't block waiting for outbox to become available
		trace.Logf(context.Background(), traceRegion, "entering select: outboxCh=%p", outboxCh)
		select {
		case outboxCh <- value:
			trace.Logf(context.Background(), traceRegion, "delivered value into outboxCh=%p", outboxCh)
			return SelectOutboxFilled
		default:
			trace.Logf(context.Background(), traceRegion, "outboxCh=%p full, aborting", outboxCh)
		}
		sent = false
		return SelectAborted
	})

	trace.Logf(context.Background(), traceRegion, "returning %v", sent)
	return sent
}

// RequiredPopSelectFunc handles the select operation for PopFrontFunc when no
// outboxes are available. It should select on the inbox channel and outbox
// filled channel, returning the appropriate result to indicate what happened.
type RequiredPopSelectFunc[T any] = func(
	inboxCh <-chan T,
	outboxFilledCh <-chan RenotifyFunc,
) (SelectResult, RenotifyFunc)

func (q *Required[T]) tryOutboxes(p *Pool[T], processFn ProcessValueFunc[T]) bool {
	if value, ok := q.TryPopFront(p); ok {
		processFn(value)
		return true
	}
	return false
}

//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) PopFrontFunc(
	p *Pool[T],
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
		ok = q.tryOutboxes(p, processFn)
		return !ok
	}

	var result SelectResult
	waitingSelectFn := func(inboxCh <-chan T) SelectResult {
		q.outboxWaiters.WaitFunc(&receiver.outboxWaiter, confirmFn, func(outboxFilledCh <-chan RenotifyFunc) RenotifyFunc {
			var renotifyFn RenotifyFunc
			result, renotifyFn = selectFn(inboxCh, outboxFilledCh)
			return renotifyFn
		})
		return result
	}

	for {
		if q.tryOutboxes(p, processFn) {
			return
		}

		q.Optional.PopFrontFunc(p, &receiver.inbox, processOrphanFn, waitingSelectFn)
		if ok || result != SelectOutboxFilled {
			return
		}
	}
}

func (q *Required[T]) PopFront(
	ctx context.Context,
	p *Pool[T],
	receiver *Receiver[T],
	processFn ProcessValueFunc[T],
) error {
	traceRegion := "rdvq.Required.PopFront"

	var err error
	q.PopFrontFunc(p, receiver, processFn,
		func(inboxCh <-chan T, outboxFilledCh <-chan RenotifyFunc) (SelectResult, RenotifyFunc) {
			trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p, outboxFilledCh=%p", inboxCh, outboxFilledCh)
			select {
			case value := <-inboxCh:
				trace.Logf(ctx, traceRegion, "received value from inboxCh=%p", inboxCh)
				processFn(value)
				return SelectInboxEmptied, nil
			case renotifyFn := <-outboxFilledCh:
				trace.Logf(ctx, traceRegion, "received signal from outboxFilledCh=%p", outboxFilledCh)
				return SelectOutboxFilled, renotifyFn
			case <-ctx.Done():
				trace.Logf(ctx, traceRegion, "received context done signal")
				err = ctx.Err()
				return SelectAborted, nil
			}
		},
	)
	return err
}

//nolint:contextcheck // background context used only for tracing
func (q *Required[T]) TryPopFront(p *Pool[T]) (T, bool) {
	traceRegion := "rdvq.Required.TryPopFront"

	for {
		outboxCh, ok := q.fullOutboxes.PopFront(&p.nodePool)
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no full outboxes to try, returning false")
			return *new(T), false // No more outboxes
		}

		trace.Logf(context.Background(), traceRegion, "entering select: outboxCh=%p", outboxCh)
		select {
		case value := <-outboxCh:
			trace.Logf(context.Background(), traceRegion, "received value from outboxCh=%p, returning true", outboxCh)
			return value, true
		default:
			// Outbox was empty (drained), return channel to pool and try next
			trace.Logf(context.Background(), traceRegion, "outboxCh=%p was empty, trying next", outboxCh)
			p.putChan(outboxCh)
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
func (q *Required[T]) PopFrontExcessFunc(p *Pool[T], outboxWaiter WaiterOrReceiver, selectFn WaitSelectFunc) (T, bool) {
	traceRegion := "rdvq.Required.PopFrontExcessFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Required=%p", q)

	var renotifyFn RenotifyFunc
	for {
		value, ok := q.TryPopFront(p)
		if ok {
			return value, true
		}
		if renotifyFn != nil {
			// We could not productively use the notification, so pass it on. Do so
			// before starting wait to avoid an infinite renotification loop.
			renotifyFn()
		}

		confirmFn := func() bool {
			value, ok = q.TryPopFront(p)
			return !ok
		}
		renotifyFn = q.outboxWaiters.WaitFunc(outboxWaiter.waiter(), confirmFn, selectFn)
		if ok || renotifyFn == nil {
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
func (q *Required[T]) PopFrontExcess(ctx context.Context, p *Pool[T], outboxWaiter WaiterOrReceiver) (T, error) {
	traceRegion := "rdvq.Required.PopFrontExcessFunc"

	var err error
	value, _ := q.PopFrontExcessFunc(p, outboxWaiter, func(outboxFilledCh <-chan RenotifyFunc) RenotifyFunc {
		trace.Logf(ctx, traceRegion, "entering select: outboxFilledCh=%p", outboxFilledCh)
		select {
		case renotifyFn := <-outboxFilledCh:
			trace.Logf(ctx, traceRegion, "received signal from outboxFilledCh=%p", outboxFilledCh)
			return renotifyFn
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
			return nil
		}
	})
	return value, err
}
