// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/streampool"
)

// InstrumentedTask stacks metrics and logging (metrics ∘ logging) onto a task
// body, returning a value-producing body of the same shape. It adds NO tracing:
// a span's lifetime is the flow, applied once at the flow level via [Traced] +
// [streampool.WithFlow], not per op. Dispatch the returned body with a
// [streampool.Launcher] (e.g. wrap it in a [streampool.TaskLauncher] that
// submits its result to a downstream sink).
func InstrumentedTask[T any](
	name string,
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (T, error) {
	return MetricsTask(name, LoggedTask(name, taskFn))
}

// InstrumentedSkim stacks metrics and logging (metrics ∘ logging) onto a skim
// function, returning a [streampool.HandlerFunc] ready for a
// [streampool.Skimmer]. No tracing — see [InstrumentedTask].
func InstrumentedSkim[T any](
	name string,
	skimFn func(ctx context.Context, result T, err error) error,
) streampool.HandlerFunc[T] {
	return MetricsSkim(name, LoggedSkim(name, skimFn))
}

// InstrumentedFunnel stacks metrics and logging (metrics ∘ logging) onto an
// accumulator factory, instrumenting both Accumulate (funnelName) and Flush
// (flushName). No tracing — see [InstrumentedTask].
func InstrumentedFunnel[T any](
	funnelName string,
	flushName string,
	factory streampool.AccumulatorFactory[T],
) streampool.AccumulatorFactory[T] {
	return MetricsFunnel(funnelName, flushName, LoggedFunnel(funnelName, flushName, factory))
}
