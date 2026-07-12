// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
)

// These tests guard the per-op allocation counts of the framework's pooled hot
// paths. They run in the normal suite (unlike benchmarks) so an allocation
// regression — a pooled object that stops being recycled — FAILS rather than
// silently slipping through. Ceilings are set a little above the measured steady
// state; they assert the count stays CONSTANT, not that it is zero (some allocs,
// e.g. interface boxing, are inherent). Tune a ceiling only after confirming the new
// floor is legitimate.

// Typed handlers (structs, not closures) keep the user side off the hot path so the
// measurement reflects framework allocations.

type allocAddHandler struct{ sum *int64 }

func (h allocAddHandler) Handle(_ context.Context, v int, err error) error {
	if err != nil {
		return err
	}
	atomic.AddInt64(h.sum, int64(v))
	return nil
}

type allocForwardHandler struct{ sink streampool.Skimmer[int] }

func (h allocForwardHandler) Handle(ctx context.Context, v int, err error) error {
	if err != nil {
		return err
	}
	return h.sink.Submit(ctx, v)
}

// allocsPerOp returns the steady-state allocations per call of fn, as the MINIMUM of
// several AllocsPerRun samples. The minimum is the cleanest run — pools warm, no
// worker spawn/retire churn, no GC inside the window — which is the deterministic
// floor we want to guard. (A single AllocsPerRun is flaky on this concurrent codebase:
// occasional worker spawns and async-work attribution add one-off spikes. Asserting
// the floor catches a true pooling regression — which raises the floor — without
// flaking on that noise.)
func allocsPerOp(t *testing.T, warmup, runs int, fn func()) float64 {
	t.Helper()
	// The race detector adds per-allocation bookkeeping, so allocs/op is inflated and
	// meaningless under -race; these floors are a no-race measurement.
	if raceEnabled {
		t.Skip("alloc counts are inflated under -race")
	}
	for i := 0; i < warmup; i++ {
		fn()
	}
	const samples = 5
	m := testing.AllocsPerRun(runs, fn)
	for i := 1; i < samples; i++ {
		if a := testing.AllocsPerRun(runs, fn); a < m {
			m = a
		}
	}
	return m
}

// TestAllocsLauncherSkimSteady measures one launcher dispatch + one skim of its
// forwarded result on a single long-lived wave (no per-op wave/op construction).
func TestAllocsLauncherSkimSteady(t *testing.T) {
	ctx := context.Background()
	w := streampool.NewWave()
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})
	fetcher := streampool.NewLauncher[int](allocForwardHandler{collector}).In(w)

	op := func() {
		if err := fetcher.Submit(ctx, 1); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if err := w.Skim(ctx); err != nil {
			t.Fatalf("skim: %v", err)
		}
	}
	a := allocsPerOp(t, 500, 1000, op)
	t.Logf("launcher submit+skim: %.2f allocs/op", a)
	// floor 0 post wave-refcount (pooled waveImpl + warm ctx/exEnv make the steady dispatch
	// allocation-free); margin left for occasional worker-spawn spikes on loaded machines.
	const ceiling = 20
	if a > ceiling {
		t.Errorf("launcher submit+skim allocs/op = %.2f, want <= %d", a, ceiling)
	}
}

// TestAllocsFunnelSubmitSteady measures one funnel submit (accumulate) on a
// long-lived wave + funnel — exercises the pooled funnelWork and funnelInstance.
func TestAllocsFunnelSubmitSteady(t *testing.T) {
	ctx := context.Background()
	w := streampool.NewWave()
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})
	// Pin concurrency to 1: exactly one live accumulator instance exists, so the
	// per-submit allocation count is deterministic (it depends on how many instances
	// coexist, which is otherwise scheduling-dependent).
	funnel := streampool.NewFnFunnel[int](w, func() streampool.Accumulator[int] {
		var s int
		return streampool.FuncAccumulator[int]{
			AccumulateFn: func(_ context.Context, v int, _ error) (time.Time, error) { s += v; return time.Time{}, nil },
			FlushFn:      func(fctx context.Context) error { return collector.Submit(fctx, s) },
		}
	}).WithLimits(streampool.NewSemaphore(1))

	op := func() {
		if err := funnel.Submit(ctx, 1); err != nil {
			t.Fatalf("submit: %v", err)
		}
		// Skim opportunistically to keep the skim queue from growing.
		_, _ = w.TrySkim(ctx)
	}
	a := allocsPerOp(t, 500, 1000, op)
	t.Logf("funnel submit (accumulate): %.2f allocs/op", a)
	// floor 0 post wave-refcount (pooled instance/work + warm ctx); margin for async spikes.
	const ceiling = 25
	if a > ceiling {
		t.Errorf("funnel submit allocs/op = %.2f, want <= %d", a, ceiling)
	}
}

// TestAllocsWavePerCycle measures one full drain cycle of a freshly constructed
// Wave with a funnel: construct via NewWave, dispatch a few inputs, drain. This is
// the guard that catches a pooling regression in the wave/funnel/instance lifecycle
// (e.g. instance wrappers not recycled at drain). A fresh wave and ops are
// constructed each cycle (the realistic pattern), so the count includes the funnel
// struct + fresh wavestate; the assertion is that it stays CONSTANT across cycles.
func TestAllocsWavePerCycle(t *testing.T) {
	ctx := context.Background()
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})

	cycle := func() {
		w := streampool.NewWave()
		funnel := streampool.NewFnFunnel[int](w, func() streampool.Accumulator[int] {
			var s int
			return streampool.FuncAccumulator[int]{
				AccumulateFn: func(_ context.Context, v int, _ error) (time.Time, error) { s += v; return time.Time{}, nil },
				FlushFn:      func(fctx context.Context) error { return collector.Submit(fctx, s) },
			}
		}).WithLimits(streampool.NewSemaphore(1))
		for k := 0; k < 4; k++ {
			if err := funnel.Submit(ctx, 1); err != nil {
				t.Fatalf("submit: %v", err)
			}
		}
		if err := w.CloseAndSkimAll(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
	a := allocsPerOp(t, 200, 500, cycle)
	t.Logf("wave per cycle (funnel, 4 inputs): %.2f allocs/cycle", a)
	// floor ~25-26 post wave-refcount: NewWave draws a warm pooled waveImpl each cycle, so a
	// full construct+funnel+4-submit+drain cycle is ~26 allocs (was ~261-450 un-pooled).
	const ceiling = 60
	if a > ceiling {
		t.Errorf("wave per cycle allocs = %.2f, want <= %d", a, ceiling)
	}
}
