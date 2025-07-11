// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// Emit is a function used by combiners to emit aggregated results.
type Emit[O any] = func(context.Context, O, error)

// Combiner is a struct that implements the psg.Combiner interface
// using function fields. This allows for simple creation of combiners using
// closures that share state.
type Combiner[I, O any] struct {
	// CombineFn is called to process each input
	CombineFn func(ctx context.Context, input I, inputErr error, emit Emit[O])

	// FlushFn is called to emit any pending aggregated results
	FlushFn func(ctx context.Context, emit Emit[O])
}

// Combine calls the CombineFn field with the provided arguments.
func (c Combiner[I, O]) Combine(ctx context.Context, input I, inputErr error, emit Emit[O]) {
	if c.CombineFn != nil {
		c.CombineFn(ctx, input, inputErr, emit)
	}
}

// Flush calls the FlushFn field with the provided arguments.
func (c Combiner[I, O]) Flush(ctx context.Context, emit Emit[O]) {
	if c.FlushFn != nil {
		c.FlushFn(ctx, emit)
	}
}
