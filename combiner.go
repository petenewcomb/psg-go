// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

// Combiner is an interface that defines operations for combining and flushing inputs.
// It is used to aggregate inputs over time before emitting outputs.
type Combiner[I, O any] interface {
	// Combine processes a single input and optionally emits an output.
	// It is called each time a task completes.
	Combine(ctx context.Context, input I, inputErr error, emit CombinerEmitFunc[O])

	// Flush is called when the job is completing or when a combiner goroutine
	// is shutting down. It should emit any pending aggregated results.
	Flush(ctx context.Context, emit CombinerEmitFunc[O])
}

type CombinerEmitFunc[O any] func(context.Context, O, error)

// FuncCombiner is a thin wrapper that implements the Combiner interface
// using function fields. This allows for simple creation of combiners using
// closures that share state.
type FuncCombiner[I, O any] struct {
	// CombineFunc is called to process each input
	CombineFunc func(ctx context.Context, input I, inputErr error, emit CombinerEmitFunc[O])

	// FlushFunc is called to emit any pending aggregated results
	FlushFunc func(ctx context.Context, emit CombinerEmitFunc[O])
}

// Combine calls the CombineFunc field with the provided arguments.
func (c FuncCombiner[I, O]) Combine(ctx context.Context, input I, inputErr error, emit CombinerEmitFunc[O]) {
	if c.CombineFunc != nil {
		c.CombineFunc(ctx, input, inputErr, emit)
	}
}

// Flush calls the FlushFunc field with the provided arguments.
func (c FuncCombiner[I, O]) Flush(ctx context.Context, emit CombinerEmitFunc[O]) {
	if c.FlushFunc != nil {
		c.FlushFunc(ctx, emit)
	}
}

// CombinerFactory is a function that creates a new Combiner instance.
type CombinerFactory[I, O any] = func() Combiner[I, O]

type errCombiner[I, O any] struct {
	err error
}

func (c errCombiner[I, O]) Combine(ctx context.Context, input I, inputErr error, emit CombinerEmitFunc[O]) {
	emit(ctx, *new(O), c.err)
}

func (c errCombiner[I, O]) Flush(ctx context.Context, emit CombinerEmitFunc[O]) {
}
