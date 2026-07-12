// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"errors"
	"strings"
	"sync"
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

	wave := streampool.NewWave()
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
		sub := streampool.NewWave()
		if err := inner.In(sub).Submit(ctx, 0); err != nil {
			return err
		}
		return sub.CloseAndSkimAll(ctx)
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := outer.In(wave).Submit(ctx, 0); err != nil {
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
	wave := streampool.NewWave()
	var sawOK atomic.Bool
	sawOK.Store(true)
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		_, ok := key.From(ctx)
		sawOK.Store(ok)
		return nil
	})
	chk.NoError(task.In(wave).Start(context.Background()))
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

	wave := streampool.NewWave()
	var saw atomic.Value

	inner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		v, ok := key.From(ctx)
		saw.Store([2]any{v, ok})
		return nil
	})

	outer := streampool.NewTaskLauncher(func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			// Dispatch through the scope meta into a sub-wave the body drains.
			sub := streampool.NewWave()
			if err := inner.In(sub).Start(ctx); err != nil {
				return err
			}
			return sub.CloseAndSkimAll(ctx)
		}, key.Value(7))
	})

	chk.NoError(outer.In(wave).Start(context.Background()))
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Equal([2]any{7, true}, saw.Load())
}

// TestFlowFlushSeesEnclosing (CP-F8): the funnel flush sees the ENCLOSING
// (driver) flow's value intact — it is structural context above the fan-in —
// while a PER-ITEM value added within the funnel's wave severs. Covers both flush
// drives: the end-of-drain sweep (executor path) and the inline already-past-
// deadline flush, which runs on the triggering accumulate body's ctx. Accumulate
// bodies see both (their full item rider).
func TestFlowFlushSeesEnclosing(t *testing.T) {
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
			driverKey := streampool.NewFlowKey[string]()
			perItem := streampool.NewFlowKey[string]()

			wave := streampool.NewWave()
			var accDriver, accItem, flushDriver, flushItem atomic.Value

			aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
				return streampool.NewAccumulator(
					func(ctx context.Context, _ int, err error) (time.Time, error) {
						if err != nil {
							return time.Time{}, err
						}
						dv, dok := driverKey.From(ctx)
						iv, iok := perItem.From(ctx)
						accDriver.Store([2]any{dv, dok})
						accItem.Store([2]any{iv, iok})
						return tc.deadline(), nil
					},
					func(ctx context.Context) error {
						dv, dok := driverKey.From(ctx)
						iv, iok := perItem.From(ctx)
						flushDriver.Store([2]any{dv, dok})
						flushItem.Store([2]any{iv, iok})
						return nil
					},
				)
			})

			// A launcher on the funnel's OWN wave: its body opens a per-item scope
			// (running inside that wave) and submits to the funnel, so perItem is added
			// within the funnel's wave — below the fan-in boundary — and severs, while
			// driverKey (the enclosing driver's value, above the wave) crosses.
			launcher := streampool.NewTaskLauncher(func(ctx context.Context) error {
				return streampool.WithFlow(ctx, func(ctx context.Context) error {
					return aggregator.Submit(ctx, 1)
				}, perItem.Value("item-x"))
			})

			err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				if err := launcher.In(wave).Start(ctx); err != nil {
					return err
				}
				return wave.CloseAndSkimAll(ctx)
			}, driverKey.Value("req-9"))
			chk.NoError(err)

			chk.Equal([2]any{"req-9", true}, accDriver.Load(), "accumulate sees the driver value")
			chk.Equal([2]any{"item-x", true}, accItem.Load(), "accumulate sees its per-item value")
			chk.Equal([2]any{"req-9", true}, flushDriver.Load(), "CP-F8: enclosing driver value crosses to the flush")
			chk.Equal([2]any{"", false}, flushItem.Load(), "per-item value severs at the fan-in")
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

	wave := streampool.NewWave()
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := task.In(wave).Start(ctx); err != nil {
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

	wave := streampool.NewWave()
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return task.In(wave).Start(ctx)
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
	extWave := streampool.NewWave()

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
		return extTask.In(extWave).Start(ctx)
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
	extWave := streampool.NewWave()
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
			return extTask.In(extWave).Start(ctx)
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

	wave := streampool.NewWave()
	aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
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
		// The value is bound and delivered as the handler argument in one call —
		// no ambient lookup, no order dependence.
		txn.FollowUpFn("tx-7", func(ctx context.Context, v string) error {
			got.Store([2]any{v, true})
			return nil
		}),
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
			wave := streampool.NewWave()
			task := streampool.NewTaskLauncher(func(ctx context.Context) error { return nil })
			errs <- streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				for i := 0; i < items; i++ {
					if err := task.In(wave).Start(ctx); err != nil {
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

			wave := streampool.NewWave()
			aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
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
			// CP-F8: reqCtx is the ENCLOSING (driver) flow's value, above the fan-in,
			// so it crosses to the flush intact (only per-item values, added within the
			// funnel's wave, sever — see TestFlowFlushSeesEnclosing).
			chk.True(flushSawVal.Load(), "enclosing flow's value crosses the fan-in")
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

	wave := streampool.NewWave()
	aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
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

	wave := streampool.NewWave()
	err := streampool.WithFlow(context.Background(), func(outerCtx context.Context) error {
		return streampool.WithFlow(outerCtx, func(inCtx context.Context) error {
			_, ok := key.From(inCtx)
			chk.False(ok, "suppressed key reads absent in the scope")
			chk.False(tag.InFlow(inCtx), "suppressed tag reads absent in the scope")
			// Long-running work under the suppressed scope takes no refs.
			return blocked.In(wave).Start(inCtx)
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

// TestFlowDisconnectRoot: options apply left to right, one nested layer each
// (an option list is sugar for nested scopes). Disconnect drops the working
// set — the inherited chain AND earlier-listed siblings — so listed first it
// roots a fresh set that later siblings furnish; listed after a Value it
// drops that value too.
func TestFlowDisconnectRoot(t *testing.T) {
	chk := require.New(t)
	inherited := streampool.NewFlowKey[string]()
	freshKey := streampool.NewFlowKey[int]()

	err := streampool.WithFlow(context.Background(), func(outerCtx context.Context) error {
		// Disconnect FIRST: fresh root; later siblings add to it.
		if err := streampool.WithFlow(outerCtx, func(inCtx context.Context) error {
			_, ok := inherited.From(inCtx)
			chk.False(ok, "fresh root inherits nothing")
			v, ok := freshKey.From(inCtx)
			chk.True(ok)
			chk.Equal(9, v)
			return nil
		}, streampool.Disconnect(), freshKey.Value(9)); err != nil {
			return err
		}
		// Disconnect AFTER a Value: the earlier layer is dropped with the
		// working set.
		return streampool.WithFlow(outerCtx, func(inCtx context.Context) error {
			_, ok := inherited.From(inCtx)
			chk.False(ok, "inherited set dropped")
			_, ok = freshKey.From(inCtx)
			chk.False(ok, "a Value listed before Disconnect is dropped too")
			return nil
		}, freshKey.Value(9), streampool.Disconnect())
	}, inherited.Value("outer"))
	chk.NoError(err)
}

// TestFlowOptionOrder pins the left-to-right layering rule beyond Disconnect:
// a later Value shadows an earlier sibling (inner layer wins), Suppress acts
// on the chain as built so far (it can suppress an earlier sibling, and a
// later re-add lands after it), and a follow-up listed before Disconnect
// still registers — it gains no carriers from the body and fires at scope
// exit as an empty flow.
func TestFlowOptionOrder(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[int]()
	tag := streampool.NewFlowTag()

	// Later sibling shadows earlier.
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		v, ok := key.From(ctx)
		chk.True(ok)
		chk.Equal(2, v, "the later Value is the inner layer and wins")
		return nil
	}, key.Value(1), key.Value(2)))

	// Suppress acts on the working chain: it removes an earlier sibling…
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		chk.False(tag.InFlow(ctx), "Suppress removes the earlier sibling Infuse")
		return nil
	}, tag.Infuse(), tag.Suppress()))
	// …and a later re-add lands after it.
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		chk.True(tag.InFlow(ctx), "an Infuse after Suppress re-establishes presence")
		return nil
	}, tag.Suppress(), tag.Infuse()))

	// A follow-up listed before Disconnect registers in the outer layer: the
	// body runs disconnected from it, and it fires at scope exit, empty.
	var fired atomic.Bool
	var bodySawKey atomic.Bool
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		_, ok := key.From(ctx)
		bodySawKey.Store(ok)
		return nil
	}, key.FollowUpFn(7, func(context.Context, int) error {
		fired.Store(true)
		return nil
	}), streampool.Disconnect()))
	chk.False(bodySawKey.Load(), "the body runs under the post-Disconnect layer")
	chk.True(fired.Load(), "a pre-Disconnect follow-up still fires, at scope exit")
}

// TestFlowAllocFloors guards the flow cost model: the degenerate WithFlow is
// free; a value-registering scope pays a small constant (scope meta + rider
// snapshot + ctxpool child on first use — never per dispatch); reads are
// free. Floors use the allocsPerOp minimum like the other alloc guards.
func TestFlowAllocFloors(t *testing.T) {
	if raceEnabled {
		// Alloc floors are documented no-race runs: the race runtime adds its
		// own allocations, tripping the hard 0-floors spuriously.
		t.Skip("alloc floors are measured without the race detector")
	}
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
	// Chain representation with CP-R2 pooling (docs/decisions/flow-rider-chain.md):
	// the scope meta, its ctxpool child, and the refcounted rider node are all
	// pooled, so a value-registering scope allocates nothing warm. Lowered 6 → 4
	// (chain removed the flat snapshot/slice pair) → 1 (R2a pooled meta + ctxpool)
	// → 0 (R2b pooled the node). A hard floor now, like the degenerate case.
	const scopeCeiling = 0
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

	wave := streampool.NewWave()
	var saw atomic.Value
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		v, ok := key.From(ctx)
		saw.Store([2]any{v, ok})
		return nil
	}).WithLimits(sem)

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := task.In(wave).Start(ctx); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, key.Value(3))
	chk.NoError(err)
	chk.Equal([2]any{3, true}, saw.Load())
}

// ─── CP-R4: anonymous follow-up + bare presence ──────────────────────────────

// TestFlowFollowUpAnonymous: FlowFollowUp fires exactly once at the flow's true
// end and, being DAG-scoped, crosses a funnel fan-in — like a tag follow-up but
// with no name to query or suppress.
func TestFlowFollowUpAnonymous(t *testing.T) {
	chk := require.New(t)

	// Fires once at scope exit for a synchronous scope.
	var fired atomic.Int32
	chk.NoError(streampool.WithFlow(context.Background(), func(context.Context) error { return nil },
		streampool.FlowFollowUp(streampool.FlowTagFollowUpFunc(func(context.Context) error {
			fired.Add(1)
			return nil
		}))))
	chk.EqualValues(1, fired.Load())

	// Two anonymous registrations in one scope are independent (distinct minted
	// identities), so both fire.
	var a, b atomic.Int32
	chk.NoError(streampool.WithFlow(context.Background(), func(context.Context) error { return nil },
		streampool.FlowFollowUpFn(func(context.Context) error { a.Add(1); return nil }),
		streampool.FlowFollowUpFn(func(context.Context) error { b.Add(1); return nil })))
	chk.EqualValues(1, a.Load())
	chk.EqualValues(1, b.Load())

	// Crosses a funnel: the anonymous follow-up must not fire before the flush,
	// and fires once after the aggregate completes.
	var xfired atomic.Int32
	var firedBeforeFlush atomic.Bool
	wave := streampool.NewWave()
	aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
		return streampool.NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { firedBeforeFlush.Store(xfired.Load() > 0); return nil },
		)
	})
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		for i := 0; i < 3; i++ {
			if err := aggregator.Submit(ctx, i); err != nil {
				return err
			}
		}
		return nil
	}, streampool.FlowFollowUpFn(func(context.Context) error { xfired.Add(1); return nil }))
	chk.NoError(err)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Eventually(func() bool { return xfired.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	chk.False(firedBeforeFlush.Load(), "anonymous follow-up crossed the funnel — must not fire before flush")
}

// TestFlowTagInfuse: bare presence — InFlow reports the tag with no follow-up
// lifetime, presence crosses a funnel (ORs through), and Suppress clears it.
func TestFlowTagInfuse(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()

	// Present inside, absent outside; no follow-up means nothing to fire.
	var inScope, downstream atomic.Bool
	chk.False(tag.InFlow(context.Background()))

	wave := streampool.NewWave()
	downTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
		downstream.Store(tag.InFlow(ctx))
		return nil
	})
	aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
		return streampool.NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(ctx context.Context) error {
				chk.True(tag.InFlow(ctx), "infused presence crosses the fan-in to the flush")
				return downTask.Submit(ctx, struct{}{})
			},
		)
	})
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		inScope.Store(tag.InFlow(ctx))
		return aggregator.Submit(ctx, 1)
	}, tag.Infuse())
	chk.NoError(err)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.True(inScope.Load(), "Infuse marks presence in the scope")
	chk.True(downstream.Load(), "presence ORs through the fan-in to downstream work")

	// Suppress clears an infused presence in a nested scope.
	var sup atomic.Bool
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(c context.Context) error {
			sup.Store(tag.InFlow(c))
			return nil
		}, tag.Suppress())
	}, tag.Infuse()))
	chk.False(sup.Load(), "Suppress clears the infused presence in the subtree")
}

// ─── CP-F7: skim handlers as flow continuations ──────────────────────────────

// TestFlowSkimContinuation: a skim result is a CONTINUATION, not a fan-in — the
// handler runs under the PRODUCING ITEM's riders (item-over-driver shadowing),
// and a follow-up on the item's flow must not fire while the result awaits
// skimming.
func TestFlowSkimContinuation(t *testing.T) {
	chk := require.New(t)
	driverKey := streampool.NewFlowKey[string]()
	itemKey := streampool.NewFlowKey[string]()
	itemTag := streampool.NewFlowTag()

	wave := streampool.NewWave()
	var skimSawDriver, skimSawItem atomic.Value
	var skimRan, followUpSawSkim atomic.Bool

	collector := streampool.NewSkimmer(streampool.HandlerFunc[int](
		func(ctx context.Context, _ int, err error) error {
			if err != nil {
				return err
			}
			dv, dok := driverKey.From(ctx)
			iv, iok := itemKey.From(ctx)
			skimSawDriver.Store([2]any{dv, dok})
			skimSawItem.Store([2]any{iv, iok})
			// The item's own tag is peeled inside its handler? No — the tag rides the
			// continuation and is present (it is not the follow-up's own body here).
			chk.True(itemTag.InFlow(ctx), "item's tag present in the skim continuation")
			skimRan.Store(true)
			return nil
		},
	))

	// A launcher whose body opens a per-item scope and submits a skim result under
	// it, plus a follow-up on the item's flow.
	producer := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
		if err != nil {
			return err
		}
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return collector.Submit(ctx, 1)
		}, itemKey.Value("item-x"),
			itemTag.FollowUpFn(func(context.Context) error {
				followUpSawSkim.Store(skimRan.Load())
				return nil
			}))
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := producer.In(wave).Submit(ctx, 0); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, driverKey.Value("driver"))
	chk.NoError(err)

	chk.Equal([2]any{"driver", true}, skimSawDriver.Load(), "skim continuation inherits the driver value")
	chk.Equal([2]any{"item-x", true}, skimSawItem.Load(), "CP-F7: skim continuation carries the producing item's value")
	chk.Eventually(followUpSawSkim.Load, 5*time.Second, 5*time.Millisecond,
		"item follow-up must fire only after its result was skimmed")
}

// TestFlowSkimRiderFreeItemIsolation: within one skim drive, a RIDER-FREE item
// processed after a rider-carrying one must not see the previous item's flow
// values — it runs under the drive's own riders (docs/decisions/
// driver-contexts.md, "Skim handlers get a per-item child context"). Pins the
// misdelivery in the pre-child-meta in-place rider override, which fired only
// for rider-carrying items and was never restored: the rider-free item then
// read the previous item's (by that point recycled) chain instead of the
// drive's. The rider-carrying→rider-free order is structural here: the
// rider-carrying item's own handler submits the rider-free item, so it is
// necessarily handled later in the same drive.
func TestFlowSkimRiderFreeItemIsolation(t *testing.T) {
	chk := require.New(t)
	driverKey := streampool.NewFlowKey[string]()
	itemKey := streampool.NewFlowKey[string]()

	wave := streampool.NewWave()

	type seen struct {
		value     int
		itemVal   string
		itemOK    bool
		driverVal string
		driverOK  bool
	}
	var mu sync.Mutex
	var handled []seen

	var collector streampool.Skimmer[int]
	collector = streampool.NewSkimmer(streampool.HandlerFunc[int](
		func(ctx context.Context, v int, err error) error {
			if err != nil {
				return err
			}
			iv, iok := itemKey.From(ctx)
			dv, dok := driverKey.From(ctx)
			mu.Lock()
			handled = append(handled, seen{v, iv, iok, dv, dok})
			mu.Unlock()
			if v == 1 {
				// The rider-free item, submitted from a bare ctx (NO riders
				// captured) while the rider-carrying item is being handled — so
				// it is skimmed after this one, in this same drive.
				//nolint:contextcheck // deliberately rider-free: a bare-ctx submit is the regression case
				return collector.In(wave).Submit(context.Background(), 2)
			}
			return nil
		},
	))

	// The producer body posts the rider-carrying item from a per-item scope.
	producer := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
		if err != nil {
			return err
		}
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return collector.Submit(ctx, 1)
		}, itemKey.Value("carry-x"))
	})

	// Dispatch outside any flow scope: the producer's body ctx is rider-free.
	chk.NoError(producer.In(wave).Submit(context.Background(), 0))

	// Drive under a scope of its own: the rider-free item must inherit THESE
	// riders — not the previous item's.
	err := streampool.WithFlow(context.Background(), wave.CloseAndSkimAll,
		driverKey.Value("driver"))
	chk.NoError(err)

	chk.Equal([]seen{
		{1, "carry-x", true, "", false},
		{2, "", false, "driver", true},
	}, handled)
}

// ─── CP-R6a: definitional tag follow-up (shared-chain) ───────────────────────

// TestFlowDefinitionalFollowUp: a follow-up bound to a tag's identity at
// declaration fires ONCE per flow carrying the tag — idempotent across repeated
// infusion in a shared chain, and once across a funnel fan-in. (Coalescing of
// INDEPENDENT flows converging is CP-R6b.)
func TestFlowDefinitionalFollowUp(t *testing.T) {
	chk := require.New(t)
	var fires atomic.Int32
	audit := streampool.NewFlowTag(
		streampool.FlowFollowUpFn(func(context.Context) error { fires.Add(1); return nil }))

	// Single infusion → one fire; InFlow reports presence.
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		chk.True(audit.InFlow(ctx), "infused definitional tag reads present")
		return nil
	}, audit.Infuse()))
	chk.EqualValues(1, fires.Load())

	// Repeated (nested) infusion in one flow → still one fire.
	fires.Store(0)
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return streampool.WithFlow(ctx, func(context.Context) error { return nil }, audit.Infuse())
		}, audit.Infuse())
	}, audit.Infuse()))
	chk.EqualValues(1, fires.Load(), "fires once per flow despite N infusions")

	// Across a funnel fan-in → one fire after the aggregate completes.
	fires.Store(0)
	wave := streampool.NewWave()
	agg := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
		return streampool.NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { return nil })
	})
	var firedBeforeFlush atomic.Bool
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		for i := 0; i < 3; i++ {
			if err := agg.Submit(ctx, i); err != nil {
				return err
			}
		}
		firedBeforeFlush.Store(fires.Load() > 0)
		return wave.CloseAndSkimAll(ctx)
	}, audit.Infuse()))
	chk.Eventually(func() bool { return fires.Load() == 1 }, 5*time.Second, 5*time.Millisecond,
		"definitional follow-up fires once across the funnel")
	chk.False(firedBeforeFlush.Load(), "must not fire before the aggregate completes")
}

// TestFlowDefinitionalCoalesce: two INDEPENDENT flows (no common ancestor) that
// converge into the SAME funnel instance coalesce so the definitional follow-up
// fires ONCE for that aggregate, not once per input flow (CP-R6b). A funnel
// instance IS one aggregated flow — a flow is defined by its data, not its
// operations — so whether two independent submits land in one instance is a runtime
// accident (submit runs inline or async). This test therefore requires only that
// coalescing is OBSERVED across iterations and the count is always in the valid
// [1,2] range (never zero — a hang — never over-firing); the deterministic
// single-fire proof is the white-box TestFlowCoalesceMechanism.
func TestFlowDefinitionalCoalesce(t *testing.T) {
	chk := require.New(t)
	coalesced := 0
	for iter := 0; iter < 40; iter++ {
		var fires atomic.Int32
		audit := streampool.NewFlowTag(
			streampool.FlowFollowUpFn(func(context.Context) error { fires.Add(1); return nil }))
		wave := streampool.NewWave()
		agg := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
			return streampool.NewAccumulator(
				func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
				func(context.Context) error { return nil })
		})
		// Two independent flow roots (each rooted at Background — no common ancestor),
		// each infusing the tag, both feeding one funnel.
		for i := 0; i < 2; i++ {
			v := i
			chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
				return agg.Submit(ctx, v)
			}, audit.Infuse()))
		}
		chk.NoError(wave.CloseAndSkimAll(context.Background()))
		chk.Eventually(func() bool { return fires.Load() >= 1 }, 5*time.Second, 5*time.Millisecond,
			"the definitional follow-up fires (no hang)")
		chk.LessOrEqual(fires.Load(), int32(2), "never fires more than once per input flow")
		if fires.Load() == 1 {
			coalesced++
		}
	}
	chk.Positive(coalesced, "co-accumulated independent flows coalesce to a single fire")
}

// TestFlowDefinitionalTagPanics: NewFlowTag rejects a non-follow-up option.
func TestFlowDefinitionalTagPanics(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[int]()
	chk.PanicsWithValue(
		"streampool: NewFlowTag accepts only a FlowFollowUp/FlowFollowUpFn option",
		func() { _ = streampool.NewFlowTag(key.Value(1)) })
}

// ─── driver-contexts step 3: fire = the last carrier's continuation ──────────

// TestFollowUpFiresAsCarrierContinuation: the fire runs under the LAST
// CARRIER's context — it sees riders the last carrier acquired AFTER the
// follow-up's registration (a nested WithFlow's value), with the fired
// binding itself peeled (no re-fire, no self-presence)
// (docs/decisions/driver-contexts.md, "Fire: the last carrier's continuation").
func TestFollowUpFiresAsCarrierContinuation(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	wave := streampool.NewWave()
	var fireSawKey atomic.Value
	var fireSawTag, fired atomic.Bool

	release := make(chan struct{})
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		return nil
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		// The task is dispatched from a nested value scope INSIDE the tag's
		// flow: its rider chain is [key=post-reg] → [tag] → …, and it is the
		// flow's last carrier (the enclosing scope exits while it still runs).
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return task.In(wave).Start(ctx)
		}, key.Value("post-reg"))
	}, tag.FollowUpFn(func(ctx context.Context) error {
		v, ok := key.From(ctx)
		fireSawKey.Store([2]any{v, ok})
		fireSawTag.Store(tag.InFlow(ctx))
		fired.Store(true)
		return nil
	}))
	chk.NoError(err)

	// The scope has exited (its carrier ref released); the task is the last
	// carrier. Its completion fires the follow-up as its continuation.
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))

	chk.Eventually(fired.Load, 5*time.Second, 2*time.Millisecond, "follow-up fires after the last carrier")
	chk.Equal([2]any{"post-reg", true}, fireSawKey.Load(),
		"fire sees the rider the last carrier acquired after registration")
	chk.False(fireSawTag.Load(), "the fired binding is peeled from the fire's chain")
}

// TestFollowUpFireShieldedFromCancellation pins the resolution of the design
// record's open point (docs/decisions/driver-contexts.md, "Fire"): a follow-up
// fire is end-of-flow work — it must run, with a usable (non-canceled) ctx,
// even when the last carrier's submit ctx was canceled long before the fire.
// The carrier's context contributes its RIDERS to the fire (the continuation
// semantics), never its cancellation.
func TestFollowUpFireShieldedFromCancellation(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	wave := streampool.NewWave()
	var fireCtxErr atomic.Value
	var fireSawKey atomic.Value
	var fired atomic.Bool

	release := make(chan struct{})
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		return nil
	})

	cancelable, cancel := context.WithCancel(context.Background())
	err := streampool.WithFlow(cancelable, func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return task.In(wave).Start(ctx)
		}, key.Value("v"))
	}, tag.FollowUpFn(func(ctx context.Context) error {
		fireCtxErr.Store([1]any{ctx.Err()})
		v, ok := key.From(ctx)
		fireSawKey.Store([2]any{v, ok})
		fired.Store(true)
		return nil
	}))
	chk.NoError(err)

	// The submitter's ctx dies while the task — the flow's last carrier — is
	// still running.
	cancel()
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))

	chk.Eventually(fired.Load, 5*time.Second, 2*time.Millisecond, "fire runs despite the canceled submit ctx")
	chk.Equal([1]any{error(nil)}, fireCtxErr.Load(), "fire ctx is not canceled by the carrier's chain")
	chk.Equal([2]any{"v", true}, fireSawKey.Load(), "carrier riders still delivered")
}
