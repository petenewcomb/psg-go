// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/stretchr/testify/assert"
)

// passthroughTestAccumulator stores the most recently received value and
// Submits it to the captured downstream Gatherer on Flush. Replaces the
// pre-Wave-2 passthroughTestCombiner which returned the value as O.
type passthroughTestAccumulator[T any] struct {
	t        *testing.T
	value    T
	gatherer psg.Gatherer[T]
	job      *psg.Pool
}

func (c *passthroughTestAccumulator[T]) Accumulate(
	ctx context.Context, value T, err error,
) (time.Time, error) {
	assert.NoError(c.t, err)
	c.value = value
	return time.Now(), nil
}

func (c *passthroughTestAccumulator[T]) Flush(ctx context.Context) error {
	return c.gatherer.Submit(ctx, c.job, c.value, nil)
}

//nolint:thelper // not a test helper, but a factory function for creating a test accumulator
func newPassthroughTestCombinerFactory[T any](
	t *testing.T, gatherer psg.Gatherer[T], job *psg.Pool,
) func() psgfn.Accumulator[T] {
	return func() psgfn.Accumulator[T] {
		return &passthroughTestAccumulator[T]{t: t, gatherer: gatherer, job: job}
	}
}

func TestCombinerScatterNilGatherPanic(t *testing.T) {
	ctx := context.Background()
	job := psg.New(ctx)
	defer job.CancelAndWait()

	assert.PanicsWithValue(t, "gather function must be non-nil", func() {
		psg.NewGatherer[int](nil)
	})
}

func TestCombinerScatterFromTask(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	job := psg.New(ctx)
	defer job.CancelAndWait()
	taskPool := job

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(job)
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[int](t, gatherer, job),
	)
	defer combineOp.Close()
	innerRunner := psg.NewTaskRunner0(taskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(taskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return combineOp.Submit(ctx, 0, nil)
	}))
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestCombinerTaskCanScatterToSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.New(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := parentJob

	// Variable to track execution flow
	subJobTaskRan := false

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(parentJob)
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t, gatherer, parentJob),
	)
	defer combineOp.Close()
	outerRunner := psg.NewTaskRunner0(parentTaskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-job inside the task
		subJob := psg.New(ctx)
		defer subJob.CancelAndWait()
		subTaskPool := subJob

		// This should succeed - dispatching a task to the sub-job's task pool
		subGatherer := psg.NewGatherer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskRunner0(subTaskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subGatherer.Submit(ctx, subJob, true, nil)
		}))
		chk.NoError(subRunner.Start(ctx))

		// Gather all results in the sub-job
		chk.NoError(subJob.CloseAndGatherAll(ctx))

		return combineOp.Submit(ctx, true, nil)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentJob.CloseAndGatherAll(ctx))

	// Verify the sub-job task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-job should have run")
}

func TestCombinerTaskCannotScatterToParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.New(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := parentJob

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(parentJob)
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t, gatherer, parentJob),
	)
	defer combineOp.Close()
	innerRunner := psg.NewTaskRunner0(parentTaskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("Should not get here - parent task pool task should not run")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(parentTaskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return combineOp.Submit(ctx, true, nil)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}
