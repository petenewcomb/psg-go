// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool"
	"go.uber.org/zap"
)

// LoggedTask adds structured logging to tasks.
// This wrapper logs the start and completion of task execution, including
// timing information and any errors that occur.
func LoggedTask[T any](
	operationName string,
	taskFn func(ctx context.Context) (T, error),
) func(ctx context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		// Get logger from context or use a default
		// This implementation uses zap, but could be adapted for any logger
		logger := zap.L()

		// Log start of operation
		logger.Debug("Starting task",
			zap.String("operation", operationName),
			zap.String("component", "otpsg"))

		// Time the operation
		startTime := time.Now()
		result, err := taskFn(ctx)
		duration := time.Since(startTime)

		// Log completion with appropriate level based on success/failure
		if err != nil {
			logger.Error("Task failed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration),
				zap.Error(err))
		} else {
			logger.Debug("Task completed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration))
		}

		return result, err
	}
}

// LoggedSkim adds structured logging to skim functions.
// This wrapper logs the processing of skim operations, including timing
// information and any errors that occur.
func LoggedSkim[T any](
	operationName string,
	skimFn func(ctx context.Context, result T, err error) error,
) streampool.HandlerFunc[T] {
	return func(ctx context.Context, result T, err error) error {
		// Get logger from context or use a default
		logger := zap.L()

		// Log starting skim operation
		logger.Debug("Processing skim",
			zap.String("operation", operationName),
			zap.String("component", "otpsg"),
			zap.Bool("input_has_error", err != nil))

		// Time the operation
		startTime := time.Now()
		skimErr := skimFn(ctx, result, err)
		duration := time.Since(startTime)

		// Log completion with appropriate level based on success/failure
		if skimErr != nil {
			logger.Error("Skim failed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration),
				zap.Error(skimErr))
		} else {
			logger.Debug("Skim completed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration))
		}

		return skimErr
	}
}

// LoggedFunnel adds structured logging to accumulators.
// This wrapper logs accumulate and flush operations, including timing information.
func LoggedFunnel[T any](
	funnelOpName string,
	flushOpName string,
	funnelFactory streampool.AccumulatorFactory[T],
) streampool.AccumulatorFactory[T] {
	return streampool.NewAccumulatorFactory(func() streampool.Accumulator[T] {
		innerFunnel := funnelFactory.NewAccumulator()

		return streampool.FuncAccumulator[T]{
			AccumulateFn: func(ctx context.Context, input T, inputErr error) (time.Time, error) {
				// Get logger from context or use a default
				logger := zap.L()

				// Log starting funnel operation
				logger.Debug("Combining input",
					zap.String("operation", funnelOpName),
					zap.String("component", "otpsg"),
					zap.Bool("input_has_error", inputErr != nil))

				// Time the operation
				startTime := time.Now()
				flushTime, err := innerFunnel.Accumulate(ctx, input, inputErr)
				duration := time.Since(startTime)

				// Log completion
				logger.Debug("Funnel completed",
					zap.String("operation", funnelOpName),
					zap.String("component", "otpsg"),
					zap.Duration("duration", duration),
					zap.Bool("has_error", err != nil))

				return flushTime, err
			},
			FlushFn: func(ctx context.Context) error {
				// Get logger from context or use a default
				logger := zap.L()

				// Log starting flush operation
				logger.Debug("Flushing funnel",
					zap.String("operation", flushOpName),
					zap.String("component", "otpsg"))

				// Time the operation
				startTime := time.Now()
				err := innerFunnel.Flush(ctx)
				duration := time.Since(startTime)

				// Log completion
				logger.Debug("Flush completed",
					zap.String("operation", flushOpName),
					zap.String("component", "otpsg"),
					zap.Duration("duration", duration),
					zap.Bool("has_error", err != nil))

				return err
			},
		}
	})
}
