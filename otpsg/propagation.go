// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package otpsg provides OpenTelemetry integration for the psg scatter-gather library.
// It enables transparent propagation of trace context through psg tasks, gathers, and
// combiners without requiring users to manually handle context propagation.
package otpsg

import (
	"context"

	"github.com/petenewcomb/psg-go"
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

// PropagateTask wraps a TaskFunc to ensure trace context flows through task results.
// The returned task function will extract any existing trace context from the incoming
// context and attach it to the result for propagation.
func PropagateTask[T any](
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[PropagatedResult[T]] {
	return func(ctx context.Context) (PropagatedResult[T], error) {
		// Extract any existing trace context from incoming context
		existingTraceCtx := trace.SpanFromContext(ctx).SpanContext()

		// Execute original task
		result, err := taskFunc(ctx)

		// Wrap result with trace context
		return PropagatedResult[T]{
			UserResult:   result,
			TraceContext: existingTraceCtx,
		}, err
	}
}

// PropagateGather wraps a gather function to ensure trace context flows through.
// The gather function receives a context with the propagated trace context properly
// set, allowing spans created in the gather function to be properly parented.
func PropagateGather[T any](
	gatherFunc func(ctx context.Context, result T, err error) error,
) *psg.Gather[PropagatedResult[T]] {
	return psg.NewGather(func(ctx context.Context, wrapped PropagatedResult[T], err error) error {
		// Create context with propagated trace data
		propagatedCtx := ctx
		if wrapped.TraceContext.IsValid() {
			propagatedCtx = trace.ContextWithRemoteSpanContext(ctx, wrapped.TraceContext)
		}

		// Call original gather with enhanced context
		return gatherFunc(propagatedCtx, wrapped.UserResult, err)
	})
}

// PropagateCombiner wraps a combiner factory to create combiners that propagate trace context.
// Both the Combine and Flush methods will properly handle trace context propagation.
func PropagateCombiner[I, O any](
	combinerFactory psg.CombinerFactory[I, O],
) psg.CombinerFactory[PropagatedResult[I], PropagatedResult[O]] {
	return func() psg.Combiner[PropagatedResult[I], PropagatedResult[O]] {
		innerCombiner := combinerFactory()

		return psg.FuncCombiner[PropagatedResult[I], PropagatedResult[O]]{
			CombineFunc: func(ctx context.Context, input PropagatedResult[I], inputErr error, emit psg.CombinerEmitFunc[PropagatedResult[O]]) {
				// Create context with propagated trace data
				propagatedCtx := ctx
				if input.TraceContext.IsValid() {
					propagatedCtx = trace.ContextWithRemoteSpanContext(ctx, input.TraceContext)
				}

				// Extract current trace context for output propagation
				currentTrace := trace.SpanFromContext(propagatedCtx).SpanContext()

				// Wrap emit to continue propagation
				wrappedEmit := func(ctx context.Context, output O, outputErr error) {
					emit(ctx, PropagatedResult[O]{
						UserResult:   output,
						TraceContext: currentTrace,
					}, outputErr)
				}

				// Process with inner combiner
				innerCombiner.Combine(propagatedCtx, input.UserResult, inputErr, wrappedEmit)
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[PropagatedResult[O]]) {
				// Extract current trace context for output propagation
				currentTrace := trace.SpanFromContext(ctx).SpanContext()

				// Wrap emit to continue propagation
				wrappedEmit := func(ctx context.Context, output O, outputErr error) {
					emit(ctx, PropagatedResult[O]{
						UserResult:   output,
						TraceContext: currentTrace,
					}, outputErr)
				}

				// Process with inner combiner
				innerCombiner.Flush(ctx, wrappedEmit)
			},
		}
	}
}
