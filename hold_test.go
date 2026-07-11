// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestHoldFlowRetention: a held ctx is an ordinary GC-owned Go context — flow
// values and tag presence stay readable after the source scope exits, the
// follow-up waits for the release (the hold is a carrier), fires run BEFORE
// the cancel (asserted from inside the follow-up), the fire error merges into
// the cancellation cause, and — the snapshot contract — reads KEEP WORKING
// after release, with liveness signaled by Err(), not read availability.
func TestHoldFlowRetention(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()
	fireErr := errors.New("fire error")
	causeErr := errors.New("cause")

	var fires atomic.Int32
	var fireSawLive atomic.Bool
	var held context.Context
	var cancel context.CancelCauseFunc
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		held, cancel = streampool.HoldFlow(ctx)
		return nil
	}, key.Value("kept"), tag.FollowUpFn(func(context.Context) error {
		// Fires run before the cancel: held is readable and not yet canceled.
		_, ok := key.From(held)
		fireSawLive.Store(ok && held.Err() == nil)
		fires.Add(1)
		return fireErr
	})))

	// The scope has exited; the hold alone carries the flow.
	chk.Equal(int32(0), fires.Load(), "the hold is a carrier: the follow-up waits for the release")
	chk.NoError(held.Err())
	v, ok := key.From(held)
	chk.True(ok)
	chk.Equal("kept", v)
	chk.True(tag.InFlow(held))
	_, hasDeadline := held.Deadline()
	chk.False(hasDeadline)

	cancel(causeErr)
	chk.Equal(int32(1), fires.Load(), "the release ends the flow; the follow-up fires inline")
	chk.True(fireSawLive.Load(), "fires run before the cancel")
	chk.ErrorIs(held.Err(), context.Canceled)
	cause := context.Cause(held)
	chk.ErrorIs(cause, causeErr, "the caller's cause is delivered")
	chk.ErrorIs(cause, fireErr, "the follow-up's error merges into the cause, not swallowed")

	// The snapshot contract: reads keep working after release.
	v, ok = key.From(held)
	chk.True(ok, "post-release reads return the flow's riders as of the hold")
	chk.Equal("kept", v)
	chk.True(tag.InFlow(held), "tag presence reads from the snapshot too")

	// Idempotent: no panic, first cause wins, no re-fire.
	cancel(errors.New("second"))
	chk.Equal(int32(1), fires.Load())
	chk.ErrorIs(context.Cause(held), causeErr)
}

// TestHoldFlowNilCause: a nil cause defaults to context.Canceled — including
// when a fire error merges, so the fire error never becomes the PRIMARY cause.
func TestHoldFlowNilCause(t *testing.T) {
	chk := require.New(t)

	held, cancel := streampool.HoldFlow(context.Background())
	cancel(nil)
	chk.ErrorIs(held.Err(), context.Canceled)
	chk.ErrorIs(context.Cause(held), context.Canceled)

	fireErr := errors.New("fire error")
	tag := streampool.NewFlowTag()
	var h2 context.Context
	var c2 context.CancelCauseFunc
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		h2, c2 = streampool.HoldFlow(ctx)
		return nil
	}, tag.FollowUpFn(func(context.Context) error { return fireErr })))
	c2(nil)
	cause := context.Cause(h2)
	chk.ErrorIs(cause, context.Canceled, "nil cause defaults to Canceled as primary")
	chk.ErrorIs(cause, fireErr, "the fire error rides along")
}

// TestHoldFlowDispatch: dispatch through a held ctx during the hold — the
// body reads the flow's values, and the follow-up waits for BOTH the work and
// the release. Dispatch requires an explicit wave (a hold is wave-less).
func TestHoldFlowDispatch(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	var fired atomic.Bool
	var held context.Context
	var cancel context.CancelCauseFunc
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		held, cancel = streampool.HoldFlow(ctx)
		return nil
	}, key.Value("carried"), tag.FollowUpFn(func(context.Context) error {
		fired.Store(true)
		return nil
	})))

	var wave streampool.Wave
	var bodySaw atomic.Value
	blocked := make(chan struct{})
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-blocked
		v, ok := key.From(ctx)
		bodySaw.Store([2]any{v, ok})
		return nil
	})
	chk.NoError(task.In(&wave).Start(held))

	// Release while the dispatched body still runs: the work's own carrier
	// refs hold the flow, so the follow-up waits for the body too.
	cancel(nil)
	chk.False(fired.Load(), "follow-up waits for work dispatched through the hold")

	close(blocked)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Equal([2]any{"carried", true}, bodySaw.Load(), "the body inherits the held flow")
	chk.Eventually(fired.Load, 5*time.Second, 2*time.Millisecond,
		"the follow-up fires once hold and work have both released")

	// A hold carries no ambient wave.
	held2, cancel2 := streampool.HoldFlow(context.Background())
	defer cancel2(nil)
	chk.PanicsWithValue(
		"op constructed with nil wave dispatched without op.In(&wave) and outside any wave body",
		func() { _ = task.Start(held2) })
}

// TestHoldFlowPostReleaseDispatch: dispatch through a released hold is
// DEFINED — no panic, no undefined behavior: ordinary canceled-ctx dispatch
// carrying the value-only snapshot. Whether the framework accepts or rejects
// canceled-ctx work, it does so through its normal error paths.
func TestHoldFlowPostReleaseDispatch(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()

	var held context.Context
	var cancel context.CancelCauseFunc
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		held, cancel = streampool.HoldFlow(ctx)
		return nil
	}, key.Value("snap")))
	cancel(nil)

	var wave streampool.Wave
	task := streampool.NewTaskLauncher(func(ctx context.Context) error { return nil })
	chk.NotPanics(func() {
		if err := task.In(&wave).Start(held); err != nil {
			chk.ErrorIs(err, context.Canceled, "rejection, if any, is the ordinary canceled-ctx error")
		}
		//nolint:contextcheck // a fresh top-level drive ctx, by design
		_ = wave.CloseAndSkimAll(context.Background())
	})
}

// TestHoldFlowConcurrentReadsDuringRelease: the hold's core safety property —
// readers hammering the held ctx from many goroutines while the release runs
// never observe an unsafe or absent read: every read returns the snapshot
// value, before, during, and after the release. Run under -race.
func TestHoldFlowConcurrentReadsDuringRelease(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	var held context.Context
	var cancel context.CancelCauseFunc
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		held, cancel = streampool.HoldFlow(ctx)
		return nil
	}, key.Value("stable"), tag.FollowUpFn(func(context.Context) error { return nil })))

	const readers = 8
	var misses atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if v, ok := key.From(held); !ok || v != "stable" {
					misses.Add(1)
				}
				if !tag.InFlow(held) {
					misses.Add(1)
				}
				_ = held.Err()
				_ = context.Cause(held)
			}
		}()
	}
	time.Sleep(2 * time.Millisecond) // let readers spin up across the release
	cancel(nil)
	time.Sleep(2 * time.Millisecond) // and keep reading past it
	close(stop)
	wg.Wait()
	chk.Zero(misses.Load(), "every read returns the snapshot, before, during, and after release")
	chk.ErrorIs(held.Err(), context.Canceled)
}

// TestHoldFlowUnpinRejects: a held ctx is not a pin token.
func TestHoldFlowUnpinRejects(t *testing.T) {
	chk := require.New(t)
	held, cancel := streampool.HoldFlow(context.Background())
	defer cancel(nil)
	chk.Panics(func() { _ = streampool.UnpinFlow(held) })
}
