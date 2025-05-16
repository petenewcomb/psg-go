// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

// InstrumentedTask combines tracing, metrics, and logging for tasks into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedTask[T any](
	operationName string,
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedTask := LoggedTask(operationName, taskFunc)

	// 2. Then add metrics
	metricsTask := MetricsTask(operationName, loggedTask)

	// 3. Finally add tracing (which includes propagation)
	return TracedTask(operationName, metricsTask)
}

// InstrumentedGather combines tracing, metrics, and logging for gather functions into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedGather[T any](
	operationName string,
	gatherFunc func(ctx context.Context, result T, err error) error,
) *psg.Gather[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedGather := LoggedGather(operationName, gatherFunc)

	// 2. Then add metrics
	metricsGather := MetricsGather(operationName, loggedGather)

	// 3. Finally add tracing (which includes propagation)
	return TracedGather(operationName, metricsGather)
}

// InstrumentedCombiner combines tracing, metrics, and logging for combiners into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedCombiner[I, O any](
	combineOpName string,
	flushOpName string,
	combinerFactory psg.CombinerFactory[I, O],
) psg.CombinerFactory[PropagatedResult[I], PropagatedResult[O]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedCombiner := LoggedCombiner(combineOpName, flushOpName, combinerFactory)

	// 2. Then add metrics
	metricsCombiner := MetricsCombiner(combineOpName, flushOpName, loggedCombiner)

	// 3. Finally add tracing (which includes propagation)
	return TracedCombiner(combineOpName, flushOpName, metricsCombiner)
}

// InstrumentedScatter is a convenience method that takes instrumented components
// and performs a scatter operation. This avoids the need for the user to manage
// the propagated result types manually.
//
// Example:
//
//	task := otpsg.InstrumentedTask("process-data", myTaskFunc)
//	gather := otpsg.InstrumentedGather("handle-result", myGatherFunc)
//	// Instead of gather.Scatter(ctx, pool, task), use:
//	err := otpsg.InstrumentedScatter(ctx, pool, task, gather)
func InstrumentedScatter[T any](
	ctx context.Context,
	pool *psg.TaskPool,
	task psg.TaskFunc[PropagatedResult[T]],
	gather *psg.Gather[PropagatedResult[T]],
) error {
	return gather.Scatter(ctx, pool, task)
}
