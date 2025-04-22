// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("task function must be non-nil", func() {
		_ = psg.Scatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
	})
}

func TestNilGatherFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("gather function must be non-nil", func() {
		_ = psg.Scatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
			nil, // Nil GatherFunc should panic
		)
	})
}

func TestPoolNotBoundPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool but don't associate it with a job
	pool := psg.NewPool(1)

	chk.PanicsWithValue("pool not bound to a job", func() {
		_ = psg.Scatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
	})
}

func TestNilTaskFuncPanicTryScatter(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("task function must be non-nil", func() {
		_, _ = psg.TryScatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
	})
}

func TestNilGatherFuncPanicTryScatter(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("gather function must be non-nil", func() {
		_, _ = psg.TryScatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
			nil, // Nil GatherFunc should panic
		)
	})
}

func TestPoolNotBoundPanicTryScatter(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool but don't associate it with a job
	pool := psg.NewPool(1)

	chk.PanicsWithValue("pool not bound to a job", func() {
		_, _ = psg.TryScatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
	})
}

func TestScatterFromTask(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)

	err := psg.Scatter(
		ctx,
		pool,
		func(ctx context.Context) (int, error) {
			chk.PanicsWithValue("psg.Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				chk.NoError(psg.Scatter(
					ctx,
					pool,
					func(ctx context.Context) (int, error) {
						chk.Fail("should not get here")
						return 0, nil
					},
					func(ctx context.Context, result int, err error) error {
						chk.NoError(err)
						chk.Fail("should not get here")
						return nil
					},
				))
			})
			return 0, nil
		},
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	chk.NoError(err)
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestTaskCanScatterToSubJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentPool := psg.NewPool(1)
	parentJob := psg.NewJob(ctx, parentPool)
	defer parentJob.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	err := psg.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subPool := psg.NewPool(1)
			subJob := psg.NewJob(ctx, subPool)
			defer subJob.CancelAndWait()

			// This should succeed - scattering a task to the sub-job's pool
			err := psg.Scatter(
				ctx,
				subPool,
				func(ctx context.Context) (bool, error) {
					subJobTaskRan = true
					return true, nil
				},
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			chk.NoError(err)

			// Gather all results in the sub-job
			chk.NoError(subJob.CloseAndGatherAll(ctx))

			return true, nil
		},
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))

	// Verify the sub-job task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-job should have run")
}

func TestTaskCannotScatterToParentJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentPool := psg.NewPool(1)
	parentJob := psg.NewJob(ctx, parentPool)
	defer parentJob.CancelAndWait()

	err := psg.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// This should panic - attempting to scatter to the parent job's pool
			// while inside a task of that same job
			chk.PanicsWithValue("psg.Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				_ = psg.Scatter(
					ctx,
					parentPool,
					func(ctx context.Context) (bool, error) {
						chk.Fail("Should not get here - parent pool task should not run")
						return false, nil
					},
					func(ctx context.Context, result bool, err error) error {
						chk.Fail("Should not get here - parent pool gather should not run")
						return nil
					},
				)
			})

			return true, nil
		},
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}
