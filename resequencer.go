// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"time"
)

// Resequencer reimposes order on out-of-order arrivals. Values are submitted
// with caller-assigned sequence numbers (via [Resequencer.Submit]) and delivered
// to a handler exactly once each, in ascending sequence order, regardless of the
// order in which they were submitted. Values that arrive early are buffered until
// their predecessors arrive.
//
// It packages the scatter-then-resequence pattern: fan work out across the pool
// tagged with monotonic sequence numbers, let it complete in any order, and have
// a single sink observe the results in the original order — writing ordered
// HTTP/1.1 pipelined responses, committing records in submission order, etc.
//
// Sequence numbers must form a gap-free run starting at the constructor's start
// value, each submitted at most once: a value reaches handler only after every
// lower sequence number has. A permanent gap strands every higher one: they are
// buffered, never delivered, and discarded at drain.
//
// Resequencer is the unit-width case of [RangeResequencer] (every value occupies
// one position). A Resequencer is a thin handle over a single-instance [Funnel];
// like a Funnel it binds its [Wave] at construction and is safe to copy.
type Resequencer[T any] struct {
	funnel Funnel[rangeItem[T]]
}

// NewResequencer returns a Resequencer that invokes handler exactly once per
// submitted value, in ascending sequence order, beginning at start (the first
// sequence number it will deliver — usually 0).
//
// Resequencing is a reducer (a serial fold over shared next-sequence state), so
// the funnel is capped to a single instance with a concurrency-1 limiter;
// handler is therefore invoked sequentially and needs no internal
// synchronization.
func NewResequencer[T any](wave *Wave, start uint64, handler Handler[T]) Resequencer[T] {
	return Resequencer[T]{funnel: newResequenceFunnel[T](wave, start, handler)}
}

// NewFnResequencer is [NewResequencer] with a function handler.
func NewFnResequencer[T any](
	wave *Wave,
	start uint64,
	handle func(ctx context.Context, value T, err error) error,
) Resequencer[T] {
	return NewResequencer[T](wave, start, HandlerFunc[T](handle))
}

// Submit hands value to the resequencer at position seq. Sugar for
// SubmitResult(ctx, seq, value, nil).
func (r Resequencer[T]) Submit(ctx context.Context, seq uint64, value T) error {
	return r.SubmitResult(ctx, seq, value, nil)
}

// SubmitResult hands (value, err) to the resequencer at position seq; both are
// forwarded to handler when seq becomes deliverable.
func (r Resequencer[T]) SubmitResult(ctx context.Context, seq uint64, value T, err error) error {
	return r.funnel.SubmitResult(ctx, rangeItem[T]{offset: seq, length: 1, value: value}, err)
}

// RangeResequencer reimposes order on out-of-order arrivals that each occupy a
// half-open range [offset, offset+length) of an index space. It generalizes
// [Resequencer] to variable-width segments: think reassembling a byte stream or
// file from chunks read at known offsets, delivered in order once contiguous.
//
// Submitted ranges must tile the index space from the constructor's start value
// with no gaps and no overlaps (start..start+length0, then …). A value is
// delivered to handler once every position below its offset has been delivered;
// the next expected offset then advances by that value's length. A permanent gap
// strands everything above it.
//
// Like [Resequencer] it is a single-instance reducer — handler is invoked
// sequentially — and binds its [Wave] at construction.
type RangeResequencer[T any] struct {
	funnel Funnel[rangeItem[T]]
}

// NewRangeResequencer returns a RangeResequencer beginning at start (the first
// offset it will deliver — usually 0). See [NewResequencer] for the
// single-instance/limiter semantics, which are identical.
func NewRangeResequencer[T any](wave *Wave, start uint64, handler Handler[T]) RangeResequencer[T] {
	return RangeResequencer[T]{funnel: newResequenceFunnel[T](wave, start, handler)}
}

// NewFnRangeResequencer is [NewRangeResequencer] with a function handler.
func NewFnRangeResequencer[T any](
	wave *Wave,
	start uint64,
	handle func(ctx context.Context, value T, err error) error,
) RangeResequencer[T] {
	return NewRangeResequencer[T](wave, start, HandlerFunc[T](handle))
}

// Submit hands value, covering [offset, offset+length), to the resequencer.
// Sugar for SubmitResult(ctx, offset, length, value, nil).
func (r RangeResequencer[T]) Submit(ctx context.Context, offset, length uint64, value T) error {
	return r.SubmitResult(ctx, offset, length, value, nil)
}

// SubmitResult hands (value, err) covering [offset, offset+length) to the
// resequencer; both are forwarded to handler when offset becomes deliverable.
func (r RangeResequencer[T]) SubmitResult(ctx context.Context, offset, length uint64, value T, err error) error {
	return r.funnel.SubmitResult(ctx, rangeItem[T]{offset: offset, length: length, value: value}, err)
}

// ── shared implementation ────────────────────────────────────────────────────

// rangeItem is the internal funnel input: a value occupying [offset, offset+length).
type rangeItem[T any] struct {
	offset uint64
	length uint64
	value  T
}

func newResequenceFunnel[T any](wave *Wave, start uint64, handler Handler[T]) Funnel[rangeItem[T]] {
	return NewFunnel[rangeItem[T]](
		wave,
		AccumulatorFactoryFunc[rangeItem[T]](func() Accumulator[rangeItem[T]] {
			return &resequenceWindow[T]{
				next:    start,
				pending: make(map[uint64]pendingRange[T]),
				handler: handler,
			}
		}),
	).WithLimits(NewSemaphore(1))
}

type pendingRange[T any] struct {
	length uint64
	value  T
	err    error
}

// resequenceWindow is the single-instance Accumulator behind both resequencers.
// It buffers values by offset and releases each to handler once the next expected
// offset reaches it, then advances by that value's length.
type resequenceWindow[T any] struct {
	next    uint64
	pending map[uint64]pendingRange[T]
	handler Handler[T]
}

func (w *resequenceWindow[T]) Accumulate(ctx context.Context, item rangeItem[T], err error) (time.Time, error) {
	w.pending[item.offset] = pendingRange[T]{length: item.length, value: item.value, err: err}
	for {
		p, ok := w.pending[w.next]
		if !ok {
			break // wait for the missing predecessor
		}
		if herr := w.handler.Handle(ctx, p.value, p.err); herr != nil {
			return time.Time{}, herr
		}
		delete(w.pending, w.next)
		w.next += p.length
	}
	// Zero time: no flush deadline — the framework flushes on drain.
	return time.Time{}, nil
}

func (w *resequenceWindow[T]) Flush(context.Context) error { return nil }
