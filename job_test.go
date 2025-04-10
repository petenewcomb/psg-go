// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestJobJobBoundPoolPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool
	pool := psg.NewPool(1)

	// Bind it to a job
	_ = psg.NewJob(ctx, pool)

	// Try to bind it to another job
	chk.PanicsWithValue("pool was already registered", func() {
		_ = psg.NewJob(ctx, pool)
	})
}

func TestJobSyncJobBoundPoolPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool
	pool := psg.NewPool(1)

	// Bind it to a job
	_ = psg.NewJob(ctx, pool)

	// Try to bind it to a SyncJob
	chk.PanicsWithValue("pool was already registered", func() {
		_ = psg.NewSyncJob(ctx, pool)
	})
}
