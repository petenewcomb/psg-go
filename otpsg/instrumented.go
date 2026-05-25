// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// InstrumentedTask combines tracing, metrics, and logging for tasks into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedTask[T any](
	operationName string,
	taskFn func(ctx context.Context) (T, error),
) psgfn.Task[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedTask := LoggedTask(operationName, taskFn)

	// 2. Then add metrics
	metricsTask := MetricsTask(operationName, loggedTask)

	// 3. Finally add tracing (which includes propagation)
	return TracedTask(operationName, metricsTask)
}

// InstrumentedGather combines tracing, metrics, and logging for gather functions into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedGather[T any](
	operationName string,
	gatherFn func(ctx context.Context, result T, err error) error,
) psg.Gatherer[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedGather := LoggedGather(operationName, gatherFn)

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
	combinerFactory psgfn.CombinerFactory[I, O],
) psgfn.CombinerFactory[PropagatedResult[I], PropagatedResult[O]] {
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
//	task := otpsg.InstrumentedTask("process-data", myTaskFn)
//	gatherer := otpsg.InstrumentedGather("handle-result", myGatherFn)
//	// Instead of gatherer.Start(ctx, pool, task), use:
//	err := otpsg.InstrumentedScatter(ctx, pool, task, gatherer)
func InstrumentedScatter[T any](
	ctx context.Context,
	target psg.TaskPoolOrJob,
	task psgfn.Task[PropagatedResult[T]],
	gather psg.Gatherer[PropagatedResult[T]],
) error {
	return gather.Start(ctx, target, task)
}
