// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"errors"
	"strings"
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
	}, checkout.FollowUpFn(func(context.Context) error { fires.Add(1); return nil }))
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
	}, tag.FollowUpFn(func(context.Context) error { fires.Add(1); return nil }))
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
	}, tag.FollowUpFn(func(context.Context) error { close(fired); return nil }))
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

// TestFollowUpFiresOnce: a follow-up fires exactly once at its end. Its own
// rider is peeled inside the body (InFlow reads false), so async work it
// dispatches does NOT re-fire it — re-extending under its identity would be an
// explicit re-stamp.
func TestFollowUpFiresOnce(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()

	var fires atomic.Int32
	release := make(chan struct{})
	var extWave streampool.Wave

	extTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		return nil
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return nil // empty scope: fires at return
	}, tag.FollowUpFn(func(ctx context.Context) error {
		fires.Add(1)
		chk.False(tag.InFlow(ctx), "the follow-up's own rider is peeled inside the body")
		// Dispatch async work — it does not carry the tag, so it cannot re-fire.
		return extTask.In(&extWave).Start(ctx)
	}))
	chk.NoError(err)
	chk.EqualValues(1, fires.Load(), "fires once, inline at scope exit")

	close(release)
	chk.NoError(extWave.CloseAndSkimAll(context.Background()))
	time.Sleep(50 * time.Millisecond) // settle: no second fire may arrive
	chk.EqualValues(1, fires.Load(), "the extension did not re-fire the follow-up")
}

// TestFollowUpNestedCoupling: an inner (later-registered) follow-up that extends
// the flow holds the outer follow-up open until the extension drains — defer-
// style LIFO nesting. The inner fires first (inline at scope exit); the outer
// fires only after the inner's async extension completes.
func TestFollowUpNestedCoupling(t *testing.T) {
	chk := require.New(t)
	outer := streampool.NewFlowTag()
	inner := streampool.NewFlowTag()

	release := make(chan struct{})
	var extDone atomic.Bool
	var extWave streampool.Wave
	extTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		extDone.Store(true)
		return nil
	})

	var innerFired, outerAfterExt atomic.Bool
	outerFired := make(chan struct{})
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return nil
	},
		outer.FollowUpFn(func(context.Context) error {
			outerAfterExt.Store(extDone.Load())
			close(outerFired)
			return nil
		}),
		inner.FollowUpFn(func(ctx context.Context) error {
			innerFired.Store(true)
			chk.True(outer.InFlow(ctx), "inner fn sees the enclosing outer rider")
			chk.False(inner.InFlow(ctx), "inner's own rider is peeled")
			// Extend under the enclosing set (outer still present): the extension
			// references outer, holding it open until it drains.
			return extTask.In(&extWave).Start(ctx)
		}),
	)
	chk.NoError(err)
	chk.True(innerFired.Load(), "inner fires inline at scope exit")
	select {
	case <-outerFired:
		t.Fatal("outer fired before the inner's extension drained")
	default:
	}

	close(release)
	chk.NoError(extWave.CloseAndSkimAll(context.Background()))
	select {
	case <-outerFired:
	case <-time.After(5 * time.Second):
		t.Fatal("outer follow-up did not fire after the extension drained")
	}
	chk.True(outerAfterExt.Load(), "outer fired only after the extension completed")
}

// TestFollowUpInlineErrors: follow-ups firing inline at scope exit join their
// errors into WithFlow's return — body error FIRST, then follow-ups in LIFO
// (innermost-first) order.
func TestFollowUpInlineErrors(t *testing.T) {
	chk := require.New(t)
	outer := streampool.NewFlowTag()
	inner := streampool.NewFlowTag()

	errBody := errors.New("body-err")
	errOuter := errors.New("outer-err")
	errInner := errors.New("inner-err")

	err := streampool.WithFlow(context.Background(), func(context.Context) error {
		return errBody
	},
		outer.FollowUpFn(func(context.Context) error { return errOuter }),
		inner.FollowUpFn(func(context.Context) error { return errInner }),
	)
	chk.ErrorIs(err, errBody)
	chk.ErrorIs(err, errOuter)
	chk.ErrorIs(err, errInner)
	// Order: body first, then follow-ups LIFO (inner before outer).
	msg := err.Error()
	chk.True(
		strings.Index(msg, "body-err") < strings.Index(msg, "inner-err") &&
			strings.Index(msg, "inner-err") < strings.Index(msg, "outer-err"),
		"order must be body, inner, outer: %q", msg)
}

// TestFollowUpErrorCrossesFunnel: a tag follow-up whose last carrier is a funnel's
// adopted tag-union ref fires from the flush; if it errors, the error must surface
// via the funnel wave's drain. Exercises the flush-defer ordering — the fire's
// wave keep-alive is taken while the funnel barrier still holds the wave open.
func TestFollowUpErrorCrossesFunnel(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()
	errFollowUp := errors.New("followup-across-funnel")

	var wave streampool.Wave
	aggregator := streampool.NewFnFunnel(&wave, func() streampool.Accumulator[int] {
		return streampool.NewAccumulator(
			func(_ context.Context, _ int, err error) (time.Time, error) { return time.Time{}, err },
			func(context.Context) error { return nil },
		)
	})

	// Scope submits one item then returns; the tag rides the fan-in and the
	// follow-up's last carrier becomes the funnel's adopted union ref, released
	// at flush.
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return aggregator.Submit(ctx, 1)
	}, tag.FollowUpFn(func(context.Context) error { return errFollowUp })))

	err := wave.CloseAndSkimAll(context.Background())
	chk.ErrorIs(err, errFollowUp,
		"an erroring tag follow-up fired from the flush must surface via the wave drain")
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
		// The value is delivered as the handler argument (end-state channel).
		txn.FollowUpFn(func(ctx context.Context, v string) error {
			got.Store([2]any{v, true})
			return nil
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
			}, tag.FollowUpFn(func(context.Context) error { fired.Add(1); return nil }))
		}()
	}
	for s := 0; s < scopes; s++ {
		chk.NoError(<-errs)
	}
	chk.Eventually(func() bool { return fired.Load() == scopes },
		5*time.Second, 5*time.Millisecond)
}

// ─── CP-F3: fan-in transfer/union ────────────────────────────────────────────

// TestFlowTagCrossesFunnel: a tag's presence and follow-up lifetime union
// through the accumulate→flush fan-in, on both flush drives. The follow-up
// must not fire while the aggregate is pending (the funnel's collected ref
// covers the gap after the accumulate items complete), the flush body must
// read the tag as present while the key's value stays severed, and work the
// flush dispatches downstream keeps the flow alive until IT completes.
func TestFlowTagCrossesFunnel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline func() time.Time
	}{
		{"drain sweep flush", func() time.Time { return time.Time{} }},
		{"inline past-deadline flush", func() time.Time { return time.Now().Add(-time.Millisecond) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chk := require.New(t)
			checkout := streampool.NewFlowTag()
			reqCtx := streampool.NewFlowKey[string]()

			var fires atomic.Int32
			var flushSawTag, flushSawVal, downSawTag atomic.Bool
			var firedBeforeFlush atomic.Bool
			downRelease := make(chan struct{})
			fired := make(chan struct{})

			downstream := streampool.NewTaskLauncher(func(ctx context.Context) error {
				downSawTag.Store(checkout.InFlow(ctx))
				<-downRelease
				return nil
			})

			var wave streampool.Wave
			aggregator := streampool.NewFnFunnel(&wave, func() streampool.Accumulator[int] {
				return streampool.NewAccumulator(
					func(ctx context.Context, _ int, err error) (time.Time, error) {
						if err != nil {
							return time.Time{}, err
						}
						return tc.deadline(), nil
					},
					func(ctx context.Context) error {
						firedBeforeFlush.Store(fires.Load() > 0)
						flushSawTag.Store(checkout.InFlow(ctx))
						_, ok := reqCtx.From(ctx)
						flushSawVal.Store(ok)
						return downstream.Submit(ctx, struct{}{}) // ambient: same wave, tagged
					},
				)
			})

			err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				for i := 0; i < 3; i++ {
					if err := aggregator.Submit(ctx, i); err != nil {
						return err
					}
				}
				return nil
			}, checkout.FollowUpFn(func(context.Context) error { fires.Add(1); close(fired); return nil }),
				reqCtx.Value("req-77"))
			chk.NoError(err)

			// Scope exited; accumulate items complete as they run; the funnel's
			// collected ref must keep the tag alive while the aggregate pends.
			go func() {
				time.Sleep(20 * time.Millisecond)
				close(downRelease)
			}()
			chk.NoError(wave.CloseAndSkimAll(context.Background()))

			select {
			case <-fired:
			case <-time.After(5 * time.Second):
				t.Fatal("follow-up never fired after flush + downstream completion")
			}
			chk.False(firedBeforeFlush.Load(), "fired before the flush ran — the fan-in transfer leaked the lifetime")
			chk.True(flushSawTag.Load(), "tag presence must cross the fan-in")
			chk.False(flushSawVal.Load(), "key value must sever at the fan-in")
			chk.True(downSawTag.Load(), "flush-dispatched work inherits the tag")
			chk.EqualValues(1, fires.Load())
		})
	}
}

// TestFlowTagFunnelUnion: items from two independent scopes (two tags) fold
// into one funnel instance; both follow-ups survive to the flush and fire
// after it — the union, not last-writer-wins.
func TestFlowTagFunnelUnion(t *testing.T) {
	chk := require.New(t)
	tagA := streampool.NewFlowTag()
	tagB := streampool.NewFlowTag()

	var firedA, firedB atomic.Int32
	var flushSawA, flushSawB atomic.Bool

	var wave streampool.Wave
	aggregator := streampool.NewFnFunnel(&wave, func() streampool.Accumulator[int] {
		return streampool.NewAccumulator(
			func(ctx context.Context, _ int, err error) (time.Time, error) {
				return time.Time{}, err
			},
			func(ctx context.Context) error {
				flushSawA.Store(tagA.InFlow(ctx))
				flushSawB.Store(tagB.InFlow(ctx))
				return nil
			},
		)
	})

	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return aggregator.Submit(ctx, 1)
	}, tagA.FollowUpFn(func(context.Context) error { firedA.Add(1); return nil })))
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return aggregator.Submit(ctx, 2)
	}, tagB.FollowUpFn(func(context.Context) error { firedB.Add(1); return nil })))

	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Eventually(func() bool { return firedA.Load() == 1 && firedB.Load() == 1 },
		5*time.Second, 2*time.Millisecond)
	chk.True(flushSawA.Load(), "union must carry scope A's tag")
	chk.True(flushSawB.Load(), "union must carry scope B's tag")
}

// ─── CP-F4: suppression, fresh roots, allocation guards ─────────────────────

// TestFlowSuppress: a suppressed key reads absent inside the scope (and in
// work dispatched there) while the outer scope is unaffected; a suppressed
// tag's follow-up reaches its nominal end without waiting for work in the
// suppressed subtree.
func TestFlowSuppress(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	release := make(chan struct{})
	fired := make(chan struct{})
	var suppressedBodySaw atomic.Value

	blocked := streampool.NewTaskLauncher(func(ctx context.Context) error {
		_, keyOK := key.From(ctx)
		suppressedBodySaw.Store([2]bool{keyOK, tag.InFlow(ctx)})
		<-release
		return nil
	})

	var wave streampool.Wave
	err := streampool.WithFlow(context.Background(), func(outerCtx context.Context) error {
		return streampool.WithFlow(outerCtx, func(inCtx context.Context) error {
			_, ok := key.From(inCtx)
			chk.False(ok, "suppressed key reads absent in the scope")
			chk.False(tag.InFlow(inCtx), "suppressed tag reads absent in the scope")
			// Long-running work under the suppressed scope takes no refs.
			return blocked.In(&wave).Start(inCtx)
		}, key.Suppress(), tag.Suppress())
	}, key.Value("outer"), tag.FollowUpFn(func(context.Context) error { close(fired); return nil }))
	chk.NoError(err)

	// The tag's only carriers were the scope and unsuppressed items (none):
	// it must fire even though the suppressed-subtree body still runs (or has
	// not even started — its refs were never taken, which is the point).
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("suppressed subtree delayed the follow-up's nominal end")
	}
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Equal([2]bool{false, false}, suppressedBodySaw.Load(),
		"suppressed identities read absent in the subtree body")
}

// TestFlowNewFlowRoot: NewFlow() clears the whole inherited set; sibling
// options add to the fresh root, in any order.
func TestFlowNewFlowRoot(t *testing.T) {
	chk := require.New(t)
	inherited := streampool.NewFlowKey[string]()
	freshKey := streampool.NewFlowKey[int]()

	err := streampool.WithFlow(context.Background(), func(outerCtx context.Context) error {
		return streampool.WithFlow(outerCtx, func(inCtx context.Context) error {
			_, ok := inherited.From(inCtx)
			chk.False(ok, "fresh root inherits nothing")
			v, ok := freshKey.From(inCtx)
			chk.True(ok)
			chk.Equal(9, v)
			return nil
		}, freshKey.Value(9), streampool.NewFlow()) // NewFlow listed last: order-independent
	}, inherited.Value("outer"))
	chk.NoError(err)
}

// TestFlowAllocFloors guards the flow cost model: the degenerate WithFlow is
// free; a value-registering scope pays a small constant (scope meta + rider
// snapshot + ctxpool child on first use — never per dispatch); reads are
// free. Floors use the allocsPerOp minimum like the other alloc guards.
func TestFlowAllocFloors(t *testing.T) {
	key := streampool.NewFlowKey[int]()
	body := func(context.Context) error { return nil }
	ctx := context.Background()

	degenerate := allocsPerOp(t, 100, 1000, func() {
		_ = streampool.WithFlow(ctx, body)
	})
	if degenerate != 0 {
		t.Errorf("zero-opt WithFlow allocates %v/op; must be 0", degenerate)
	}

	opt := key.Value(7) // reused option value: measures the scope, not boxing
	scope := allocsPerOp(t, 100, 1000, func() {
		_ = streampool.WithFlow(ctx, body, opt)
	})
	// Chain representation with CP-R2a pooling (docs/decisions/flow-rider-chain.md):
	// the scope meta and its ctxpool child are pooled, leaving just the one GC-owned
	// rider node. Lowered 6 → 4 (chain removed the flat snapshot/slice pair) → 1
	// (R2a pooled the meta + ctxpool child); drops to 0 when CP-R2b pools the node.
	const scopeCeiling = 1
	if scope > scopeCeiling {
		t.Errorf("value-registering WithFlow allocates %v/op; ceiling %d", scope, scopeCeiling)
	}

	// Read inside the scope: the scope ctx and its meta are pooled and freed at
	// WithFlow return (retain-of-a-scope-ctx is undefined), so From must be
	// measured while the scope is live.
	var reads float64
	_ = streampool.WithFlow(ctx, func(c context.Context) error {
		reads = allocsPerOp(t, 100, 1000, func() {
			_, _ = key.From(c)
		})
		return nil
	}, key.Value(7))
	if reads != 0 {
		t.Errorf("key.From allocates %v/op; must be 0", reads)
	}
}

// TestFlowScopeWithLimiter: a limiter-bound dispatch from inside a top-level
// WithFlow scope — the wave-less scope meta must be transparent to the permit
// cache-ancestry walk (regression: ensureCache/ensureCacheChain panicked on a
// nil wave; found by the sim's flow oracle on its first run).
func TestFlowScopeWithLimiter(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[int]()
	sem := streampool.NewSemaphore(2)

	var wave streampool.Wave
	var saw atomic.Value
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		v, ok := key.From(ctx)
		saw.Store([2]any{v, ok})
		return nil
	}, streampool.WithLimits(sem))

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := task.In(&wave).Start(ctx); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, key.Value(3))
	chk.NoError(err)
	chk.Equal([2]any{3, true}, saw.Load())
}
