// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/assert"
)

// passthroughTestAccumulator stores the most recently received value and
// Submits it to the captured downstream Skimmer on Flush.
type passthroughTestAccumulator[T any] struct {
	t       *testing.T
	value   T
	skimmer streampool.Skimmer[T]
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
	t *testing.T, skimmer streampool.Skimmer[T],
) streampool.AccumulatorFactoryFunc[T] {
	return func() streampool.Accumulator[T] {
		return &passthroughTestAccumulator[T]{t: t, skimmer: skimmer}
	}
}

// Exercises the NewErrFunnel convenience constructor — the err-aggregating
// funnel shape — end-to-end.
func TestNewErrFunnel(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	wave := streampool.NewWave()

	funnelPool := wave
	// The funnel is unlimited, so its two SubmitErr inputs may be accumulated
	// concurrently on distinct instances; guard the shared slice accordingly.
	var seenMu sync.Mutex
	var seen []error
	funnel := streampool.NewErrFunnel(
		funnelPool,
		func(_ context.Context, err error) (time.Time, error) {
			seenMu.Lock()
			seen = append(seen, err)
			seenMu.Unlock()
			return time.Time{}, nil
		},
		nil, // no flush
	)
	var _ streampool.ErrFunnel = funnel //nolint:staticcheck // intentional alias type-check
	chk.NoError(funnel.SubmitErr(ctx, errors.New("first")))
	chk.NoError(funnel.SubmitErr(ctx, errors.New("second")))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Len(seen, 2)
}

func TestFunnelScatterNilSkimPanic(t *testing.T) {
	assert.PanicsWithValue(t, "handler must be non-nil", func() {
		streampool.NewSkimmer[int](nil)
	})
}

func TestFunnelScatterFromTask(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	wave := streampool.NewWave()

	skimmer := streampool.NewFnSkimmer(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	).In(wave)
	funnelPool := wave
	funnelOp := streampool.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[int](t, skimmer),
	)
	var innerRan atomic.Bool
	innerRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		innerRan.Store(true)
		return nil
	})
	outerRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// Task-to-task scatter into the ambient wave now succeeds.
		if err := innerRunner.Start(ctx); err != nil {
			return err
		}
		return funnelOp.Submit(ctx, 0)
	})
	chk.NoError(outerRunner.In(wave).Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.True(innerRan.Load(), "the task-to-task-scattered inner task must run")
}

func TestFunnelTaskCanScatterToSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	parentWave := streampool.NewWave()

	// Variable to track execution flow
	subJobTaskRan := false

	skimmer := streampool.NewFnSkimmer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	).In(parentWave)
	funnelPool := parentWave
	funnelOp := streampool.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	outerRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subWave := streampool.NewWave()

		// This should succeed - dispatching a task to the sub-wave's pool
		subSkimmer := streampool.NewFnSkimmer(
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, true)
		})
		chk.NoError(subRunner.In(subWave).Start(ctx))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(ctx))

		return funnelOp.Submit(ctx, true)
	})

	chk.NoError(outerRunner.In(parentWave).Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestFunnelTaskScattersToAmbientWave(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	parentWave := streampool.NewWave()

	skimmer := streampool.NewFnSkimmer(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	).In(parentWave)
	funnelPool := parentWave
	funnelOp := streampool.NewFunnel(
		funnelPool,
		newPassthroughTestFunnelFactory[bool](t, skimmer),
	)
	var innerRan atomic.Bool
	innerRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		innerRan.Store(true)
		return nil
	})
	outerRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// The inner runner has no bound wave, so it resolves the ambient wave
		// (parentWave, the wave this body runs in) — task-to-task scatter, now allowed.
		if err := innerRunner.Start(ctx); err != nil {
			return err
		}
		return funnelOp.Submit(ctx, true)
	})

	chk.NoError(outerRunner.In(parentWave).Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
	chk.True(innerRan.Load(), "the task-to-task-scattered inner task must run")
}
