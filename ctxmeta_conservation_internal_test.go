// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCtxMetaConservation proves every ctxMeta drawn from bodyMetaPool returns
// exactly once across drained workloads (mirrors TestFlowNodeConservation):
// a leaked meta — one whose refcount never drains, e.g. a creation path that
// takes a parent pin without a matching release — leaves the balance positive;
// a double release trips unrefMeta's underflow panic. The workloads cover every
// creation path: top-level dispatch mints (launcher, funnel and skimmer
// submits), skim-drive derivations (bare-ctx owned chain and reuse), dispatch
// borrows (task, accumulate), the stashed-continuation borrows (deadline and
// sweep flushes, follow-up fires, both async and inline), WithFlow scope metas,
// and the flush fan-in clone.
func TestCtxMetaConservation(t *testing.T) {
	chk := require.New(t)

	var balance, draws atomic.Int64
	hook := func(delta int) {
		balance.Add(int64(delta))
		if delta > 0 {
			draws.Add(1)
		}
	}
	ctxMetaAllocHook.Store(&hook)
	defer ctxMetaAllocHook.Store(nil)
	// Not vacuous: the workloads below must actually draw metas through the hook.
	defer func() { chk.Positive(draws.Load(), "no metas drawn — hook not exercised") }()

	// Metas borrowed so far must all come back; wait past any async fire/free tail.
	settled := func(where string) {
		chk.Eventuallyf(func() bool { return balance.Load() == 0 }, 5*time.Second, 2*time.Millisecond,
			"%s: %d ctxMeta(s) leaked (balance not zero)", where, balance.Load())
	}

	// (A) Task dispatch + skim drive: launcher top-level mint released at
	// dispatch end while the task body's borrow pins it; bare-ctx CloseAndSkimAll
	// mints the owned skim-over-top-level chain, released via the cascade.
	release := make(chan struct{})
	task := NewTaskLauncher(func(context.Context) error { <-release; return nil })
	var wave Wave
	for i := 0; i < 4; i++ {
		chk.NoError(task.In(&wave).Start(context.Background()))
	}
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	settled("task dispatch + skim drive")

	// (B) Nested subwave inside a body: the body meta becomes a parent (sync
	// derivation ref) of the subwave's minted top-level meta.
	outer := NewTaskLauncher(func(ctx context.Context) error {
		var sub Wave
		inner := NewTaskLauncher(func(context.Context) error { return nil })
		if err := inner.In(&sub).Start(ctx); err != nil {
			return err
		}
		return sub.CloseAndSkimAll(ctx)
	})
	var owave Wave
	chk.NoError(outer.In(&owave).Start(context.Background()))
	chk.NoError(owave.CloseAndSkimAll(context.Background()))
	settled("nested subwave")

	// (C) Skimmer submit (top-level mint, synchronous-only use) + skim handler.
	var swave Wave
	skimmer := NewFnSkimmer(func(context.Context, int, error) error { return nil })
	chk.NoError(skimmer.In(&swave).Submit(context.Background(), 1))
	chk.NoError(swave.CloseAndSkimAll(context.Background()))
	settled("skimmer submit")

	// (D) Funnel: submit mint pinned by the accumulate body's borrow; the
	// deadline/sweep flush exercises the Execute-stash pin + flush borrow +
	// fan-in clone. WithFlow adds scope metas and a tag follow-up fire (async,
	// via flowFireWork's Execute-stash pin).
	tag := NewFlowTag()
	var fwave Wave
	funnel := NewFnFunnel(&fwave, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { return nil },
		)
	})
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return funnel.Submit(ctx, 1)
	}, tag.FollowUpFn(func(context.Context) error { return nil })))
	chk.NoError(fwave.CloseAndSkimAll(context.Background()))
	settled("funnel + flow fire")

	// (E) Inline scope-exit fire (empty scope fires at return on the caller's
	// frame) + nested scopes.
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return WithFlow(ctx, func(context.Context) error { return nil },
			NewFlowTag().FollowUpFn(func(context.Context) error { return nil }))
	}, FlowFollowUpFn(func(context.Context) error { return nil })))
	settled("inline scope fires")

	// (F) Pinned flow: while the pin stands, the metas it holds are a
	// DELIBERATE positive — a leaked pin is visible here by design — and
	// UnpinFlow restores zero. The pin arc covers mint (PinFlow), retention
	// past the source scope, dispatch from the pin, and the unpin release.
	pinKey := NewFlowKey[int]()
	var pinned context.Context
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = PinFlow(ctx)
		return nil
	}, pinKey.Value(1), NewFlowTag().FollowUpFn(func(context.Context) error { return nil })))
	chk.Positive(balance.Load(), "a standing pin holds metas out of the pool — a leaked pin is visible")
	var pwave Wave
	ptask := NewTaskLauncher(func(context.Context) error { return nil })
	chk.NoError(ptask.In(&pwave).Start(pinned))
	chk.NoError(pwave.CloseAndSkimAll(context.Background()))
	chk.NoError(UnpinFlow(pinned))
	settled("pinned flow released")
}
