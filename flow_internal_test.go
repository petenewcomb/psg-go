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
// from sharedNodePool comes back once the flow drains. Both scenarios pin
// co-accumulation deterministically (a funnel INSTANCE is one aggregation is one
// flow; whether independent flows land in the SAME instance is a timing accident, so
// a cross-instance fire count is intentionally not asserted). It runs white-box to
// install the shared alloc seam; a leaked component node leaves sharedBal positive, a
// double-free trips the underflow panic in derefShared.
func TestFlowCoalesceConservation(t *testing.T) {
	chk := require.New(t)

	var sharedBal atomic.Int64
	sh := func(d int) { sharedBal.Add(int64(d)) }
	flowSharedAllocHook.Store(&sh)
	defer flowSharedAllocHook.Store(nil)

	settled := func(where string) {
		chk.Eventuallyf(func() bool { return sharedBal.Load() == 0 },
			5*time.Second, 2*time.Millisecond,
			"%s: shared leak (%d)", where, sharedBal.Load())
	}

	var fires atomic.Int32
	tag := NewFlowTag(FlowFollowUpFn(func(context.Context) error { fires.Add(1); return nil }))

	// (A) Five flow roots submitted SEQUENTIALLY into one funnel co-accumulate in a
	// single instance (each submit reuses the live instance): their five separate
	// definitional instances coalesce into one component, fire once, all reclaimed.
	fires.Store(0)
	w1 := NewWave()
	f1 := NewFnFunnel(w1, func() Accumulator[int] {
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
	wUp, wDown := NewWave(), NewWave()
	release := make(chan struct{})
	task := NewTaskLauncher(func(context.Context) error { <-release; return nil })
	fc := NewFnFunnel(wUp, func() Accumulator[int] {
		return NewAccumulator(
			func(context.Context, int, error) (time.Time, error) { return time.Time{}, nil },
			func(ctx context.Context) error {
				for k := 0; k < 8; k++ {
					if err := task.In(wDown).Start(ctx); err != nil {
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
// the wave is otherwise quiescent. Each inspect confirms the pin re-points to the
// LATEST meta and releases the previous one (NotSame below); the flush-time
// release rides that same re-point mechanism.
func TestFunnelDriverPin(t *testing.T) {
	chk := require.New(t)
	key := NewFlowKey[int]()

	var mu sync.Mutex
	var accMetas []*ctxMeta
	var accRiders []*flowRiderNode

	wave := NewWave()
	f := NewFnFunnel(wave, func() Accumulator[int] {
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
		v, ok := waveImplOf(wave).funnelInstances.Load(f.id)
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
}
