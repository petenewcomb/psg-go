// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"go.opentelemetry.io/otel"
)

// MetricsTask adds metrics collection to tasks.
// This wrapper records count, duration, and error metrics for task execution.
func MetricsTask[T any](
	metricName string,
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[T] {
	return func(ctx context.Context) (T, error) {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics
		taskCounter, _ := meter.Int64Counter(metricName + ".count")
		taskDuration, _ := meter.Float64Histogram(metricName + ".duration")

		// Track execution
		taskCounter.Add(ctx, 1)

		// Execute task
		result, err := taskFunc(ctx)

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
	gatherFunc func(ctx context.Context, result T, err error) error,
) psg.GatherFunc[T] {
	return func(ctx context.Context, result T, err error) error {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics
		gatherCounter, _ := meter.Int64Counter(metricName + ".count")
		gatherDuration, _ := meter.Float64Histogram(metricName + ".duration")

		// Track execution
		gatherCounter.Add(ctx, 1)

		// Execute gather
		gatherErr := gatherFunc(ctx, result, err)

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

// MetricsCombiner adds metrics collection to combiners.
// This wrapper records metrics for both Combine and Flush operations.
func MetricsCombiner[I, O any](
	combineMetricName string,
	flushMetricName string,
	combinerFactory psg.CombinerFactory[I, O],
) psg.CombinerFactory[I, O] {
	return func() psg.Combiner[I, O] {
		innerCombiner := combinerFactory()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics for combine operations
		combineCounter, _ := meter.Int64Counter(combineMetricName + ".count")
		combineDuration, _ := meter.Float64Histogram(combineMetricName + ".duration")
		combineErrorCounter, _ := meter.Int64Counter(combineMetricName + ".errors")

		// Create metrics for flush operations
		flushCounter, _ := meter.Int64Counter(flushMetricName + ".count")
		flushDuration, _ := meter.Float64Histogram(flushMetricName + ".duration")

		return psg.FuncCombiner[I, O]{
			CombineFunc: func(ctx context.Context, input I, inputErr error, emit psg.CombinerEmitFunc[O]) {
				startTime := time.Now()

				// Track execution
				combineCounter.Add(ctx, 1)

				// Execute combine with error tracking
				didPanic := true
				defer func() {
					// Record duration
					duration := time.Since(startTime).Seconds()
					combineDuration.Record(ctx, duration)

					// Record error or panic
					if didPanic || inputErr != nil {
						combineErrorCounter.Add(ctx, 1)
					}
				}()

				// Execute original combine
				innerCombiner.Combine(ctx, input, inputErr, emit)
				didPanic = false
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[O]) {
				startTime := time.Now()

				// Track execution
				flushCounter.Add(ctx, 1)

				// Execute flush
				innerCombiner.Flush(ctx, emit)

				// Record duration (always succeeds since we don't check flush errors)
				duration := time.Since(startTime).Seconds()
				flushDuration.Record(ctx, duration)
			},
		}
	}
}
