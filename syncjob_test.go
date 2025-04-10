// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestSyncJobJobBoundPoolPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool
	pool := psg.NewPool(1)

	// Bind it to a job
	_ = psg.NewSyncJob(ctx, pool)

	// Try to bind it to a SyncJob
	chk.PanicsWithValue("pool was already registered", func() {
		_ = psg.NewJob(ctx, pool)
	})
}

func TestSyncJobSyncJobBoundPoolPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool
	pool := psg.NewPool(1)

	// Bind it to a job
	_ = psg.NewSyncJob(ctx, pool)

	// Try to bind it to another job
	chk.PanicsWithValue("pool was already registered", func() {
		_ = psg.NewSyncJob(ctx, pool)
	})
}

func TestMultiGatherAllInvalidParallelism(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewSyncJob(ctx, pool)
	defer job.Cancel()

	chk.PanicsWithValue("parallelism is less than one", func() {
		_ = job.MultiGatherAll(ctx, 0)
	})
}

func TestMultiTryGatherAllInvalidParallelism(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewSyncJob(ctx, pool)
	defer job.Cancel()

	chk.PanicsWithValue("parallelism is less than one", func() {
		_ = job.MultiTryGatherAll(ctx, 0)
	})
}
