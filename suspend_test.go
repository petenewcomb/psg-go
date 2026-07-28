// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"
	"time"

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

// TestSubwaveTaskAdmittedAfterSiblingReleases pins the decided semantics of
// the eager-confirm-latch scenario (WORKING_NOTES diagnosis 5): a body drives a
// subwave whose task shares its limiter, the drive bracket lends the body's
// permit, and a sibling unit — registered first — takes the freed permit and
// runs. The subwave task's admission waits for the running sibling (a runner's
// release can move the world) and is admitted when the sibling's body returns;
// the lend's own-chain suspension does not block it (strangerSuspended), and
// nothing may latch a permit mid-help while the drive waits. Ordering is
// reliable rather than strictly deterministic: one sleep covers the sibling's
// synchronous gate registration; the pool's arrival-order barrier does the
// rest.
func TestSubwaveTaskAdmittedAfterSiblingReleases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wave := streampool.NewWave()
	limiter := streampool.NewSemaphore(1)

	unit1Holding := make(chan struct{})
	unit2Submitted := make(chan struct{})

	launcher := streampool.NewFnLauncher(func(ctx context.Context, unit int, _ error) error {
		switch unit {
		case 1:
			close(unit1Holding)
			select {
			case <-unit2Submitted:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Cover the gap between the sibling's Submit call and its gate
			// registration; from then on the arrival-order barrier holds it
			// ahead of the subwave task's demand.
			time.Sleep(100 * time.Millisecond)
			subWave := streampool.NewWave()
			sub := streampool.NewTaskLauncher(func(ctx context.Context) error {
				return nil
			}).WithLimits(limiter)
			if err := sub.In(subWave).Start(ctx); err != nil {
				return err
			}
			return subWave.CloseAndSkimAll(ctx)
		case 2:
			// The sibling runs and returns promptly; its release is what
			// admits the subwave task.
		}
		return nil
	}).WithLimits(limiter)

	require.NoError(t, launcher.In(wave).Submit(ctx, 1))

	<-unit1Holding
	submitErr := make(chan error, 1)
	go func() {
		close(unit2Submitted)
		submitErr <- launcher.In(wave).Submit(ctx, 2)
	}()

	done := make(chan error, 1)
	go func() {
		if err := <-submitErr; err != nil {
			done <- err
			return
		}
		done <- wave.CloseAndSkimAll(ctx)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("wedged: the subwave task was never admitted after the sibling released")
	}
}
