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
// Submits it to the captured downstream Gatherer on Flush.
type passthroughTestAccumulator[T any] struct {
	t        *testing.T
	value    T
	gatherer psg.Gatherer[T]
	wave     *psg.Wave
}

func (c *passthroughTestAccumulator[T]) Accumulate(
	ctx context.Context, value T, err error,
) (time.Time, error) {
	assert.NoError(c.t, err)
	c.value = value
	return time.Now(), nil
}

func (c *passthroughTestAccumulator[T]) Flush(ctx context.Context) error {
	return c.gatherer.Submit(ctx, c.wave, c.value)
}

//nolint:thelper // not a test helper, but a factory function for creating a test accumulator
func newPassthroughTestCombinerFactory[T any](
	t *testing.T, gatherer psg.Gatherer[T], wave *psg.Wave,
) func() psgfn.Accumulator[T] {
	return func() psgfn.Accumulator[T] {
		return &passthroughTestAccumulator[T]{t: t, gatherer: gatherer, wave: wave}
	}
}

func TestCombinerScatterNilGatherPanic(t *testing.T) {
	ctx := context.Background()
	_, wave := psg.NewWave(ctx)
	defer wave.CancelAndWait()

	assert.PanicsWithValue(t, "handler must be non-nil", func() {
		psg.NewGatherer[int](nil)
	})
}

func TestCombinerScatterFromTask(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	gatherer := psg.NewGatherer(psgfn.HandlerFunc[int](
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	))
	combinerPool := psg.NewCombinerPool(wave.Pool())
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[int](t, gatherer, wave),
	)
	defer combineOp.Close()
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
		return combineOp.Submit(ctx, 0)
	}))
	chk.NoError(outerRunner.Start(ctx, wave))
	chk.NoError(wave.CloseAndGatherAll(ctx))
}

func TestCombinerTaskCanScatterToSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	gatherer := psg.NewGatherer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	combinerPool := psg.NewCombinerPool(parentWave.Pool())
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t, gatherer, parentWave),
	)
	defer combineOp.Close()
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subGatherer := psg.NewGatherer(psgfn.HandlerFunc[bool](
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		))
		subRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			subJobTaskRan = true
			return subGatherer.Submit(ctx, subWave, true)
		}))
		chk.NoError(subRunner.Start(subCtx, subWave))

		// Gather all results in the sub-wave
		chk.NoError(subWave.CloseAndGatherAll(subCtx))

		return combineOp.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndGatherAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestCombinerTaskCannotScatterToParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	gatherer := psg.NewGatherer(psgfn.HandlerFunc[bool](
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	))
	combinerPool := psg.NewCombinerPool(parentWave.Pool())
	combineOp := psg.NewCombiner(
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t, gatherer, parentWave),
	)
	defer combineOp.Close()
	innerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.Fail("Should not get here - parent task pool task should not run")
		return nil
	}))
	outerRunner := psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, gather, or combine context",
			func() {
				_ = innerRunner.Start(ctx, parentWave)
			},
		)
		return combineOp.Submit(ctx, true)
	}))

	chk.NoError(outerRunner.Start(ctx, parentWave))
	chk.NoError(parentWave.CloseAndGatherAll(ctx))
}
