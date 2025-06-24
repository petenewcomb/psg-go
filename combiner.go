// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/psgfn"
)

// CombinerFactory is a function that creates a new Combiner instance.
type CombinerFactory[I, O any] = func() Combiner[I, O]

// Combiner is an interface that defines operations for combining and flushing inputs.
// It is used to aggregate inputs over time before emitting outputs.
type Combiner[I, O any] interface {
	// Combine processes a single input and optionally emits an output.
	// It is called each time a task completes.
	Combine(ctx context.Context, input I, inputErr error, emit psgfn.Emit[O])

	// Flush is called when the job is completing or when a combiner goroutine
	// is shutting down. It should emit any pending aggregated results.
	Flush(ctx context.Context, emit psgfn.Emit[O])
}

type errCombiner[I, O any] struct {
	err error
}

func (c errCombiner[I, O]) Combine(ctx context.Context, input I, inputErr error, emit psgfn.Emit[O]) {
	emit(ctx, *new(O), c.err)
}

func (c errCombiner[I, O]) Flush(ctx context.Context, emit psgfn.Emit[O]) {
}
