// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/stretchr/testify/assert"
)

func TestNewTaskRunnerNilTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	job := psg.New(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job)

	chk.PanicsWithValue("task must be non-nil", func() {
		// Nil Task should panic at construction.
		psg.NewTaskRunner0(pool, nil)
	})
}

func TestGatherScatterNilGatherPanic(t *testing.T) {
	chk := assert.New(t)
	chk.PanicsWithValue("gather function must be non-nil", func() {
		psg.NewGatherer[int](nil)
	})
}

func TestTaskRunnerStartFromTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	job := psg.New(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job)

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	innerRunner := psg.NewTaskRunner0(pool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(pool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return gatherer.Submit(ctx, job, 0, nil)
	}))
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestTaskCanStartTaskInSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentJob := psg.New(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob)

	// Variable to track execution flow
	subJobTaskRan := false

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskRunner0(parentPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-job inside the task
		subJob := psg.New(ctx)
		defer subJob.CancelAndWait()
		subPool := psg.NewTaskPool(subJob)

		// This should succeed - dispatching a task to the sub-job's pool
		subGatherer := psg.NewGatherer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskRunner0(subPool, psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subGatherer.Submit(ctx, subJob, true, nil)
		}))
		chk.NoError(subRunner.Start(ctx))

		// Gather all results in the sub-job
		chk.NoError(subJob.CloseAndGatherAll(ctx))

		return gatherer.Submit(ctx, parentJob, true, nil)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentJob.CloseAndGatherAll(ctx))

	// Verify the sub-job task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-job should have run")
}

func TestTaskCannotStartTaskOnParentPool(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	parentJob := psg.New(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob)

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	innerRunner := psg.NewTaskRunner0(parentPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here - parent pool task should not run")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(parentPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return gatherer.Submit(ctx, parentJob, true, nil)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}

func TestTaskCannotGather(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	job := psg.New(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job)

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	runner := psg.NewTaskRunner0(pool, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue("Gather called from task context but allowed only by top-level or gather context", func() {
			_, _ = job.TryGather(ctx)
		})
		return gatherer.Submit(ctx, job, true, nil)
	}))

	chk.NoError(runner.Start(ctx))
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestTaskCannotGatherParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	parentJob := psg.New(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob)

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskRunner0(parentPool, psgfn.TaskFunc0(func(ctx context.Context) error {
		subJob := psg.New(ctx)
		defer subJob.CancelAndWait()
		subPool := psg.NewTaskPool(subJob)

		subGatherer := psg.NewGatherer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskRunner0(subPool, psgfn.TaskFunc0(func(ctx context.Context) error {
			chk.PanicsWithValue("Context belongs to a child job", func() {
				_, _ = parentJob.TryGather(ctx)
			})
			return subGatherer.Submit(ctx, subJob, true, nil)
		}))
		chk.NoError(subRunner.Start(ctx))
		chk.NoError(subJob.CloseAndGatherAll(ctx))
		return gatherer.Submit(ctx, parentJob, true, nil)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}
