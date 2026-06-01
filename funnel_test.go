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
// Submits it to the captured downstream Skimmer on Flush.
type passthroughTestAccumulator[T any] struct {
	t       *testing.T
	value   T
	skimmer psg.Skimmer[T]
}

func (c *passthroughTestAccumulator[T]) Accumulate(
	ctx context.Context, value T, err error,
) (time.Time, error) {
	assert.NoError(c.t, err)
	c.value = value
	return time.Now(), nil
}

func (c *passthroughTestAccumulator[T]) Flush(ctx context.Context) error {
	return c.skimmer.Submit(ctx, c.value)
}

//nolint:thelper // not a test helper, but a factory function for creating a test accumulator
func newPassthroughTestFunnelFactory[T any](
	t *testing.T, skimmer psg.Skimmer[T],
) func() psgfn.Accumulator[T] {
	return func() psgfn.Accumulator[T] {
		return &passthroughTestAccumulator[T]{t: t, skimmer: skimmer}
	}
}

func TestFunnelScatterNilSkimPanic(t *testing.T) {
	ctx := context.Background()
	_, wave := psg.NewWave(ctx)
	defer wave.CancelAndWait()

	assert.PanicsWithValue(t, "handler must be non-nil", func() {
		psg.NewSkimmer[int](wave, nil)
	})
}

func TestFunnelScatterFromTask(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewSkimmer(wave, psgfn.HandlerFunc[int](
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	))
	funnelPool := psg.NewFunnelPool(wave.Pool())
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[int](t, skimmer),
	)
	defer funnelOp.Close()
	innerRunner := psg.NewLauncher(wave, psgfn.Task(func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	}))
	outerRunner := psg.NewLauncher(wave, psgfn.Task(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return funnelOp.Submit(ctx, 0)
	}))
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestFunnelTaskCanScatterToSubJob(t *testing.T) {
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
	funnelPool := psg.NewFunnelPool(parentWave.Pool())
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	defer funnelOp.Close()
	outerRunner := psg.NewLauncher(parentWave, psgfn.Task(func(ctx context.Context) error {
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
		subRunner := psg.NewLauncher(subWave, psgfn.Task(func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, true)
		}))
		chk.NoError(subRunner.Start(subCtx))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(subCtx))

		return funnelOp.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestFunnelTaskCannotScatterToParentJob(t *testing.T) {
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
	funnelPool := psg.NewFunnelPool(parentWave.Pool())
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	defer funnelOp.Close()
	innerRunner := psg.NewLauncher(parentWave, psgfn.Task(func(ctx context.Context) error {
		chk.Fail("Should not get here - parent task pool task should not run")
		return nil
	}))
	outerRunner := psg.NewLauncher(parentWave, psgfn.Task(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return funnelOp.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}
