// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	psg "github.com/petenewcomb/psg-go"
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
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	gate := make(chan struct{})
	launcher := psg.NewFnLauncher(wave, func(ctx context.Context, unit int, _ error) error {
		switch unit {
		case 1:
			subCtx, subWave := psg.NewWave(ctx)
			sub := psg.NewTaskLauncher(subWave, func(ctx context.Context) error {
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
	}, psg.WithLimits(psg.NewSemaphore(nil, 1)))

	require.NoError(t, launcher.Submit(ctx, 1))
	require.NoError(t, launcher.Submit(ctx, 2))
	require.NoError(t, wave.CloseAndSkimAll(ctx))
}
