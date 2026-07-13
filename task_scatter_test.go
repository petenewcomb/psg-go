// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestTaskToTaskScatterSharedLimiter pins that task-to-task scatter is deadlock-safe
// under a shared limiter: an outer task holds the limiter's only permit and, from
// inside its body, dispatches an inner task into the same (ambient) wave under the
// same limiter (the self-acquisition shape). The permit gate postpones on the miss,
// so the outer body returns and frees the permit, then the inner runs.
func TestTaskToTaskScatterSharedLimiter(t *testing.T) {
	chk := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wave := streampool.NewWave()
	sem := streampool.NewSemaphore(1) // limit==1, shared by both ops

	var outerRan, innerRan atomic.Int32
	inner := streampool.NewTaskLauncher(func(_ context.Context) error {
		innerRan.Add(1)
		return nil
	}).WithLimits(sem)

	outer := streampool.NewTaskLauncher(func(bodyCtx context.Context) error {
		outerRan.Add(1)
		// Task-to-task scatter: inner has no bound wave, so it resolves the
		// ambient wave (this wave) and acquires the same sem — while this body
		// still holds sem's only permit.
		return inner.Start(bodyCtx)
	}).WithLimits(sem)

	chk.NoError(outer.In(wave).Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(int32(1), outerRan.Load(), "outer must run once")
	chk.Equal(int32(1), innerRan.Load(), "inner must run once")
}
