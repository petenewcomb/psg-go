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

// TestFlowValuePropagates covers the core path-scoped propagation along a
// full causal chain: top-level scope → launcher body → sub-wave body
// (cross-wave dispatch) → skim handler driven from the body.
func TestFlowValuePropagates(t *testing.T) {
	const reqID = "req-42"
	chk := require.New(t)
	requestID := streampool.NewFlowKey[string]()

	var wave streampool.Wave
	var sawBody, sawNested, sawSkim atomic.Value

	collector := streampool.NewSkimmer(streampool.HandlerFunc[int](
		func(ctx context.Context, _ int, err error) error {
			if err != nil {
				return err
			}
			v, ok := requestID.From(ctx)
			sawSkim.Store([2]any{v, ok})
			return nil
		},
	))

	inner := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
		if err != nil {
			return err
		}
		v, ok := requestID.From(ctx)
		sawNested.Store([2]any{v, ok})
		return collector.Submit(ctx, 1) // ambient: lands in the sub-wave
	})

	outer := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
		if err != nil {
			return err
		}
		v, ok := requestID.From(ctx)
		sawBody.Store([2]any{v, ok})
		// Nested work goes to a sub-wave the body owns and drains.
		var sub streampool.Wave
		if err := inner.In(&sub).Submit(ctx, 0); err != nil {
			return err
		}
		return sub.CloseAndSkimAll(ctx)
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := outer.In(&wave).Submit(ctx, 0); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, requestID.Value(reqID))
	chk.NoError(err)

	chk.Equal([2]any{reqID, true}, sawBody.Load(), "launcher body")
	chk.Equal([2]any{reqID, true}, sawNested.Load(), "sub-wave body")
	chk.Equal([2]any{reqID, true}, sawSkim.Load(), "skim handler")
}

// TestFlowAbsent covers the truthful-absence cases: a never-stamped ctx, a
// dispatch outside any registering scope, and the zero key.
func TestFlowAbsent(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	// Never-stamped ctx.
	_, ok := key.From(context.Background())
	chk.False(ok)
	chk.False(tag.InFlow(context.Background()))

	// Zero identities read absent, never panic.
	var zeroKey streampool.FlowKey[string]
	var zeroTag streampool.FlowTag
	_, ok = zeroKey.From(context.Background())
	chk.False(ok)
	chk.False(zeroTag.InFlow(context.Background()))

	// A body dispatched outside any scope reads absent.
	var wave streampool.Wave
	var sawOK atomic.Bool
	sawOK.Store(true)
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		_, ok := key.From(ctx)
		sawOK.Store(ok)
		return nil
	})
	chk.NoError(task.In(&wave).Start(context.Background()))
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.False(sawOK.Load())
}

// TestWithFlowDegenerate: zero options must pass the ctx through untouched
// and return the body's error verbatim.
func TestWithFlowDegenerate(t *testing.T) {
	chk := require.New(t)
	type ctxKey struct{}
	base := context.WithValue(context.Background(), ctxKey{}, "x")
	err := streampool.WithFlow(base, func(ctx context.Context) error {
		chk.Equal(base, ctx, "zero-opt WithFlow must degenerate to body(ctx)")
		return context.DeadlineExceeded
	})
	chk.ErrorIs(err, context.DeadlineExceeded)
}

// TestWithFlowShadowing: a nested scope re-registering a key shadows the
// outer value for its extent; the outer scope is unaffected after it returns
// (rider sets are immutable snapshots).
func TestWithFlowShadowing(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()

	err := streampool.WithFlow(context.Background(), func(outerCtx context.Context) error {
		v, ok := key.From(outerCtx)
		chk.True(ok)
		chk.Equal("outer", v)

		err := streampool.WithFlow(outerCtx, func(innerCtx context.Context) error {
			v, ok := key.From(innerCtx)
			chk.True(ok)
			chk.Equal("inner", v)
			return nil
		}, key.Value("inner"))
		chk.NoError(err)

		// The outer ctx still reads the outer value after the inner scope.
		v, ok = key.From(outerCtx)
		chk.True(ok)
		chk.Equal("outer", v)
		return nil
	}, key.Value("outer"))
	chk.NoError(err)
}

// TestWithFlowInsideBody: opening a scope inside a body (the clone path) —
// the scope is transparent to wave resolution (ambient dispatch still works)
// and adds its rider to work dispatched within.
func TestWithFlowInsideBody(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[int]()

	var wave streampool.Wave
	var saw atomic.Value

	inner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		v, ok := key.From(ctx)
		saw.Store([2]any{v, ok})
		return nil
	})

	outer := streampool.NewTaskLauncher(func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			// Dispatch through the scope meta into a sub-wave the body drains.
			var sub streampool.Wave
			if err := inner.In(&sub).Start(ctx); err != nil {
				return err
			}
			return sub.CloseAndSkimAll(ctx)
		}, key.Value(7))
	})

	chk.NoError(outer.In(&wave).Start(context.Background()))
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Equal([2]any{7, true}, saw.Load())
}

// TestFlowSeverAtFlush: path-scoped values do not cross the funnel
// accumulate→flush fan-in. Covers both flush drives: the end-of-drain sweep
// (executor path) and the inline already-past-deadline flush, which runs on
// the triggering accumulate body's ctx and is the leak path the sever exists
// to close. Accumulate bodies themselves see their item's rider.
func TestFlowSeverAtFlush(t *testing.T) {
	for _, tc := range []struct {
		name string
		// deadline returned by Accumulate: zero → flushed by the drain sweep;
		// already-past → flushed inline on the accumulate ctx.
		deadline func() time.Time
	}{
		{"drain sweep flush", func() time.Time { return time.Time{} }},
		{"inline past-deadline flush", func() time.Time { return time.Now().Add(-time.Millisecond) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chk := require.New(t)
			key := streampool.NewFlowKey[string]()

			var wave streampool.Wave
			var accSaw, flushSaw atomic.Value

			aggregator := streampool.NewFnFunnel(&wave, func() streampool.Accumulator[int] {
				return streampool.NewAccumulator(
					func(ctx context.Context, _ int, err error) (time.Time, error) {
						if err != nil {
							return time.Time{}, err
						}
						v, ok := key.From(ctx)
						accSaw.Store([2]any{v, ok})
						return tc.deadline(), nil
					},
					func(ctx context.Context) error {
						v, ok := key.From(ctx)
						flushSaw.Store([2]any{v, ok})
						return nil
					},
				)
			})

			err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				if err := aggregator.Submit(ctx, 1); err != nil {
					return err
				}
				return wave.CloseAndSkimAll(ctx)
			}, key.Value("req-9"))
			chk.NoError(err)

			chk.Equal([2]any{"req-9", true}, accSaw.Load(), "accumulate sees its item's rider")
			chk.Equal([2]any{"", false}, flushSaw.Load(), "flush reads absent — fan-in severs")
		})
	}
}

// TestFlowZeroIdentityPanics: registration on a zero (unminted) identity is a
// bug and panics; reads never do (covered in TestFlowAbsent).
func TestFlowZeroIdentityPanics(t *testing.T) {
	chk := require.New(t)
	var zeroKey streampool.FlowKey[string]
	chk.PanicsWithValue(
		"streampool: Value called on a zero FlowKey; mint with NewFlowKey",
		func() { _ = zeroKey.Value("x") },
	)
	chk.Panics(func() {
		_ = streampool.WithFlow(context.Background(),
			func(context.Context) error { return nil },
			streampool.FlowOption{})
	})
}

// TestWithFlowNilBodyPanics: a nil body is a bug at the call site.
func TestWithFlowNilBodyPanics(t *testing.T) {
	require.New(t).Panics(func() {
		_ = streampool.WithFlow(context.Background(), nil)
	})
}

// ─── CP-F2: follow-ups ───────────────────────────────────────────────────────

// TestFollowUpFiresAtScopeExit: work drained inside the scope means the scope
// ref is the last carrier — the follow-up fires inline at WithFlow return,
// after the drain, exactly once.
func TestFollowUpFiresAtScopeExit(t *testing.T) {
	chk := require.New(t)
	checkout := streampool.NewFlowTag()

	var fires atomic.Int32
	var bodyRan atomic.Bool
	var firedBeforeReturn bool

	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		bodyRan.Store(true)
		return nil
	})

	var wave streampool.Wave
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := task.In(&wave).Start(ctx); err != nil {
			return err
		}
		if err := wave.CloseAndSkimAll(ctx); err != nil {
			return err
		}
		chk.EqualValues(0, fires.Load(), "must not fire while the scope holds its ref")
		return nil
	}, checkout.FollowUp(func(context.Context) { fires.Add(1) }))
	firedBeforeReturn = fires.Load() == 1
	chk.NoError(err)
	chk.True(bodyRan.Load())
	chk.True(firedBeforeReturn, "scope exit is the last release — fires inline at return")
	chk.EqualValues(1, fires.Load())
}

// TestFollowUpEmptyScope: a scope that dispatches nothing fires at return.
func TestFollowUpEmptyScope(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()
	var fires atomic.Int32
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		chk.True(tag.InFlow(ctx), "registration marks presence")
		return nil
	}, tag.FollowUp(func(context.Context) { fires.Add(1) }))
	chk.NoError(err)
	chk.EqualValues(1, fires.Load())
}

// TestFollowUpFiresAfterAsyncCompletion: work still in flight at scope exit
// keeps the instance alive; the last item's completion fires the follow-up on
// an executor worker (the scheduler-routed path).
func TestFollowUpFiresAfterAsyncCompletion(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()

	release := make(chan struct{})
	fired := make(chan struct{})

	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		return nil
	})

	var wave streampool.Wave
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return task.In(&wave).Start(ctx)
	}, tag.FollowUp(func(context.Context) { close(fired) }))
	chk.NoError(err)

	select {
	case <-fired:
		t.Fatal("fired while the dispatched body still held its ref")
	case <-time.After(10 * time.Millisecond):
	}

	close(release)
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up did not fire after the last carrier completed")
	}
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
}

// TestFollowUpExtension: a follow-up that dispatches async work extends the
// flow — a later nominal end fires it again; a firing that extends nothing is
// the true end (no third fire).
func TestFollowUpExtension(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()

	var fires atomic.Int32
	release := make(chan struct{})
	secondFire := make(chan struct{})
	var extWave streampool.Wave

	extTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		return nil
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return nil // empty scope: first fire happens at return
	}, tag.FollowUp(func(ctx context.Context) {
		switch fires.Add(1) {
		case 1:
			// Extend: async work under the firing instance (ambient bundle).
			chk.True(tag.InFlow(ctx), "fn ctx carries the firing bundle")
			chk.NoError(extTask.In(&extWave).Start(ctx))
		case 2:
			close(secondFire) // extend nothing: true end
		}
	}))
	chk.NoError(err)
	chk.EqualValues(1, fires.Load(), "first fire is inline at scope exit")

	close(release)
	select {
	case <-secondFire:
	case <-time.After(5 * time.Second):
		t.Fatal("extension's nominal end did not refire the follow-up")
	}
	chk.NoError(extWave.CloseAndSkimAll(context.Background()))
	time.Sleep(50 * time.Millisecond) // settle: no third fire may arrive
	chk.EqualValues(2, fires.Load(), "a firing that extends nothing is the true end")
}

// TestFollowUpBundleValue: a follow-up under a value-bearing key receives the
// bundle ambiently — the fn ctx reads the key's value — regardless of option
// order within the call.
func TestFollowUpBundleValue(t *testing.T) {
	chk := require.New(t)
	txn := streampool.NewFlowKey[string]()

	var got atomic.Value
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return nil
	},
		// FollowUp deliberately listed BEFORE Value: order-independent.
		txn.FollowUp(func(ctx context.Context) {
			v, ok := txn.From(ctx)
			got.Store([2]any{v, ok})
		}),
		txn.Value("tx-7"),
	)
	chk.NoError(err)
	chk.Equal([2]any{"tx-7", true}, got.Load())
}

// TestFollowUpConcurrentStress: many concurrent scopes, each with a follow-up
// and a burst of work items, hammering ref/unref and the firing state machine.
// Every follow-up must fire at least once, and the per-scope firing loop must
// resolve (run under -race in the suite).
func TestFollowUpConcurrentStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test; skipped in -short")
	}
	chk := require.New(t)
	const scopes = 32
	const items = 16

	var fired atomic.Int32
	errs := make(chan error, scopes)
	for s := 0; s < scopes; s++ {
		go func() {
			tag := streampool.NewFlowTag()
			var wave streampool.Wave
			task := streampool.NewTaskLauncher(func(ctx context.Context) error { return nil })
			errs <- streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				for i := 0; i < items; i++ {
					if err := task.In(&wave).Start(ctx); err != nil {
						return err
					}
				}
				return wave.CloseAndSkimAll(ctx)
			}, tag.FollowUp(func(context.Context) { fired.Add(1) }))
		}()
	}
	for s := 0; s < scopes; s++ {
		chk.NoError(<-errs)
	}
	chk.Eventually(func() bool { return fired.Load() == scopes },
		5*time.Second, 5*time.Millisecond)
}
