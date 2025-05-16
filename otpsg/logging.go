// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"go.uber.org/zap"
)

// LoggedTask adds structured logging to tasks.
// This wrapper logs the start and completion of task execution, including
// timing information and any errors that occur.
func LoggedTask[T any](
	operationName string,
	taskFunc func(ctx context.Context) (T, error),
) psg.TaskFunc[T] {
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
		result, err := taskFunc(ctx)
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

// LoggedGather adds structured logging to gather functions.
// This wrapper logs the processing of gather operations, including timing
// information and any errors that occur.
func LoggedGather[T any](
	operationName string,
	gatherFunc func(ctx context.Context, result T, err error) error,
) psg.GatherFunc[T] {
	return func(ctx context.Context, result T, err error) error {
		// Get logger from context or use a default
		logger := zap.L()

		// Log starting gather operation
		logger.Debug("Processing gather",
			zap.String("operation", operationName),
			zap.String("component", "otpsg"),
			zap.Bool("input_has_error", err != nil))

		// Time the operation
		startTime := time.Now()
		gatherErr := gatherFunc(ctx, result, err)
		duration := time.Since(startTime)

		// Log completion with appropriate level based on success/failure
		if gatherErr != nil {
			logger.Error("Gather failed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration),
				zap.Error(gatherErr))
		} else {
			logger.Debug("Gather completed",
				zap.String("operation", operationName),
				zap.String("component", "otpsg"),
				zap.Duration("duration", duration))
		}

		return gatherErr
	}
}

// LoggedCombiner adds structured logging to combiners.
// This wrapper logs combine and flush operations, including timing information.
func LoggedCombiner[I, O any](
	combineOpName string,
	flushOpName string,
	combinerFactory psg.CombinerFactory[I, O],
) psg.CombinerFactory[I, O] {
	return func() psg.Combiner[I, O] {
		innerCombiner := combinerFactory()

		return psg.FuncCombiner[I, O]{
			CombineFunc: func(ctx context.Context, input I, inputErr error, emit psg.CombinerEmitFunc[O]) {
				// Get logger from context or use a default
				logger := zap.L()

				// Log starting combine operation
				logger.Debug("Combining input",
					zap.String("operation", combineOpName),
					zap.String("component", "otpsg"),
					zap.Bool("input_has_error", inputErr != nil))

				// Time the operation
				startTime := time.Now()
				innerCombiner.Combine(ctx, input, inputErr, emit)
				duration := time.Since(startTime)

				// Log completion
				logger.Debug("Combine completed",
					zap.String("operation", combineOpName),
					zap.String("component", "otpsg"),
					zap.Duration("duration", duration))
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[O]) {
				// Get logger from context or use a default
				logger := zap.L()

				// Log starting flush operation
				logger.Debug("Flushing combiner",
					zap.String("operation", flushOpName),
					zap.String("component", "otpsg"))

				// Time the operation
				startTime := time.Now()
				innerCombiner.Flush(ctx, emit)
				duration := time.Since(startTime)

				// Log completion
				logger.Debug("Flush completed",
					zap.String("operation", flushOpName),
					zap.String("component", "otpsg"),
					zap.Duration("duration", duration))
			},
		}
	}
}
