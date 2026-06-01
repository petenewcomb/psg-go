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
	_, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	chk.PanicsWithValue("task must be non-nil", func() {
		// Nil Task should panic at construction.
		psg.NewLauncher0(wave, nil)
	})
}

func TestSkimScatterNilSkimPanic(t *testing.T) {
	chk := assert.New(t)
	_, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()
	chk.PanicsWithValue("handler must be non-nil", func() {
		psg.NewSkimmer[int](wave, nil)
	})
}

// Nil-sentinel resolution: a Skimmer constructed with nil wave is
// wave-independent. At dispatch, the wave is resolved from the
// ctx, which descends from a NewWave call.
func TestSkimmerNilWaveResolvesFromCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	var got int
	skimmer := psg.NewSkimmer(nil, psgfn.HandlerFunc[int](
		func(_ context.Context, v int, err error) error {
			chk.NoError(err)
			got = v
			return nil
		},
	))
	chk.NoError(skimmer.Submit(ctx, 42))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(42, got)
}

// Dispatching a nil-wave Skimmer on a ctx with no wave panics.
func TestSkimmerNilWaveDispatchWithoutCtxWavePanics(t *testing.T) {
	chk := assert.New(t)
	skimmer := psg.NewSkimmer(nil, psgfn.HandlerFunc[int](
		func(_ context.Context, _ int, _ error) error { return nil },
	))
	chk.PanicsWithValue(
		"op constructed with nil wave dispatched from a ctx with no wave (call NewWave first)",
		func() { _ = skimmer.Submit(context.Background(), 1) },
	)
}

// Same nil-sentinel resolution for Launcher0.
func TestLauncherNilWaveResolvesFromCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	ran := false
	runner := psg.NewLauncher0(nil, psgfn.TaskFunc0(func(_ context.Context) error {
		ran = true
		return nil
	}))
	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.True(ran)
}

// Documents a known limitation: a nil-wave Skimmer dispatched from
// inside a task body panics because the worker's ctx doesn't yet
// carry the dispatching wave. The fix lives in the worker plumbing
// (stamp the wave onto the task body's ctx) and is left for a
// follow-up to Thread B. For now, sinks used from inside task /
// skim / accumulate bodies must be constructed with an explicit
// wave.
func TestSkimmerNilWaveFromTaskBodyPanicsKnownLimitation(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(nil, psgfn.HandlerFunc[int](
		func(_ context.Context, _ int, _ error) error { return nil },
	))
	runner := psg.NewLauncher0(wave, psgfn.TaskFunc0(func(taskCtx context.Context) error {
		chk.PanicsWithValue(
			"op constructed with nil wave dispatched from a ctx with no wave (call NewWave first)",
			func() { _ = skimmer.Submit(taskCtx, 1) },
		)
		return nil
	}))
	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestLauncherStartFromTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(wave, psgfn.HandlerFunc[int](
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	))
	innerRunner := psg.NewLauncher0(wave, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewLauncher0(wave, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return skimmer.Submit(ctx, 0)
	}))
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCanStartTaskInSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	skimmer := psg.NewSkimmer(parentWave, psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	outerRunner := psg.NewLauncher0(parentWave, psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subSkimmer := psg.NewSkimmer(subWave, psgfn.HandlerFunc[bool](
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		))
		subRunner := psg.NewLauncher0(subWave, psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, true)
		}))
		chk.NoError(subRunner.Start(subCtx))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(subCtx))

		return skimmer.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestTaskCannotStartTaskOnParentPool(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewSkimmer(parentWave, psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	innerRunner := psg.NewLauncher0(parentWave, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("should not get here - parent pool task should not run")
		return nil
	}))
	outerRunner := psg.NewLauncher0(parentWave, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return skimmer.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkim(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(wave, psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	runner := psg.NewLauncher0(wave, psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue("Skim called from task context but allowed only by top-level or skim context", func() {
			_, _ = wave.TrySkim(ctx)
		})
		return skimmer.Submit(ctx, true)
	}))

	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkimParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewSkimmer(parentWave, psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	outerRunner := psg.NewLauncher0(parentWave, psgfn.TaskFunc0(func(ctx context.Context) error {
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		subSkimmer := psg.NewSkimmer(subWave, psgfn.HandlerFunc[bool](
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		))
		subRunner := psg.NewLauncher0(subWave, psgfn.TaskFunc0(func(ctx context.Context) error {
			chk.PanicsWithValue("Context belongs to a child job", func() {
				_, _ = parentWave.TrySkim(ctx)
			})
			return subSkimmer.Submit(ctx, true)
		}))
		chk.NoError(subRunner.Start(subCtx))
		chk.NoError(subWave.CloseAndSkimAll(subCtx))
		return skimmer.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}
