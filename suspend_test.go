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

// TestSubwaveTaskAdmittedWhileSiblingRuns is the black-box reproduction of the
// self-suspension gather wait (WORKING_NOTES diagnosis 5, traced 2026-07-19): a
// body drives a subwave whose task shares its limiter, so the drive bracket
// lends the body's permit to the episode — and a sibling unit, registered and
// waiting, takes the freed permit and keeps running. The subwave task's
// admission then finds the pool fully in use with one suspension outstanding:
// the suspension is its own chain's lend, resumable only after the subwave
// completes. If the admission waits on the running sibling, and the sibling's
// progress depends on the subwave task (here directly; in the traced sim wedge
// through the framework's own skim obligations), the wait is a deadlock. The
// lend is what vouches for the subwave task: it must be admitted.
//
// Ordering is reliable rather than strictly deterministic: one sleep covers
// the sibling's synchronous gate registration, and the pool's arrival-order
// barrier does the rest (the subwave task's fresh demand cannot bypass the
// sibling's standing head).
func TestSubwaveTaskAdmittedWhileSiblingRuns(t *testing.T) {
	t.Skip("reproduces the eager-confirm-latch wedge (WORKING_NOTES diagnosis 5); " +
		"un-skip and reshape with the agreed lazy-reacquire fix")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wave := streampool.NewWave()
	limiter := streampool.NewSemaphore(1)

	unit1Holding := make(chan struct{})
	unit2Submitted := make(chan struct{})
	subTaskRan := make(chan struct{})

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
				close(subTaskRan)
				return nil
			}).WithLimits(limiter)
			if err := sub.In(subWave).Start(ctx); err != nil {
				return err
			}
			return subWave.CloseAndSkimAll(ctx)
		case 2:
			// The running sibling: holds the permit until the subwave task —
			// which needs the same permit — has run.
			select {
			case <-subTaskRan:
			case <-ctx.Done():
				return ctx.Err()
			}
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
		t.Fatal("wedged: the subwave task's admission waited on the running sibling " +
			"while its own chain's suspension vouched for it")
	}
}
