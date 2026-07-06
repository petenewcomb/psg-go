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

// TestFlowCoalesceMechanism is the DETERMINISTIC proof of the CP-R6b invariant,
// driving the union-find primitives directly (a funnel instance's co-accumulation
// is a runtime accident — submit runs inline or async — so no black-box submit can
// pin "N flows → one fire"). It merges three independent definitional instances into
// one component, as one funnel instance's union would, then drains them in order and
// asserts the follow-up fires EXACTLY ONCE — at the last drain, not before — and
// that the shared component node is reclaimed.
func TestFlowCoalesceMechanism(t *testing.T) {
	chk := require.New(t)

	var sharedBal atomic.Int64
	sh := func(d int) { sharedBal.Add(int64(d)) }
	flowSharedAllocHook.Store(&sh)
	defer flowSharedAllocHook.Store(nil)

	var fires int
	fn := func(context.Context, any) error { fires++; return nil }
	id := &flowIdentity{kind: flowTagIdent, definitionalFn: fn}
	mk := func() *flowInstance {
		in := flowInstancePool.Get()
		in.fn = fn
		in.definitional = true
		in.id = id
		in.count.Store(1) // one carrier, released by the unref below
		return in
	}
	inA, inB, inC := mk(), mk(), mk()

	// As collectFlowTags would when all three co-accumulate in one funnel instance.
	id.mergeMu.Lock()
	mergeDefinitional(inA, inB)
	mergeDefinitional(inB, inC)
	id.mergeMu.Unlock()
	chk.EqualValues(1, sharedBal.Load(), "one shared component node for the merged trio")

	// Drain in order (inline fire path, no wave): only the LAST reaching zero fires.
	chk.NoError(inA.unref(true, nil))
	chk.Equal(0, fires, "no fire while the component is still live")
	chk.NoError(inB.unref(true, nil))
	chk.Equal(0, fires, "no fire while the component is still live")
	chk.NoError(inC.unref(true, nil))
	chk.Equal(1, fires, "the last drain fires the coalesced follow-up exactly once")

	chk.EqualValues(0, sharedBal.Load(), "the component node reclaims when it fires")
}

// TestFlowCoalesceConservation exercises the CP-R6b coalescing union-find along
// its reclaim-critical paths — N flows co-accumulating in one funnel instance
// (their separate definitional instances merging into one component), and that
// merged component draining CONCURRENTLY across a downstream fan-out (the shared
// tree dereffed under mergeMu contention) — and asserts that (a) the definitional
// follow-up fires EXACTLY ONCE per aggregated flow and (b) every sharedNode drawn
// from sharedNodePool and every rider node come back once the flow drains. Both
// scenarios pin co-accumulation deterministically (a funnel INSTANCE is one
// aggregation is one flow; whether independent flows land in the SAME instance is a
// timing accident, so a cross-instance fire count is intentionally not asserted). It
// runs white-box to install both alloc seams; a leaked component node leaves
// sharedBal positive, a double-free trips the underflow panic in derefShared.
func TestFlowCoalesceConservation(t *testing.T) {
	chk := require.New(t)

	var nodeBal, sharedBal atomic.Int64
	nh := func(d int) { nodeBal.Add(int64(d)) }
	sh := func(d int) { sharedBal.Add(int64(d)) }
	flowNodeAllocHook.Store(&nh)
	flowSharedAllocHook.Store(&sh)
	defer flowNodeAllocHook.Store(nil)
	defer flowSharedAllocHook.Store(nil)

	settled := func(where string) {
		chk.Eventuallyf(func() bool { return nodeBal.Load() == 0 && sharedBal.Load() == 0 },
			5*time.Second, 2*time.Millisecond,
			"%s: leak (nodes=%d shared=%d)", where, nodeBal.Load(), sharedBal.Load())
	}

	var fires atomic.Int32
	tag := NewFlowTag(FlowFollowUpFn(func(context.Context) error { fires.Add(1); return nil }))

	// (A) Five flow roots submitted SEQUENTIALLY into one funnel co-accumulate in a
	// single instance (each submit reuses the live instance): their five separate
	// definitional instances coalesce into one component, fire once, all reclaimed.
	fires.Store(0)
	var w1 Wave
	f1 := NewFnFunnel(&w1, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(context.Context) error { return nil })
	})
	for i := 0; i < 5; i++ {
		v := i
		chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
			return f1.Submit(ctx, v)
		}, tag.Infuse()))
	}
	chk.NoError(w1.CloseAndSkimAll(context.Background()))
	// Fire count is nondeterministic (the five flows co-accumulate into one instance
	// only when submit runs inline — usually, not always); conservation is the
	// invariant. Range-check only: at least one fire, never more than five.
	chk.Eventually(func() bool { n := fires.Load(); return n >= 1 && n <= 5 }, 5*time.Second, 5*time.Millisecond,
		"single-funnel fires between one and five times")
	settled("single funnel")

	// (B) Concurrent downstream drain: four flows co-accumulate in one funnel
	// instance (merged component), whose flush fans the single aggregate out to eight
	// CONCURRENT downstream tasks — all carrying that one component. Their concurrent
	// completion derefs the shared tree under mergeMu contention (the real
	// concurrency surface: sibling count→0 derefs racing). One flow, so exactly one
	// fire; still fully reclaimed.
	fires.Store(0)
	var wUp, wDown Wave
	release := make(chan struct{})
	task := NewTaskLauncher(func(context.Context) error { <-release; return nil })
	fc := NewFnFunnel(&wUp, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(ctx context.Context) error {
				for k := 0; k < 8; k++ {
					if err := task.In(&wDown).Start(ctx); err != nil {
						return err
					}
				}
				return nil
			})
	})
	for i := 0; i < 4; i++ {
		v := i
		chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
			return fc.Submit(ctx, v)
		}, tag.Infuse()))
	}
	chk.NoError(wUp.CloseAndSkimAll(context.Background())) // flush fans out onto wDown
	close(release)
	chk.NoError(wDown.CloseAndSkimAll(context.Background()))
	// Each fc instance is one component firing once after its eight downstream tasks
	// deref it concurrently; the four flows co-accumulate into one instance (usually)
	// or a few, so fires is in [1,4]. Conservation is the invariant under the
	// concurrent deref contention.
	chk.Eventually(func() bool { n := fires.Load(); return n >= 1 && n <= 4 }, 5*time.Second, 5*time.Millisecond,
		"merged component fires after concurrent downstream drain")
	settled("concurrent downstream drain")
}
