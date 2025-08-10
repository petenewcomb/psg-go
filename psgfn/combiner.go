// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/cerr"
)

// CombinerFactory is a function that creates a new Combiner instance.
type CombinerFactory[I, O any] = func() Combiner[I, O]

// Combiner instances perform partial aggregation of task results before final
// gathering. They are used by [CombinerPool] to enable scalable concurrent and
// parallel result aggregation without requiring multiple gathering goroutines
// or use of thread-safe data structures and algorithms.
type Combiner[I, O any] interface {
	// Combine processes a single input task result and optionally emits an
	// output to be gathered. Returns the time when the combiner's Flush()
	// method should be called, or a zero time value if Flush() need not be
	// called until a combiner goroutine exits. If a non-nil error is returned,
	// it will be emitted immediately for gathering as if Flush called
	// emit(*new(T), err), but subsequent calls to the Combiner instance's
	// Combine will continue to be made and Flush will still be called as
	// directed by the flush deadlines returned by the Combine calls, including
	// those that returned non-nil errors.
	Combine(ctx context.Context, input I, inputErr error) (time.Time, error)

	// Flush returns combined results to be gathered. If there are no results to
	// be gathered, returns [ErrDoNotGather]. No further calls to Combine or Flush
	// will be made to an instance after Flush has been called, and all
	// references to the instance held by the psg framework will be dropped.
	Flush(ctx context.Context) (O, error)
}

const ErrDoNotGather = cerr.Error("flushed results should not be gathered")

// FuncCombiner is a struct that implements the [Combiner] interface
// using function fields. This allows for simple creation of combiners using
// closures that share state.
type FuncCombiner[I, O any] struct {
	CombineFn func(ctx context.Context, input I, inputErr error) (time.Time, error)
	FlushFn   func(ctx context.Context) (O, error)
}

// Combine calls the CombineFn field with the provided arguments.
func (c FuncCombiner[I, O]) Combine(ctx context.Context, input I, inputErr error) (time.Time, error) {
	return c.CombineFn(ctx, input, inputErr)
}

// Flush calls the FlushFn field with the provided arguments.
func (c FuncCombiner[I, O]) Flush(ctx context.Context) (O, error) {
	if c.FlushFn == nil {
		return *new(O), ErrDoNotGather
	}
	return c.FlushFn(ctx)
}
