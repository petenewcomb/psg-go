// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/psgfn"
	"go.opentelemetry.io/otel"
)

// MetricsTask adds metrics collection to tasks.
// This wrapper records count, duration, and error metrics for task execution.
func MetricsTask[T any](
	metricName string,
	taskFn func(ctx context.Context) (T, error),
) psgfn.Task[T] {
	return func(ctx context.Context) (T, error) {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics
		taskCounter, _ := meter.Int64Counter(metricName + ".count")
		taskDuration, _ := meter.Float64Histogram(metricName + ".duration")

		// Track execution
		taskCounter.Add(ctx, 1)

		// Execute task
		result, err := taskFn(ctx)

		// Record duration
		duration := time.Since(startTime).Seconds()
		taskDuration.Record(ctx, duration)

		// Record error if any
		if err != nil {
			errorCounter, _ := meter.Int64Counter(metricName + ".errors")
			errorCounter.Add(ctx, 1)
		}

		return result, err
	}
}

// MetricsGather adds metrics collection to gather functions.
// This wrapper records count, duration, and error metrics for gather execution.
func MetricsGather[T any](
	metricName string,
	gatherFn func(ctx context.Context, result T, err error) error,
) psgfn.Gather[T] {
	return func(ctx context.Context, result T, err error) error {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics
		gatherCounter, _ := meter.Int64Counter(metricName + ".count")
		gatherDuration, _ := meter.Float64Histogram(metricName + ".duration")

		// Track execution
		gatherCounter.Add(ctx, 1)

		// Execute gather
		gatherErr := gatherFn(ctx, result, err)

		// Record duration
		duration := time.Since(startTime).Seconds()
		gatherDuration.Record(ctx, duration)

		// Record error if any
		if gatherErr != nil {
			errorCounter, _ := meter.Int64Counter(metricName + ".errors")
			errorCounter.Add(ctx, 1)
		}

		return gatherErr
	}
}

// MetricsCombiner adds metrics collection to accumulators.
// This wrapper records metrics for both Accumulate and Flush operations.
func MetricsCombiner[T any](
	combineMetricName string,
	flushMetricName string,
	combinerFactory psgfn.CombinerFactory[T],
) psgfn.CombinerFactory[T] {
	return func() psgfn.Accumulator[T] {
		innerCombiner := combinerFactory()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics for combine operations
		combineCounter, _ := meter.Int64Counter(combineMetricName + ".count")
		combineDuration, _ := meter.Float64Histogram(combineMetricName + ".duration")
		combineErrorCounter, _ := meter.Int64Counter(combineMetricName + ".errors")

		// Create metrics for flush operations
		flushCounter, _ := meter.Int64Counter(flushMetricName + ".count")
		flushDuration, _ := meter.Float64Histogram(flushMetricName + ".duration")
		flushErrorCounter, _ := meter.Int64Counter(flushMetricName + ".errors")

		return psgfn.FuncAccumulator[T]{
			AccumulateFn: func(ctx context.Context, input T, inputErr error) (time.Time, error) {
				startTime := time.Now()

				// Track execution
				combineCounter.Add(ctx, 1)

				// Execute combine with error tracking
				var flushTime time.Time
				var err error
				didPanic := true
				defer func() {
					// Record duration
					duration := time.Since(startTime).Seconds()
					combineDuration.Record(ctx, duration)

					// Record error or panic
					if didPanic || inputErr != nil || err != nil {
						combineErrorCounter.Add(ctx, 1)
					}
				}()

				// Execute original combine
				flushTime, err = innerCombiner.Accumulate(ctx, input, inputErr)
				didPanic = false
				return flushTime, err
			},
			FlushFn: func(ctx context.Context) error {
				startTime := time.Now()

				// Track execution
				flushCounter.Add(ctx, 1)

				// Execute flush
				err := innerCombiner.Flush(ctx)

				// Record duration and errors
				duration := time.Since(startTime).Seconds()
				flushDuration.Record(ctx, duration)
				if err != nil {
					flushErrorCounter.Add(ctx, 1)
				}

				return err
			},
		}
	}
}
