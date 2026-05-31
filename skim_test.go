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

func TestNewLauncherNilTaskPanic(t *testing.T) {
	chk := assert.New(t)

	chk.PanicsWithValue("task must be non-nil", func() {
		// Nil Task should panic at construction.
		psg.NewLauncher0(nil)
	})
}

func TestSkimScatterNilSkimPanic(t *testing.T) {
	chk := assert.New(t)
	chk.PanicsWithValue("handler must be non-nil", func() {
		psg.NewSkimmer[int](nil)
	})
}

func TestLauncherStartFromTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(psgfn.HandlerFunc[int](
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	))
	innerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx, wave)
			},
		)
		return skimmer.Submit(ctx, wave, 0)
	}))
	chk.NoError(outerRunner.Start(ctx, wave))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCanStartTaskInSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	skimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	outerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subSkimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		))
		subRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, subWave, true)
		}))
		chk.NoError(subRunner.Start(subCtx, subWave))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(subCtx))

		return skimmer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestTaskCannotStartTaskOnParentPool(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	innerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here - parent pool task should not run")
		return nil
	}))
	outerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx, parentWave)
			},
		)
		return skimmer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkim(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	runner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue("Skim called from task context but allowed only by top-level or skim context", func() {
			_, _ = wave.TrySkim(ctx)
		})
		return skimmer.Submit(ctx, wave, true)
	}))

	chk.NoError(runner.Start(ctx, wave))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkimParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	outerRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		subSkimmer := psg.NewSkimmer(psgfn.HandlerFunc[bool](
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		))
		subRunner := psg.NewLauncher0(psgfn.TaskFunc0(func(ctx context.Context) error {
			chk.PanicsWithValue("Context belongs to a child job", func() {
				_, _ = parentWave.TrySkim(ctx)
			})
			return subSkimmer.Submit(ctx, subWave, true)
		}))
		chk.NoError(subRunner.Start(subCtx, subWave))
		chk.NoError(subWave.CloseAndSkimAll(subCtx))
		return skimmer.Submit(ctx, parentWave, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}
