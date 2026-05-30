// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// InstrumentedTask combines tracing, metrics, and logging for tasks into a
// single wrapper. Returns a value-producing task body; pair it with a sink
// Gatherer via [Scatter] (or build your own [psg.TaskRunner]) to dispatch.
func InstrumentedTask[T any](
	operationName string,
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (PropagatedResult[T], error) {
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
func InstrumentedCombiner[T any](
	combineOpName string,
	flushOpName string,
	combinerFactory psgfn.CombinerFactory[T],
) psgfn.CombinerFactory[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedCombiner := LoggedCombiner(combineOpName, flushOpName, combinerFactory)

	// 2. Then add metrics
	metricsCombiner := MetricsCombiner(combineOpName, flushOpName, loggedCombiner)

	// 3. Finally add tracing (which includes propagation)
	return TracedCombiner(combineOpName, flushOpName, metricsCombiner)
}

// Scatter wraps the value-producing task in a one-shot [psg.TaskRunner0]
// that submits the result to gather, and dispatches it. Pass psg op
// options (e.g. [psg.WithLimits]) via opts to throttle dispatch. This
// replaces the pre-Wave-3 pattern of [psg.Gatherer].Start on an
// instrumented-task value.
//
// Example:
//
//	task := otpsg.InstrumentedTask("process-data", myTaskFn)
//	gatherer := otpsg.InstrumentedGather("handle-result", myGatherFn)
//	err := otpsg.Scatter(ctx, job, gatherer, task)
func Scatter[T any](
	ctx context.Context,
	pool *psg.Pool,
	gather psg.Gatherer[PropagatedResult[T]],
	task func(context.Context) (PropagatedResult[T], error),
	opts ...psg.OpOption,
) error {
	runner := psg.NewTaskRunner0(pool, psgfn.TaskFunc0(func(ctx context.Context) error {
		result, err := task(ctx)
		return gather.Submit(ctx, pool, result, err)
	}), opts...)
	return runner.Start(ctx)
}
