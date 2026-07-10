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

// TestOriginFlowTaskBody: a task body's origin is its dispatching body — the
// flow it branched off from. Discriminated by a body-side scope: the scope's
// value is visible on the body's own ctx but absent at every origin hop, and
// composition walks one branch point per application.
func TestOriginFlowTaskBody(t *testing.T) {
	chk := require.New(t)
	dispatchKey := streampool.NewFlowKey[string]()
	bodyKey := streampool.NewFlowKey[string]()

	var originSaw, hopTwoSaw atomic.Value
	var wave streampool.Wave
	inner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			// Hop 1 from inside the body scope: the scope's origin is the
			// body meta — bodyKey absent there, dispatchKey present.
			origin, ok := streampool.OriginFlow(ctx)
			if !ok {
				originSaw.Store([3]any{"", false, false})
				return nil
			}
			dv, dok := dispatchKey.From(origin)
			_, bok := bodyKey.From(origin)
			originSaw.Store([3]any{dv, dok, bok})
			// Hop 2: the body's origin, the dispatching body's position.
			origin2, ok2 := streampool.OriginFlow(origin)
			if ok2 {
				dv2, dok2 := dispatchKey.From(origin2)
				hopTwoSaw.Store([2]any{dv2, dok2})
			}
			return nil
		}, bodyKey.Value("mine"))
	})

	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := inner.In(&wave).Start(ctx); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, dispatchKey.Value("dispatcher"))
	chk.NoError(err)

	chk.Equal([3]any{"dispatcher", true, false}, originSaw.Load(),
		"the scope's origin is the body: dispatch chain visible, the scope's own value absent")
	chk.Equal([2]any{"dispatcher", true}, hopTwoSaw.Load(),
		"composition hops to the dispatching position")
}

// TestOriginFlowSkimHandler: a skim handler's origin is the DRIVE flow — not
// the item's producer, whose chain the handler already runs under. The
// discriminator: the item's value is on the handler ctx but NOT on the
// origin; the drive's value is on both.
func TestOriginFlowSkimHandler(t *testing.T) {
	chk := require.New(t)
	driveKey := streampool.NewFlowKey[string]()
	itemKey := streampool.NewFlowKey[string]()

	var handlerSaw, originSaw atomic.Value
	var wave streampool.Wave
	collector := streampool.NewSkimmer(streampool.HandlerFunc[int](
		func(ctx context.Context, _ int, err error) error {
			if err != nil {
				return err
			}
			iv, iok := itemKey.From(ctx)
			handlerSaw.Store([2]any{iv, iok})
			origin, ok := streampool.OriginFlow(ctx)
			if !ok {
				originSaw.Store([4]any{"", false, "", false})
				return nil
			}
			dv, dok := driveKey.From(origin)
			_, oik := itemKey.From(origin)
			originSaw.Store([4]any{dv, dok, "", oik})
			return nil
		},
	))

	producer := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
		if err != nil {
			return err
		}
		return streampool.WithFlow(ctx, func(ctx context.Context) error {
			return collector.Submit(ctx, 1)
		}, itemKey.Value("item-v"))
	})

	chk.NoError(producer.In(&wave).Submit(context.Background(), 0))
	err := streampool.WithFlow(context.Background(), wave.CloseAndSkimAll,
		driveKey.Value("drive"))
	chk.NoError(err)

	chk.Equal([2]any{"item-v", true}, handlerSaw.Load(), "the handler runs under the item's chain")
	chk.Equal([4]any{"drive", true, "", false}, originSaw.Load(),
		"the origin is the drive: its value present, the item's absent")
}

// TestOriginFlowFlush: a flush body's origin is THE LAST ACCUMULATE, via the
// instance's rolling driver pin. The severing fan-in makes the assertion
// sharp: the per-item value is ABSENT on the flush ctx and PRESENT on its
// origin. Covers the executor (sweep) flush and the inline past-deadline
// flush with a tag (the fan-in clone carries the stamp).
func TestOriginFlowFlush(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline func() time.Time
		opts     func(tag streampool.FlowTag) []streampool.FlowOption
	}{
		{"sweep flush", func() time.Time { return time.Time{} },
			func(streampool.FlowTag) []streampool.FlowOption { return nil }},
		{"inline past-deadline flush with tag", func() time.Time { return time.Now().Add(-time.Millisecond) },
			func(tag streampool.FlowTag) []streampool.FlowOption {
				return []streampool.FlowOption{tag.Infuse()}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chk := require.New(t)
			perItem := streampool.NewFlowKey[string]()
			tag := streampool.NewFlowTag()

			var flushCtxSaw, flushOriginSaw atomic.Value
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
						_, cok := perItem.From(ctx)
						flushCtxSaw.Store(cok)
						origin, ok := streampool.OriginFlow(ctx)
						if !ok {
							flushOriginSaw.Store([2]any{"", false})
							return nil
						}
						v, vok := perItem.From(origin)
						flushOriginSaw.Store([2]any{v, vok})
						return nil
					},
				)
			})

			producer := streampool.NewFnLauncher(func(ctx context.Context, _ int, err error) error {
				if err != nil {
					return err
				}
				opts := append(tc.opts(tag), perItem.Value("acc-v"))
				return streampool.WithFlow(ctx, func(ctx context.Context) error {
					return aggregator.Submit(ctx, 1)
				}, opts...)
			})

			chk.NoError(producer.In(&wave).Submit(context.Background(), 0))
			chk.NoError(wave.CloseAndSkimAll(context.Background()))

			chk.Equal(false, flushCtxSaw.Load(), "the fan-in severs the per-item value from the flush ctx")
			chk.Equal([2]any{"acc-v", true}, flushOriginSaw.Load(),
				"the flush's origin is the last accumulate: its per-item value readable there")
		})
	}
}

// TestOriginFlowFire: a follow-up fire has no origin — it IS the last
// carrier's continuation. Covers the async (wave-drain) fire and the inline
// scope-exit fire.
func TestOriginFlowFire(t *testing.T) {
	chk := require.New(t)
	tag := streampool.NewFlowTag()

	// Async fire: the flow's last carrier is a task completing in a drain.
	var asyncFireOK atomic.Bool
	asyncFireOK.Store(true)
	var wave streampool.Wave
	release := make(chan struct{})
	task := streampool.NewTaskLauncher(func(context.Context) error { <-release; return nil })
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		return task.In(&wave).Start(ctx)
	}, tag.FollowUpFn(func(ctx context.Context) error {
		_, ok := streampool.OriginFlow(ctx)
		asyncFireOK.Store(ok)
		return nil
	}))
	chk.NoError(err)
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Eventually(func() bool { return !asyncFireOK.Load() }, 5*time.Second, 2*time.Millisecond,
		"an async fire reports no origin")

	// Inline scope-exit fire.
	var inlineFireOK atomic.Bool
	inlineFireOK.Store(true)
	chk.NoError(streampool.WithFlow(context.Background(), func(context.Context) error { return nil },
		streampool.NewFlowTag().FollowUpFn(func(ctx context.Context) error {
			_, ok := streampool.OriginFlow(ctx)
			inlineFireOK.Store(ok)
			return nil
		})))
	chk.False(inlineFireOK.Load(), "an inline scope-exit fire reports no origin")
}

// TestOriginFlowAbsence: bare and top-level-rooted contexts have no origin,
// and a held ctx (GC wrapper, no parent) reports honest absence; a pinned ctx
// resolves its source.
func TestOriginFlowAbsence(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()

	_, ok := streampool.OriginFlow(context.Background())
	chk.False(ok, "a bare ctx has no origin")

	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		_, ok := streampool.OriginFlow(ctx)
		chk.False(ok, "a top-level-rooted scope has no origin above it")
		return nil
	}, key.Value("v")))

	// A pinned ctx's origin is the pinned source position.
	var pinned context.Context
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = streampool.PinFlow(ctx)
		return nil
	}, key.Value("src")))
	origin, ok := streampool.OriginFlow(pinned)
	chk.True(ok, "a pin's origin is its source position")
	v, vok := key.From(origin)
	chk.True(vok)
	chk.Equal("src", v)
	chk.NoError(streampool.UnpinFlow(pinned))

	// A held ctx severs ancestry entirely: honest absence.
	held, cancel := streampool.HoldFlow(context.Background())
	defer cancel(nil)
	_, ok = streampool.OriginFlow(held)
	chk.False(ok, "a hold carries the flow, not the ancestry")
}

// TestOriginFlowRetention: the blessed way to keep an origin — pin it while
// still inside the extent.
func TestOriginFlowRetention(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()

	var pinnedOrigin context.Context
	var wave streampool.Wave
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		origin, ok := streampool.OriginFlow(ctx)
		if ok {
			pinnedOrigin = streampool.PinFlow(origin)
		}
		return nil
	})
	err := streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		if err := task.In(&wave).Start(ctx); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}, key.Value("kept"))
	chk.NoError(err)

	chk.NotNil(pinnedOrigin, "origin resolved inside the body")
	v, ok := key.From(pinnedOrigin)
	chk.True(ok, "the pinned origin outlives the extent")
	chk.Equal("kept", v)
	chk.NoError(streampool.UnpinFlow(pinnedOrigin))
}
