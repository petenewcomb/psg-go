// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import "context"

// Outbox provides per-sender buffering for overflow items in Required queues.
// Each sender should maintain their own Outbox instance to achieve "drop-and-go"
// semantics where the first overflow item is buffered without blocking.
//
// An Outbox has two states:
//   - Empty: ch is nil, can accept one item immediately
//   - Full: ch contains one buffered item, subsequent sends will block
//
// Outboxes are designed to be lightweight and reusable. The zero value is
// ready to use (empty state). Outboxes should not be shared between senders
// as this breaks the drop-and-go guarantees and may cause data races.
type Outbox[T any] struct {
	ch chan T // nil when empty, contains 1 buffered item when full
}

// IsEmpty reports whether the outbox can accept an item without blocking.
//
// This method is used internally by Required.PushBackFunc to determine
// whether to use the outbox for immediate buffering or fall back to
// the shared channel.
//
// Note: This method has side effects when the outbox is empty but contains
// a zero value. It will drain and recycle the channel in this case.
func (ob *Outbox[T]) IsEmpty(p *Pool[T]) bool {
	ch := ob.ch
	if ch == nil {
		return true
	}
	select {
	case ch <- *new(T):
		// Successfully sent a zero value, so the channel is empty
		ob.ch = nil
		<-ch // remove the zero value
		p.putChan(ch)
		return true
	default:
		// Can't send a zero value, so the channel is not empty
		return false
	}
}

// OutboxWaitSelectFunc should attempt to write a (zero value) T to the channel.
// It must return SelectOutboxFilled if it successfully wrote, SelectAborted otherwise.
// The value written will be discarded.
type OutboxWaitSelectFunc[T any] func(ch chan<- T) SelectResult

func (ob *Outbox[T]) WaitFunc(p *Pool[T], selectFn OutboxWaitSelectFunc[T]) {
	ch := ob.ch
	if ch != nil && selectFn(ch) == SelectOutboxFilled {
		// If the select function returns SelectOutboxFilled, it means that the channel was
		// successfully written to, confirming that the box was empty. But now
		// it's full, so we need to drain the value before putting the channel
		// back into the pool.
		ob.ch = nil
		<-ch
		p.putChan(ch)
	}
}

// Wait blocks until the outbox is empty, ensuring any buffered item has been
// processed by a receiver. This method should typically be called before a
// sender exits to ensure all work has been completed.
//
// Returns an error only if the context is cancelled before the outbox is drained.
// If the outbox is already empty, this method returns immediately.
//
// Example usage:
//
//	err := queue.PushBack(ctx, pool, &outbox, item)
//	if err != nil { return err }
//	err = outbox.Wait(ctx, pool) // Ensure item is processed
//	return err
func (ob *Outbox[T]) Wait(ctx context.Context, p *Pool[T]) error {
	var err error
	ob.WaitFunc(p, func(ch chan<- T) SelectResult {
		select {
		case ch <- *new(T):
			return SelectOutboxFilled
		case <-ctx.Done():
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}

// Drain extracts any item from the outbox and cleans up resources.
// Returns the drained value and true if an item was present, or zero value
// and false if the outbox was empty.
//
// After calling Drain, the outbox should not be reused. This method is
// typically used for cleanup during context cancellation or job completion.
func (ob *Outbox[T]) Drain(p *Pool[T]) (T, bool) {
	if ch := ob.ch; ch != nil {
		ob.ch = nil
		select {
		case value := <-ch:
			// Had an item, drained it. Don't pool - tryOutboxes will handle it
			return value, true
		default:
			// Was empty, safe to pool immediately
			p.putChan(ch)
		}
	}
	return *new(T), false
}
