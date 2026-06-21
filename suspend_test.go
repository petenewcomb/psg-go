// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestSuspendDuringSubwaveAllowsSibling pins the limiter suspend/resume
// fix end to end (docs/limiter-suspend-resume.md): a permit gates active
// computation, not blocked-waiting, so a body parked driving a subwave
// relinquishes its slot and a sibling unit of the same op can run.
//
// The construction makes the old livelock deterministic: unit 1 holds the
// only permit and drives a subwave whose task blocks until unit 2 — which
// needs that same permit — closes a channel. Without suspension this
// deadlocks (unit 1 holds the slot forever, unit 2 never runs); with the
// subwave-skim suspend bracket, unit 1's permit frees during
// CloseAndSkimAll, unit 2 runs and unblocks the subwave, and unit 1
// reclaims and completes.
func TestSuspendDuringSubwaveAllowsSibling(t *testing.T) {
	ctx, wave := streampool.NewWave(context.Background())
	defer wave.CancelAndWait()

	gate := make(chan struct{})
	launcher := streampool.NewFnLauncher(wave, func(ctx context.Context, unit int, _ error) error {
		switch unit {
		case 1:
			subCtx, subWave := streampool.NewWave(ctx)
			sub := streampool.NewTaskLauncher(subWave, func(ctx context.Context) error {
				select {
				case <-gate:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			if err := sub.Start(subCtx); err != nil {
				return err
			}
			return subWave.CloseAndSkimAll(subCtx)
		case 2:
			close(gate)
		}
		return nil
	}, streampool.WithLimits(streampool.NewSemaphore(1)))

	require.NoError(t, launcher.Submit(ctx, 1))
	require.NoError(t, launcher.Submit(ctx, 2))
	require.NoError(t, wave.CloseAndSkimAll(ctx))
}

// TestSkimHandlerDrivingSubwavePanics pins the Finding 10 constraint: a
// skim handler may not drive a subwave (it would monopolize the wave's
// sole serial skim driver and deadlock). Subwork from a skim handler must
// go through a funnel or a launched task instead.
func TestSkimHandlerDrivingSubwavePanics(t *testing.T) {
	ctx, wave := streampool.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := streampool.NewFnSkimmer(wave, func(ctx context.Context, _ int, _ error) error {
		subCtx, subWave := streampool.NewWave(ctx)
		defer subWave.CancelAndWait()
		return subWave.CloseAndSkimAll(subCtx) // disallowed: gather from a skim handler
	})

	require.NoError(t, skimmer.Submit(ctx, 1))
	require.Panics(t, func() {
		_ = wave.CloseAndSkimAll(ctx)
	})
}
