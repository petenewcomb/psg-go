// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
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
) psg.AccumulatorFactoryFunc[T] {
	return func() psg.Accumulator[T] {
		return &passthroughTestAccumulator[T]{t: t, skimmer: skimmer}
	}
}

// Verifies that AccumulatorFactory.Close fires when the bound
// Funnel's refcount hits zero (Funnel.Close on the last reference).
// Note: this test exercises the unused-factory case (no Submits).
// When Submits create funnelInstances, the factory remains
// referenced until those instances are fully flushed and freed —
// see funnelOp.unref() for the refcount details.
func TestFunnelFactoryCloseFires(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	funnelPool := wave
	closeCount := 0
	factory := psg.NewAccumulatorFactory(func() psg.Accumulator[int] {
		return psg.FuncAccumulator[int]{
			AccumulateFn: func(_ context.Context, _ int, _ error) (time.Time, error) {
				return time.Time{}, nil
			},
		}
	}, func() error {
		closeCount++
		return nil
	})
	funnel := psg.NewFunnel(funnelPool, factory)
	chk.Equal(0, closeCount, "Close should not fire while funnel is open")
	funnel.Close()
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(1, closeCount, "Close should fire exactly once on funnel teardown")
}

// NewErrFunnel convenience constructor — exercises the
// err-aggregating funnel shape end-to-end.
func TestNewErrFunnel(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	funnelPool := wave
	var seen []error
	funnel := psg.NewErrFunnel(
		funnelPool,
		func(_ context.Context, err error) (time.Time, error) {
			seen = append(seen, err)
			return time.Time{}, nil
		},
		nil, // no flush
		nil, // no close
	)
	var _ psg.ErrFunnel = funnel //nolint:staticcheck // intentional alias type-check
	chk.NoError(funnel.SubmitErr(ctx, errors.New("first")))
	chk.NoError(funnel.SubmitErr(ctx, errors.New("second")))
	funnel.Close()
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Len(seen, 2)
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

	skimmer := psg.NewFnSkimmer(wave,
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	funnelPool := wave
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[int](t, skimmer),
	)
	defer funnelOp.Close()
	innerRunner := psg.NewTaskLauncher(wave, func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	})
	outerRunner := psg.NewTaskLauncher(wave, func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return funnelOp.Submit(ctx, 0)
	})
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestFunnelTaskCanScatterToSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	skimmer := psg.NewFnSkimmer(parentWave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	funnelPool := parentWave
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	defer funnelOp.Close()
	outerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subSkimmer := psg.NewFnSkimmer(subWave,
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskLauncher(subWave, func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, true)
		})
		chk.NoError(subRunner.Start(subCtx))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(subCtx))

		return funnelOp.Submit(ctx, true)
	})

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestFunnelTaskCannotScatterToParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewFnSkimmer(parentWave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	funnelPool := parentWave
	funnelOp := psg.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	defer funnelOp.Close()
	innerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		chk.Fail("Should not get here - parent task pool task should not run")
		return nil
	})
	outerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return funnelOp.Submit(ctx, true)
	})

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}
