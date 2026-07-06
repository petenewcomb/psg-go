// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/streampool"
)

// InstrumentedTask funnels tracing, metrics, and logging for tasks into a
// single wrapper. Returns a value-producing task body; pair it with a sink
// Skimmer via [Scatter] (or build your own [streampool.Launcher]) to dispatch.
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

// InstrumentedSkim funnels tracing, metrics, and logging for skim functions into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
//
// wave may be nil to defer wave binding to the dispatching ctx.
func InstrumentedSkim[T any](
	wave *streampool.Wave,
	operationName string,
	skimFn func(ctx context.Context, result T, err error) error,
) streampool.Skimmer[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedSkim := LoggedSkim(operationName, skimFn)

	// 2. Then add metrics
	metricsSkim := MetricsSkim(operationName, loggedSkim)

	// 3. Finally add tracing (which includes propagation)
	return TracedSkim(wave, operationName, metricsSkim)
}

// InstrumentedFunnel funnels tracing, metrics, and logging for funnels into a single wrapper.
// This provides a convenient way to apply all instrumentation at once.
func InstrumentedFunnel[T any](
	funnelOpName string,
	flushOpName string,
	funnelFactory streampool.AccumulatorFactory[T],
) streampool.AccumulatorFactory[PropagatedResult[T]] {
	// Apply wrappers inside-out:
	// 1. First add logging
	loggedFunnel := LoggedFunnel(funnelOpName, flushOpName, funnelFactory)

	// 2. Then add metrics
	metricsFunnel := MetricsFunnel(funnelOpName, flushOpName, loggedFunnel)

	// 3. Finally add tracing (which includes propagation)
	return TracedFunnel(funnelOpName, flushOpName, metricsFunnel)
}

// Scatter wraps the value-producing task in a one-shot [streampool.TaskLauncher]
// that submits the result to skim, and dispatches it. The Launcher is
// constructed with a nil wave and resolves the dispatching wave from
// ctx at Start time (see [streampool.NewLauncher0]). Pass plain
// [streampool.Limiter]s via limits to throttle dispatch.
//
// Example:
//
//	task := otpsg.InstrumentedTask("process-data", myTaskFn)
//	skimmer := otpsg.InstrumentedSkim(&wave, "handle-result", mySkimFn)
//	err := otpsg.Scatter(ctx, &wave, skimmer, task)
//
// wave is the Wave the launched task is part of; it is bound explicitly because a
// top-level ctx carries no ambient wave.
func Scatter[T any](
	ctx context.Context,
	wave *streampool.Wave,
	skim streampool.Skimmer[PropagatedResult[T]],
	task func(context.Context) (PropagatedResult[T], error),
	limits ...streampool.Limiter,
) error {
	runner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		result, err := task(ctx)
		return skim.SubmitResult(ctx, result, err)
	}).WithLimits(limits...)
	return runner.In(wave).Start(ctx)
}
