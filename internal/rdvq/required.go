// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

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
func (q *Required[T]) Init(p *Pool[T]) {
	q.Optional.Init(p)
	q.fullOutboxes.Init(&p.nodePool)
	q.outboxWaiters.Init()
}

// PushSelectFunc is called when PushBackFunc needs to send a value when the outbox is full.
// It should wait for the outbox to become available, typically using a select statement
// to handle context cancellation and other events. Must return SelectOutboxFilled if the value was sent,
// SelectAborted if not.
type PushSelectFunc[T any] func(outboxCh chan<- T) SelectResult

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
func (q *Required[T]) PushBackFunc(p *Pool[T], outbox *Outbox[T], value T, selectFn PushSelectFunc[T]) {
	if !q.Optional.TryPushBack(p, value) {
		if outbox.ch == nil {
			// Outbox is empty, use it for "drop-and-go" semantics
			outbox.ch = p.getChan()
			outbox.ch <- value
			q.fullOutboxes.PushBack(&p.nodePool, outbox.ch)
			q.outboxWaiters.Notify()
			return
		}

		// Outbox is full, wait for it to become available
		if selectFn(outbox.ch) == SelectOutboxFilled {
			// Value was successfully sent to outbox
			q.fullOutboxes.PushBack(&p.nodePool, outbox.ch)
			q.outboxWaiters.Notify()
		}
		// If selectFn returned anything else, value was not sent (cancelled/aborted)
	}
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
	var err error
	q.PushBackFunc(p, outbox, value, func(outboxCh chan<- T) SelectResult {
		select {
		case outboxCh <- value:
			return SelectOutboxFilled
		case <-ctx.Done():
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
func (q *Required[T]) TryPushBack(p *Pool[T], outbox *Outbox[T], value T) bool {
	sent := true
	q.PushBackFunc(p, outbox, value, func(outboxCh chan<- T) SelectResult {
		// Don't block waiting for outbox to become available
		sent = false
		return SelectAborted
	})
	return sent
}

// RequiredPopSelectFunc handles the select operation for PopFrontFunc when no outboxes
// are available. It should select on the inbox channel and wait channel, returning
// the appropriate result to indicate what happened.
type RequiredPopSelectFunc[T any] func(inboxCh <-chan T, waitCh <-chan struct{}) SelectResult

func (q *Required[T]) tryOutboxes(p *Pool[T], processFn ProcessValueFunc[T]) bool {
	if value, ok := q.TryPopFront(p); ok {
		processFn(value)
		return true
	}
	return false
}

func (q *Required[T]) PopFrontFunc(p *Pool[T], processFn ProcessValueFunc[T], selectFn RequiredPopSelectFunc[T]) {
	for {
		if q.tryOutboxes(p, processFn) {
			return
		}
		var result SelectResult
		q.Optional.PopFrontFunc(p, processFn,
			func(inboxCh <-chan T) SelectResult {
				waiter := q.outboxWaiters.New(func() bool {
					checkResult := !q.tryOutboxes(p, processFn)
					return checkResult
				})
				waiter.Wait(func(waitCh <-chan struct{}) SelectResult {
					result = selectFn(inboxCh, waitCh)
					return result
				})
				return result
			},
		)
		if result != SelectWaitSignaled {
			break
		}
	}
}

func (q *Required[T]) PopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(p, processFn, func(inboxCh <-chan T, waitCh <-chan struct{}) SelectResult {
		select {
		case value := <-inboxCh:
			processFn(value)
			return SelectInboxEmptied
		case <-waitCh:
			return SelectWaitSignaled
		case <-ctx.Done():
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}

func (q *Required[T]) TryPopFront(p *Pool[T]) (T, bool) {
	for {
		outboxCh, ok := q.fullOutboxes.PopFront(&p.nodePool)
		if !ok {
			return *new(T), false // No more outboxes
		}

		select {
		case value := <-outboxCh:
			return value, true
		default:
			// Outbox was empty (drained), return channel to pool and try next
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
func (q *Required[T]) PopFrontExcessFunc(p *Pool[T], selectFn WaitSelectFunc) (T, bool) {
	for {
		value, ok := q.TryPopFront(p)
		if ok {
			return value, true
		}
		waiter := q.outboxWaiters.New(func() bool {
			value, ok = q.TryPopFront(p)
			return !ok
		})
		waitResult := waiter.Wait(selectFn)
		if waitResult != SelectWaitSignaled || ok {
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
func (q *Required[T]) PopFrontExcess(ctx context.Context, p *Pool[T]) (T, error) {
	var err error
	value, _ := q.PopFrontExcessFunc(p, func(waitCh <-chan struct{}) SelectResult {
		select {
		case <-waitCh:
			return SelectWaitSignaled
		case <-ctx.Done():
			err = ctx.Err()
			return SelectAborted
		}
	})
	return value, err
}
