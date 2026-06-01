// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
)

// GenericFunnel mirrors psg.Accumulator but injects the Workflow
// instance associated with each input. The implementation owns its
// aggregated state and is responsible for routing results downstream
// via Submit on whatever sinks it captures.
type GenericFunnel[T, C any] interface {
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

type Funnel[T any] = GenericFunnel[T, Context]

type GenericFunnelFactory[T, C any] func() GenericFunnel[T, C]
type FunnelFactory[T any] = GenericFunnelFactory[T, Context]

func wrapFunnelFactory[T, C any](
	funnelFactory GenericFunnelFactory[T, C],
) psg.AccumulatorFactory[result[T, C]] {
	return psg.AccumulatorFactoryFunc[result[T, C]](func() psg.Accumulator[result[T, C]] {
		inner := funnelFactory()
		return psg.FuncAccumulator[result[T, C]]{
			AccumulateFn: func(ctx context.Context, input result[T, C], inputErr error) (time.Time, error) {
				defer input.Workflow.unref(ctx)
				return inner.Accumulate(ctx, input.Workflow, input.Value, inputErr)
			},
			FlushFn: inner.Flush,
		}
	})
}
