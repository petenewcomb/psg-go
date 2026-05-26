// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/psgfn"
)

// GenericCombiner mirrors psgfn.Accumulator but injects the Workflow
// instance associated with each input. The implementation owns its
// aggregated state and is responsible for routing results downstream
// via Submit on whatever sinks it captures.
type GenericCombiner[T, C any] interface {
	// Accumulate processes a single input value (paired with its workflow
	// and an upstream error). Returns the time when this instance's
	// Flush method should be called, or a zero time value if Flush need
	// not be called until the framework drains.
	Accumulate(ctx context.Context, wf *GenericWorkflow[C], value T, err error) (time.Time, error)

	// Flush finalizes the aggregated state. The body is responsible for
	// constructing any downstream submissions itself; it has access to
	// whatever workflow handles it retained from Accumulate calls.
	Flush(ctx context.Context) error
}

type Combiner[T any] = GenericCombiner[T, Context]

type GenericCombinerFactory[T, C any] func() GenericCombiner[T, C]
type CombinerFactory[T any] = GenericCombinerFactory[T, Context]

func wrapCombinerFactory[T, C any](
	combinerFactory GenericCombinerFactory[T, C],
) psgfn.CombinerFactory[result[T, C]] {
	return func() psgfn.Accumulator[result[T, C]] {
		inner := combinerFactory()
		return psgfn.FuncAccumulator[result[T, C]]{
			AccumulateFn: func(ctx context.Context, input result[T, C], inputErr error) (time.Time, error) {
				defer input.Workflow.unref(ctx)
				return inner.Accumulate(ctx, input.Workflow, input.Value, inputErr)
			},
			FlushFn: inner.Flush,
		}
	}
}
