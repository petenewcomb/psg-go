// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package otpsg provides OpenTelemetry integration for the psg scatter-gather library.
// It enables transparent propagation of trace context through psg tasks, skims, and
// funnels without requiring users to manually handle context propagation.
package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool"

	"go.opentelemetry.io/otel/trace"
)

// PropagatedResult wraps a user result with trace context information for propagation.
// This allows trace context to flow through the psg pipeline even when user code doesn't
// explicitly handle trace context.
type PropagatedResult[T any] struct {
	// UserResult is the original result returned by the user function
	UserResult T
	// TraceContext is the trace context to propagate
	TraceContext trace.SpanContext
}

// PropagateTask wraps a value-returning task body so its result carries
// the trace context from the calling ctx. The returned function is the
// raw value-producing body — pair it with [Scatter] (or build your own
// [streampool.Launcher]) to dispatch.
func PropagateTask[T any](
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (PropagatedResult[T], error) {
	return func(ctx context.Context) (PropagatedResult[T], error) {
		// Extract any existing trace context from incoming context
		existingTraceCtx := trace.SpanFromContext(ctx).SpanContext()

		// Execute original task
		result, err := taskFn(ctx)

		// Wrap result with trace context
		return PropagatedResult[T]{
			UserResult:   result,
			TraceContext: existingTraceCtx,
		}, err
	}
}

// PropagateSkim wraps a skim function to ensure trace context flows through.
// The skim function receives a context with the propagated trace context properly
// set, allowing spans created in the skim function to be properly parented.
//
// wave may be nil to defer wave binding to the dispatching ctx
// (see [streampool.NewSkimmer]).
func PropagateSkim[T any](
	wave *streampool.Wave,
	skimFn func(ctx context.Context, result T, err error) error,
) streampool.Skimmer[PropagatedResult[T]] {
	return streampool.NewFnSkimmer(
		func(ctx context.Context, wrapped PropagatedResult[T], err error) error {
			// Create context with propagated trace data
			propagatedCtx := ctx
			if wrapped.TraceContext.IsValid() {
				propagatedCtx = trace.ContextWithRemoteSpanContext(ctx, wrapped.TraceContext)
			}

			// Call original skim with enhanced context
			return skimFn(propagatedCtx, wrapped.UserResult, err)
		},
	).In(wave)
}

// PropagateFunnel wraps an accumulator factory to create accumulators that
// propagate trace context. After Wave 2 the streampool.Accumulator has no output type;
// the wrapper just rehydrates the trace span from the incoming
// PropagatedResult[T] into ctx so any Submit calls inside the user's
// Accumulate body carry the right trace context downstream.
func PropagateFunnel[T any](
	funnelFactory streampool.AccumulatorFactory[T],
) streampool.AccumulatorFactory[PropagatedResult[T]] {
	return streampool.AccumulatorFactoryFunc[PropagatedResult[T]](func() streampool.Accumulator[PropagatedResult[T]] {
		innerFunnel := funnelFactory.NewAccumulator()

		return streampool.FuncAccumulator[PropagatedResult[T]]{
			AccumulateFn: func(
				ctx context.Context,
				input PropagatedResult[T],
				inputErr error,
			) (time.Time, error) {
				// Create context with propagated trace data
				propagatedCtx := ctx
				if input.TraceContext.IsValid() {
					propagatedCtx = trace.ContextWithRemoteSpanContext(ctx, input.TraceContext)
				}

				return innerFunnel.Accumulate(propagatedCtx, input.UserResult, inputErr)
			},
			FlushFn: innerFunnel.Flush,
		}
	})
}
