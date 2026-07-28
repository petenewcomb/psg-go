// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool"
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

		taskCounter, _ := meter.Int64Counter(metricName + ".count")
		taskDuration, _ := meter.Float64Histogram(metricName + ".duration")

		taskCounter.Add(ctx, 1)

		result, err := taskFn(ctx)

		duration := time.Since(startTime).Seconds()
		taskDuration.Record(ctx, duration)

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
) streampool.HandlerFunc[T] {
	return func(ctx context.Context, result T, err error) error {
		startTime := time.Now()
		meter := otel.GetMeterProvider().Meter("otpsg")

		skimCounter, _ := meter.Int64Counter(metricName + ".count")
		skimDuration, _ := meter.Float64Histogram(metricName + ".duration")

		skimCounter.Add(ctx, 1)

		skimErr := skimFn(ctx, result, err)

		duration := time.Since(startTime).Seconds()
		skimDuration.Record(ctx, duration)

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
	funnelFactory streampool.AccumulatorFactory[T],
) streampool.AccumulatorFactory[T] {
	return streampool.NewAccumulatorFactory(func() streampool.Accumulator[T] {
		innerFunnel := funnelFactory.NewAccumulator()
		meter := otel.GetMeterProvider().Meter("otpsg")

		funnelCounter, _ := meter.Int64Counter(funnelMetricName + ".count")
		funnelDuration, _ := meter.Float64Histogram(funnelMetricName + ".duration")
		funnelErrorCounter, _ := meter.Int64Counter(funnelMetricName + ".errors")

		flushCounter, _ := meter.Int64Counter(flushMetricName + ".count")
		flushDuration, _ := meter.Float64Histogram(flushMetricName + ".duration")
		flushErrorCounter, _ := meter.Int64Counter(flushMetricName + ".errors")

		return streampool.FuncAccumulator[T]{
			AccumulateFn: func(ctx context.Context, input T, inputErr error) (time.Time, error) {
				startTime := time.Now()

				funnelCounter.Add(ctx, 1)

				var flushTime time.Time
				var err error
				didPanic := true
				defer func() {
					duration := time.Since(startTime).Seconds()
					funnelDuration.Record(ctx, duration)

					if didPanic || inputErr != nil || err != nil {
						funnelErrorCounter.Add(ctx, 1)
					}
				}()

				flushTime, err = innerFunnel.Accumulate(ctx, input, inputErr)
				didPanic = false
				return flushTime, err
			},
			FlushFn: func(ctx context.Context) error {
				startTime := time.Now()

				flushCounter.Add(ctx, 1)

				err := innerFunnel.Flush(ctx)

				duration := time.Since(startTime).Seconds()
				flushDuration.Record(ctx, duration)
				if err != nil {
					flushErrorCounter.Add(ctx, 1)
				}

				return err
			},
		}
	})
}
