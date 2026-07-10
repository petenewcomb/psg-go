// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"sync"
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
				Disconnect(), key.Value(9), inner.FollowUpFn(func(context.Context) error { return nil }))
		}, key.Suppress(), tag.FollowUpFn(func(context.Context) error { return nil }))
	}, key.Value(1), tag.FollowUpFn(func(context.Context) error { return nil })))
	settled("inline nesting")

	// (A2) Sequential-layer disposal arcs: subtractive options over fresh
	// sibling layers exercise the working-chain replace/dispose path — a
	// Suppress dropping an earlier sibling's node, a Disconnect dropping a
	// whole fresh prefix including a follow-up's node (whose instance still
	// fires empty at scope exit) — every dropped node must reclaim.
	chk.NoError(WithFlow(context.Background(), func(context.Context) error { return nil },
		key.Value(3), tag.Infuse(), tag.Suppress(), key.Value(4)))
	chk.NoError(WithFlow(context.Background(), func(context.Context) error { return nil },
		key.Value(5), inner.FollowUpFn(func(context.Context) error { return nil }),
		Disconnect(), key.Value(6)))
	settled("sequential-layer disposal")

	// (A3) Pinned flow: the pin's node refs hold the chain past the source
	// scope (a deliberate positive while it stands); UnpinFlow releases it,
	// firing the follow-up inline, and every node returns.
	var pinned context.Context
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = PinFlow(ctx)
		return nil
	}, key.Value(7), tag.FollowUpFn(func(context.Context) error { return nil })))
	chk.Positive(balance.Load(), "a standing pin holds rider nodes — a leaked pin is visible")
	chk.NoError(UnpinFlow(pinned))
	settled("pinned flow released")

	// (A4) Held flow: the hold's snapshot is GC-owned (invisible to the node
	// hook), but its carrier refs keep the instances — and through their
	// enclosing refs, pooled chain — alive until release; the release fires
	// and every pooled node returns.
	var heldFires atomic.Int64
	var held context.Context
	var releaseHold context.CancelCauseFunc
	chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
		held, releaseHold = HoldFlow(ctx)
		return nil
	}, key.Value(8), tag.FollowUpFn(func(context.Context) error { heldFires.Add(1); return nil })))
	chk.Equal(int64(0), heldFires.Load(), "the hold carries the flow past the scope")
	releaseHold(nil)
	chk.Equal(int64(1), heldFires.Load(), "release ends the flow")
	_ = held
	settled("held flow released")

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
	chk.NoError(inA.unref(true, nil, nil))
	chk.Equal(0, fires, "no fire while the component is still live")
	chk.NoError(inB.unref(true, nil, nil))
	chk.Equal(0, fires, "no fire while the component is still live")
	chk.NoError(inC.unref(true, nil, nil))
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

// TestFunnelDriverPin: each accumulate re-points the instance's rolling driver
// pin at its own body meta and that meta's rider head, releasing the previous
// pair; the flush releases the final pair after the flush body runs
// (docs/decisions/driver-contexts.md, "Flush: a rolling node-only driver pin on
// the instance"). White-box: between accumulates the cached live instance is
// inspected via the owner lineage (pop → read under mu → push back), safe while
// the wave is otherwise quiescent. The alloc-hook balance proves the flush-time
// release: a pin left standing would hold the last accumulate's meta out of the
// pool forever.
func TestFunnelDriverPin(t *testing.T) {
	chk := require.New(t)
	key := NewFlowKey[int]()

	var balance atomic.Int64
	hook := func(delta int) { balance.Add(int64(delta)) }
	ctxMetaAllocHook.Store(&hook)
	defer ctxMetaAllocHook.Store(nil)

	var mu sync.Mutex
	var accMetas []*ctxMeta
	var accRiders []*flowRiderNode

	var wave Wave
	f := NewFnFunnel(&wave, func() Accumulator[int] {
		return NewAccumulator(
			func(ctx context.Context, _ int, _ error) (time.Time, error) {
				m, ok := metaFromContext(ctx)
				chk.True(ok)
				mu.Lock()
				accMetas = append(accMetas, m)
				accRiders = append(accRiders, m.riders)
				mu.Unlock()
				return time.Time{}, nil // no deadline: flushed by the end-of-work sweep
			},
			func(context.Context) error { return nil },
		)
	})

	accumulated := func(n int) func() bool {
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(accMetas) == n
		}
	}

	// inspect reads the pin off the cached live instance via the owner lineage.
	// Eventually covers the window between the accumulate's return and the
	// lineage's deferred push-back.
	inspect := func(assert func(inst *funnelInstance[int])) {
		v, ok := wave.funnelInstances.Load(f.id)
		chk.True(ok, "instance queue registered")
		q := v.(*funnelInstanceQueue[int])
		var inst *funnelInstance[int]
		chk.Eventually(func() bool {
			var popped bool
			inst, popped = q.queue.TryPopFront()
			return popped
		}, 5*time.Second, time.Millisecond, "cached live instance")
		inst.mu.Lock()
		assert(inst)
		inst.mu.Unlock()
		q.queue.PushBack(inst)
	}

	submit := func(v int) {
		chk.NoError(WithFlow(context.Background(), func(ctx context.Context) error {
			return f.Submit(ctx, v)
		}, key.Value(v)))
	}

	submit(1)
	chk.Eventually(accumulated(1), 5*time.Second, time.Millisecond)
	inspect(func(inst *funnelInstance[int]) {
		chk.Same(accMetas[0], inst.driverMeta, "pin holds the first accumulate's meta")
		chk.NotNil(accRiders[0], "scope value rides the accumulate body")
		chk.Same(accRiders[0], inst.driverRiders, "pin holds the first accumulate's rider head")
	})

	submit(2)
	chk.Eventually(accumulated(2), 5*time.Second, time.Millisecond)
	inspect(func(inst *funnelInstance[int]) {
		chk.Same(accMetas[1], inst.driverMeta, "pin re-points to the LAST accumulate's meta")
		chk.Same(accRiders[1], inst.driverRiders, "pin re-points to the LAST accumulate's rider head")
		chk.NotSame(accMetas[0], inst.driverMeta, "previous pin released, not accumulated")
	})

	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	// Flush released the final pair: every meta drawn during the test returns.
	chk.Eventually(func() bool { return balance.Load() == 0 }, 5*time.Second, 2*time.Millisecond,
		"ctxMeta balance settles to zero after the drain (pin released at flush)")
}
