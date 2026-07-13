// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestSuspendDuringSubwaveAllowsSibling verifies end to end that a permit
// gates active computation, not blocked-waiting
// (docs/limiter-suspend-resume.md): a body parked driving a subwave
// relinquishes its slot and a sibling unit of the same op can run.
//
// The construction makes the failure mode deterministic: unit 1 holds the
// only permit and drives a subwave whose task blocks until unit 2 — which
// needs that same permit — closes a channel. Without suspension this
// deadlocks (unit 1 holds the slot forever, unit 2 never runs); with the
// subwave-skim suspend bracket, unit 1's permit frees during
// CloseAndSkimAll, unit 2 runs and unblocks the subwave, and unit 1
// reclaims and completes.
func TestSuspendDuringSubwaveAllowsSibling(t *testing.T) {
	ctx := context.Background()
	wave := streampool.NewWave()

	gate := make(chan struct{})
	launcher := streampool.NewFnLauncher(func(ctx context.Context, unit int, _ error) error {
		switch unit {
		case 1:
			subWave := streampool.NewWave()
			sub := streampool.NewTaskLauncher(func(ctx context.Context) error {
				select {
				case <-gate:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			if err := sub.In(subWave).Start(ctx); err != nil {
				return err
			}
			return subWave.CloseAndSkimAll(ctx)
		case 2:
			close(gate)
		}
		return nil
	}).WithLimits(streampool.NewSemaphore(1))

	require.NoError(t, launcher.In(wave).Submit(ctx, 1))
	require.NoError(t, launcher.In(wave).Submit(ctx, 2))
	require.NoError(t, wave.CloseAndSkimAll(ctx))
}

// TestSkimHandlerDrivingSubwavePanics pins the Finding 10 constraint: a
// skim handler may not drive a subwave (it would monopolize the wave's
// sole serial skim driver and deadlock). Subwork from a skim handler must
// go through a funnel or a launched task instead.
func TestSkimHandlerDrivingSubwavePanics(t *testing.T) {
	ctx := context.Background()
	wave := streampool.NewWave()

	skimmer := streampool.NewFnSkimmer(func(ctx context.Context, _ int, _ error) error {
		subWave := streampool.NewWave()
		return subWave.CloseAndSkimAll(ctx) // disallowed: gather from a skim handler
	})

	require.NoError(t, skimmer.In(wave).Submit(ctx, 1))
	require.Panics(t, func() {
		_ = wave.CloseAndSkimAll(ctx)
	})
}
