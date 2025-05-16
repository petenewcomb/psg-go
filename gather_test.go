// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestGatherScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job, 1)

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_ = gather.Scatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestGatherScatterNilGatherFuncPanic(t *testing.T) {
	chk := require.New(t)
	chk.PanicsWithValue("gather function must be non-nil", func() {
		psg.NewGather[int](nil)
	})
}

func TestGatherTryScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job, 1)

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_, _ = gather.TryScatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestGatherScatterGatherScatterFromTaskFunc(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		pool,
		func(ctx context.Context) (int, error) {
			chk.PanicsWithValue("Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				innerGather := psg.NewGather(
					func(ctx context.Context, result int, err error) error {
						chk.NoError(err)
						chk.Fail("should not get here")
						return nil
					},
				)
				chk.NoError(innerGather.Scatter(
					ctx,
					pool,
					func(ctx context.Context) (int, error) {
						chk.Fail("should not get here")
						return 0, nil
					},
				))
			})
			return 0, nil
		},
	)
	chk.NoError(err)
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestGatherScatterTaskFuncCanGatherScatterToSubJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob, 1)

	// Variable to track execution flow
	subJobTaskRan := false

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subJob := psg.NewJob(ctx)
			defer subJob.CancelAndWait()
			subPool := psg.NewTaskPool(subJob, 1)

			// This should succeed - scattering a task to the sub-job's pool
			gather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			err := gather.Scatter(
				ctx,
				subPool,
				func(ctx context.Context) (bool, error) {
					subJobTaskRan = true
					return true, nil
				},
			)
			chk.NoError(err)

			// Gather all results in the sub-job
			chk.NoError(subJob.CloseAndGatherAll(ctx))

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))

	// Verify the sub-job task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-job should have run")
}

func TestGatherScatterTaskFuncCannotGatherScatterToParentJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// This should panic - attempting to scatter to the parent job's pool
			// while inside a task of that same job
			innerGather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.Fail("Should not get here - parent pool gather should not run")
					return nil
				},
			)
			chk.PanicsWithValue("Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				_ = innerGather.Scatter(
					ctx,
					parentPool,
					func(ctx context.Context) (bool, error) {
						chk.Fail("Should not get here - parent pool task should not run")
						return false, nil
					},
				)
			})

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}

func TestGatherScatterTaskFuncCannotGather(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		pool,
		func(ctx context.Context) (bool, error) {
			chk.PanicsWithValue("Gather called from within TaskFunc of the same or a parent Job", func() {
				_, _ = job.TryGatherOne(ctx)
			})
			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestGatherScatterTaskFuncCannotGatherParentJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentPool := psg.NewTaskPool(parentJob, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subJob := psg.NewJob(ctx)
			defer subJob.CancelAndWait()
			subPool := psg.NewTaskPool(subJob, 1)

			// This should succeed - scattering a task to the sub-job's pool
			gather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			err := gather.Scatter(
				ctx,
				subPool,
				func(ctx context.Context) (bool, error) {
					chk.PanicsWithValue("Gather called from within TaskFunc of the same or a parent Job", func() {
						_, _ = parentJob.TryGatherOne(ctx)
					})
					return true, nil
				},
			)
			chk.NoError(err)

			// Gather all results in the sub-job
			chk.NoError(subJob.CloseAndGatherAll(ctx))

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}
