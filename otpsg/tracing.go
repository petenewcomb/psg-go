// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"go.opentelemetry.io/otel"
)

// TracedTask adds spans with the given operation name to a task function.
// This builds on PropagateTask, adding explicit span creation while maintaining
// trace context propagation.
func TracedTask[T any](
	operationName string,
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[PropagatedResult[T]] {
	// Use the base propagator first
	propagatedTask := PropagateTask(taskFunc)

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

// TracedGather adds spans with the given operation name to a gather function.
// This builds on PropagateGather, adding explicit span creation while maintaining
// trace context propagation.
func TracedGather[T any](
	operationName string,
	gatherFunc func(ctx context.Context, result T, err error) error,
) *psg.Gather[PropagatedResult[T]] {
	// Create a gather function that adds tracing
	tracedGatherFunc := func(ctx context.Context, result T, err error) error {
		// Create span with meaningful name
		tracer := otel.Tracer("otpsg")
		ctx, span := tracer.Start(ctx, operationName)
		defer span.End()

		// Call the original gather function
		return gatherFunc(ctx, result, err)
	}

	// Then use the base propagation
	return PropagateGather(tracedGatherFunc)
}

// TracedCombiner adds spans with the given operation names to a combiner.
// This builds on PropagateCombiner, adding explicit span creation for both
// Combine and Flush operations while maintaining trace context propagation.
func TracedCombiner[I, O any](
	combineOpName string,
	flushOpName string,
	combinerFactory psg.CombinerFactory[I, O],
) psg.CombinerFactory[PropagatedResult[I], PropagatedResult[O]] {
	// Create a combiner factory that adds tracing
	tracedFactory := func() psg.Combiner[I, O] {
		innerCombiner := combinerFactory()

		return psg.FuncCombiner[I, O]{
			CombineFunc: func(ctx context.Context, input I, inputErr error, emit psg.CombinerEmitFunc[O]) {
				// Create span with meaningful name
				tracer := otel.Tracer("otpsg")
				ctx, span := tracer.Start(ctx, combineOpName)
				defer span.End()

				// Call the original combine function
				innerCombiner.Combine(ctx, input, inputErr, emit)
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[O]) {
				// Create span with meaningful name
				tracer := otel.Tracer("otpsg")
				ctx, span := tracer.Start(ctx, flushOpName)
				defer span.End()

				// Call the original flush function
				innerCombiner.Flush(ctx, emit)
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
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[T] {
	return func(ctx context.Context) (T, error) {
		// Create span with meaningful name
		tracer := otel.Tracer("otpsg")
		ctx, span := tracer.Start(ctx, operationName)
		defer span.End()

		// Execute original task with traced context
		return taskFunc(ctx)
	}
}
