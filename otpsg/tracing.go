// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
	"go.opentelemetry.io/otel"
)

// TracedTask adds a span with the given operation name to a task body
// and propagates trace context through the result.
func TracedTask[T any](
	operationName string,
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (PropagatedResult[T], error) {
	// Use the base propagator first
	propagatedTask := PropagateTask(taskFn)

	return func(ctx context.Context) (PropagatedResult[T], error) {
		// Create span with meaningful name
		tracer := otel.Tracer("otpsg")
		ctx, span := tracer.Start(ctx, operationName)
		defer span.End()

		// Execute with propagation
		result, err := propagatedTask(ctx)

		// Ensure the result has our span context
		result.TraceContext = span.SpanContext()
		return result, err
	}
}

// TracedSkim adds spans with the given operation name to a skim function.
// This builds on PropagateSkim, adding explicit span creation while maintaining
// trace context propagation.
func TracedSkim[T any](
	operationName string,
	skimFn func(ctx context.Context, result T, err error) error,
) psg.Skimmer[PropagatedResult[T]] {
	// Create a skim function that adds tracing
	tracedSkimFn := func(ctx context.Context, result T, err error) error {
		// Create span with meaningful name
		tracer := otel.Tracer("otpsg")
		ctx, span := tracer.Start(ctx, operationName)
		defer span.End()

		// Call the original skim function
		return skimFn(ctx, result, err)
	}

	// Then use the base propagation
	return PropagateSkim(tracedSkimFn)
}

// TracedCombiner adds spans with the given operation names to an accumulator.
// This builds on PropagateCombiner, adding explicit span creation for both
// Accumulate and Flush operations while maintaining trace context propagation.
func TracedCombiner[T any](
	combineOpName string,
	flushOpName string,
	combinerFactory psgfn.CombinerFactory[T],
) psgfn.CombinerFactory[PropagatedResult[T]] {
	// Create an accumulator factory that adds tracing
	tracedFactory := func() psgfn.Accumulator[T] {
		innerCombiner := combinerFactory()

		return psgfn.FuncAccumulator[T]{
			AccumulateFn: func(ctx context.Context, input T, inputErr error) (time.Time, error) {
				// Create span with meaningful name
				tracer := otel.Tracer("otpsg")
				ctx, span := tracer.Start(ctx, combineOpName)
				defer span.End()

				// Call the original combine function
				return innerCombiner.Accumulate(ctx, input, inputErr)
			},
			FlushFn: func(ctx context.Context) error {
				// Create span with meaningful name
				tracer := otel.Tracer("otpsg")
				ctx, span := tracer.Start(ctx, flushOpName)
				defer span.End()

				// Call the original flush function
				return innerCombiner.Flush(ctx)
			},
		}
	}

	// Then use the base propagation
	return PropagateCombiner(tracedFactory)
}

// WithTaskTracing is a convenience function that applies tracing to a task
// without changing its return type. This is useful when you want to trace a task
// but don't need to propagate context through its result.
func WithTaskTracing[T any](
	operationName string,
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		// Create span with meaningful name
		tracer := otel.Tracer("otpsg")
		ctx, span := tracer.Start(ctx, operationName)
		defer span.End()

		// Execute original task with traced context
		return taskFn(ctx)
	}
}
