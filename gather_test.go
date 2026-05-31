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

	chk.PanicsWithValue("task must be non-nil", func() {
		// Nil Task should panic at construction.
		psg.NewTaskRunner0(nil)
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
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	innerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx, wave)
			},
		)
		return gatherer.Submit(ctx, wave, 0)
	}))
	chk.NoError(outerRunner.Start(ctx, wave))
	chk.NoError(wave.CloseAndGatherAll(ctx))
}

func TestTaskCanStartTaskInSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subGatherer := psg.NewGatherer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subGatherer.Submit(ctx, subWave, true)
		}))
		chk.NoError(subRunner.Start(subCtx, subWave))

		// Gather all results in the sub-wave
		chk.NoError(subWave.CloseAndGatherAll(subCtx))

		return gatherer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndGatherAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestTaskCannotStartTaskOnParentPool(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	innerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here - parent pool task should not run")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx, parentWave)
			},
		)
		return gatherer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndGatherAll(ctx))
}

func TestTaskCannotGather(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	runner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue("Gather called from task context but allowed only by top-level or gather context", func() {
			_, _ = wave.TryGather(ctx)
		})
		return gatherer.Submit(ctx, wave, true)
	}))

	chk.NoError(runner.Start(ctx, wave))
	chk.NoError(wave.CloseAndGatherAll(ctx))
}

func TestTaskCannotGatherParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	gatherer := psg.NewGatherer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		subGatherer := psg.NewGatherer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			chk.PanicsWithValue("Context belongs to a child job", func() {
				_, _ = parentWave.TryGather(ctx)
			})
			return subGatherer.Submit(ctx, subWave, true)
		}))
		chk.NoError(subRunner.Start(subCtx, subWave))
		chk.NoError(subWave.CloseAndGatherAll(subCtx))
		return gatherer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndGatherAll(ctx))
}
