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
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (T, error) {
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

// MetricsSkim adds metrics collection to skim functions.
// This wrapper records count, duration, and error metrics for skim execution.
func MetricsSkim[T any](
	metricName string,
	skimFn func(ctx context.Context, result T, err error) error,
) psg.HandlerFunc[T] {
	return func(ctx context.Context, result T, err error) error {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics
		skimCounter, _ := meter.Int64Counter(metricName + ".count")
		skimDuration, _ := meter.Float64Histogram(metricName + ".duration")

		// Track execution
		skimCounter.Add(ctx, 1)

		// Execute skim
		skimErr := skimFn(ctx, result, err)

		// Record duration
		duration := time.Since(startTime).Seconds()
		skimDuration.Record(ctx, duration)

		// Record error if any
		if skimErr != nil {
			errorCounter, _ := meter.Int64Counter(metricName + ".errors")
			errorCounter.Add(ctx, 1)
		}

		return skimErr
	}
}

// MetricsFunnel adds metrics collection to accumulators.
// This wrapper records metrics for both Accumulate and Flush operations.
func MetricsFunnel[T any](
	funnelMetricName string,
	flushMetricName string,
	funnelFactory psg.AccumulatorFactory[T],
) psg.AccumulatorFactory[T] {
	return psg.NewAccumulatorFactory(func() psg.Accumulator[T] {
		innerFunnel := funnelFactory.NewAccumulator()
		meter := otel.GetMeterProvider().Meter("otpsg")

		// Create metrics for funnel operations
		funnelCounter, _ := meter.Int64Counter(funnelMetricName + ".count")
		funnelDuration, _ := meter.Float64Histogram(funnelMetricName + ".duration")
		funnelErrorCounter, _ := meter.Int64Counter(funnelMetricName + ".errors")

		// Create metrics for flush operations
		flushCounter, _ := meter.Int64Counter(flushMetricName + ".count")
		flushDuration, _ := meter.Float64Histogram(flushMetricName + ".duration")
		flushErrorCounter, _ := meter.Int64Counter(flushMetricName + ".errors")

		return psg.FuncAccumulator[T]{
			AccumulateFn: func(ctx context.Context, input T, inputErr error) (time.Time, error) {
				startTime := time.Now()

				// Track execution
				funnelCounter.Add(ctx, 1)

				// Execute funnel with error tracking
				var flushTime time.Time
				var err error
				didPanic := true
				defer func() {
					// Record duration
					duration := time.Since(startTime).Seconds()
					funnelDuration.Record(ctx, duration)

					// Record error or panic
					if didPanic || inputErr != nil || err != nil {
						funnelErrorCounter.Add(ctx, 1)
					}
				}()

				// Execute original funnel
				flushTime, err = innerFunnel.Accumulate(ctx, input, inputErr)
				didPanic = false
				return flushTime, err
			},
			FlushFn: func(ctx context.Context) error {
				startTime := time.Now()

				// Track execution
				flushCounter.Add(ctx, 1)

				// Execute flush
				err := innerFunnel.Flush(ctx)

				// Record duration and errors
				duration := time.Since(startTime).Seconds()
				flushDuration.Record(ctx, duration)
				if err != nil {
					flushErrorCounter.Add(ctx, 1)
				}

				return err
			},
		}
	}, nil)
}
