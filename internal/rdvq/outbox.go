// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

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
//
//nolint:contextcheck // background context used only for tracing
func (ob *Outbox[T]) IsEmpty(p *Pool[T]) bool {
	traceRegion := "rdvq.Outbox.IsEmpty"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	outboxCh := ob.ch
	trace.Logf(context.Background(), traceRegion, "Outbox=%p, outboxCh=%p", ob, outboxCh)
	if outboxCh == nil {
		return true
	}

	trace.Logf(context.Background(), traceRegion, "entering select: outboxCh=%p", outboxCh)
	select {
	case outboxCh <- *new(T):
		trace.Logf(context.Background(), traceRegion, "delivered zero value to outboxCh=%p, returning true", outboxCh)
		// Successfully sent a zero value, so the channel is empty
		ob.ch = nil
		<-outboxCh // remove the zero value
		p.putChan(outboxCh)
		return true
	default:
		trace.Logf(context.Background(), traceRegion, "outboxCh=%p is not empty, returning false", outboxCh)
		// Can't send a zero value, so the channel is not empty
		return false
	}
}

// OutboxWaitSelectFunc should attempt to write a (zero value) T to the channel.
// It must return SelectOutboxFilled if it successfully wrote, SelectAborted otherwise.
// The value written will be discarded.
type OutboxWaitSelectFunc[T any] func(ch chan<- T) SelectResult

//nolint:contextcheck // background context used only for tracing
func (ob *Outbox[T]) WaitFunc(p *Pool[T], selectFn OutboxWaitSelectFunc[T]) {
	traceRegion := "rdvq.Outbox.WaitFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	outboxCh := ob.ch
	trace.Logf(context.Background(), traceRegion, "Outbox=%p, outboxCh=%p", ob, outboxCh)

	if outboxCh != nil && selectFn(outboxCh) == SelectOutboxFilled {
		// If the select function returns SelectOutboxFilled, it means that the channel was
		// successfully written to, confirming that the box was empty. But now
		// it's full, so we need to drain the value before putting the channel
		// back into the pool.
		ob.ch = nil
		<-outboxCh
		p.putChan(outboxCh)
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
	traceRegion := "rdvq.Outbox.Wait"

	var err error
	ob.WaitFunc(p, func(outboxCh chan<- T) SelectResult {
		trace.Logf(ctx, traceRegion, "entering select: outboxCh=%p", outboxCh)
		select {
		case outboxCh <- *new(T):
			trace.Logf(ctx, traceRegion, "delivered zero value to outboxCh=%p", outboxCh)
			return SelectOutboxFilled
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
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
//
//nolint:contextcheck // background context used only for tracing
func (ob *Outbox[T]) Drain(p *Pool[T]) (T, bool) {
	traceRegion := "rdvq.Outbox.Drain"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	outboxCh := ob.ch
	trace.Logf(context.Background(), traceRegion, "Outbox=%p, outboxCh=%p", ob, outboxCh)

	if outboxCh != nil {
		ob.ch = nil
		trace.Logf(context.Background(), traceRegion, "entering select: outboxCh=%p", outboxCh)
		select {
		case value := <-outboxCh:
			// Had an item, drained it. Don't pool because it is still queued in
			// fullOutboxes; tryOutboxes will handle it.
			trace.Logf(context.Background(), traceRegion, "received value from outboxCh=%p, returning true", outboxCh)
			return value, true
		default:
			// Was empty, safe to pool immediately
			p.putChan(outboxCh)
		}
	}

	trace.Logf(context.Background(), traceRegion, "outbox was empty, returning false")
	return *new(T), false
}
