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

// TestFlowNodeConservation exercises the CP-R2b node refcount along its
// reclaim-critical paths — inline nesting, async work that outlives the scope,
// and the funnel tag-union adoption — and asserts every rider node drawn from
// flowRiderNodePool is returned once the flow drains. It runs white-box so it can
// install flowNodeAllocHook, the +1/-1 seam around the pool borrow/reclaim; a leak
// leaves the balance positive, a double-free trips the underflow panic in
// nodeUnref before the balance could even reach zero.
func TestFlowNodeConservation(t *testing.T) {
	chk := require.New(t)

	var balance atomic.Int64
	hook := func(delta int) { balance.Add(int64(delta)) }
	flowNodeAllocHook.Store(&hook)
	defer flowNodeAllocHook.Store(nil)

	// Nodes borrowed so far must all come back; wait past any async fire tail.
	settled := func(where string) {
		chk.Eventuallyf(func() bool { return balance.Load() == 0 }, 5*time.Second, 2*time.Millisecond,
			"%s: %d rider node(s) leaked (balance not zero)", where, balance.Load())
	}

	key := NewFlowKey[int]()
	tag := NewFlowTag()
	inner := NewFlowTag()

	// (A) Inline nesting with values, tags, follow-ups, suppress and a fresh root —
	// every fire runs at scope exit, so the whole chain reclaims synchronously.
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return WithFlow(ctx, func(ctx context.Context) error {
			return WithFlow(ctx, func(context.Context) error { return nil },
				NewFlow(), key.Value(9), inner.FollowUpFn(func(context.Context) error { return nil }))
		}, key.Suppress(), tag.FollowUpFn(func(context.Context) error { return nil }))
	}, key.Value(1), tag.FollowUpFn(func(context.Context) error { return nil })))
	settled("inline nesting")

	// (B) Async work outliving the scope: the follow-up fires from the wave drain,
	// so the instance's enclosing ref (and the chain behind it) must survive the
	// gap between count→0 and the async fire, then reclaim.
	release := make(chan struct{})
	task := NewTaskLauncher(func(context.Context) error { <-release; return nil })
	var wave Wave
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		for i := 0; i < 4; i++ {
			if err := task.In(&wave).Start(ctx); err != nil {
				return err
			}
		}
		return nil
	}, key.Value(2), tag.FollowUpFn(func(context.Context) error { return nil })))
	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	settled("async drain")

	// (C) Funnel tag union: two independent scopes' tags fold into one funnel
	// instance, materialize onto the flush meta, and release with it.
	tagA, tagB := NewFlowTag(), NewFlowTag()
	var fwave Wave
	funnel := NewFnFunnel(&fwave, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { return nil },
		)
	})
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return funnel.Submit(ctx, 1)
	}, tagA.FollowUpFn(func(context.Context) error { return nil })))
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return funnel.Submit(ctx, 2)
	}, tagB.FollowUpFn(func(context.Context) error { return nil })))
	chk.NoError(fwave.CloseAndSkimAll(context.Background()))
	settled("funnel union")

	// (D) Bare presence (Infuse) + anonymous follow-up crossing a funnel: the
	// presence node (no instance) and the anonymous follow-up node must both fold
	// into the union, materialize at flush, and reclaim.
	pres := NewFlowTag()
	var iwave Wave
	ifunnel := NewFnFunnel(&iwave, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { return nil },
		)
	})
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		return ifunnel.Submit(ctx, 1)
	}, pres.Infuse(), FlowFollowUpFn(func(context.Context) error { return nil })))
	chk.NoError(iwave.CloseAndSkimAll(context.Background()))
	settled("infuse + anonymous follow-up funnel")
}
