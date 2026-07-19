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

// TestSkimHandlerDrivesSubwave verifies that a skim handler may drive a subwave
// of its own: the blocking gather help-drains while its admission demand stays
// FIFO-registered, suspending whenever the handler's goroutine is off helping so
// capacity wakes route past it to a consumer that can act (the demand-suspension
// model — see internal/permits). Real subwork with a shared limiter exercises the
// nested block-and-help path end to end.
func TestSkimHandlerDrivesSubwave(t *testing.T) {
	ctx := context.Background()
	wave := streampool.NewWave()
	limiter := streampool.NewSemaphore(1)

	var subResults int
	skimmer := streampool.NewFnSkimmer(func(ctx context.Context, _ int, _ error) error {
		subWave := streampool.NewWave()
		sub := streampool.NewTaskLauncher(func(ctx context.Context) error {
			return nil
		}).WithLimits(limiter)
		if err := sub.In(subWave).Start(ctx); err != nil {
			return err
		}
		if err := subWave.CloseAndSkimAll(ctx); err != nil {
			return err
		}
		subResults++
		return nil
	})

	launcher := streampool.NewFnLauncher(func(ctx context.Context, unit int, _ error) error {
		return nil
	}).WithLimits(limiter)

	require.NoError(t, launcher.In(wave).Submit(ctx, 1))
	require.NoError(t, launcher.In(wave).Submit(ctx, 2))
	require.NoError(t, skimmer.In(wave).Submit(ctx, 3))
	require.NoError(t, wave.CloseAndSkimAll(ctx))
	require.Positive(t, subResults, "the skim handler drove its subwave to completion")
}
