// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync"
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
	var w streampool.Wave
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})
	fetcher := streampool.NewLauncher[int](allocForwardHandler{collector}).In(&w)

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
	const ceiling = 70 // floor ~53 (min-of-5 is stable for this synchronous path)
	if a > ceiling {
		t.Errorf("launcher submit+skim allocs/op = %.2f, want <= %d", a, ceiling)
	}
}

// TestAllocsFunnelSubmitSteady measures one funnel submit (accumulate) on a
// long-lived wave + funnel — exercises the pooled funnelWork and funnelInstance.
func TestAllocsFunnelSubmitSteady(t *testing.T) {
	ctx := context.Background()
	var w streampool.Wave
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})
	// Pin concurrency to 1: exactly one live accumulator instance exists, so the
	// per-submit allocation count is deterministic (it depends on how many instances
	// coexist, which is otherwise scheduling-dependent).
	funnel := streampool.NewFnFunnel[int](&w, func() streampool.Accumulator[int] {
		var s int
		return streampool.FuncAccumulator[int]{
			AccumulateFn: func(_ context.Context, v int, _ error) (time.Time, error) { s += v; return time.Time{}, nil },
			FlushFn:      func(fctx context.Context) error { return collector.Submit(fctx, s) },
		}
	}, streampool.WithLimits(streampool.NewSemaphore(1)))

	op := func() {
		if err := funnel.Submit(ctx, 1); err != nil {
			t.Fatalf("submit: %v", err)
		}
		// Skim opportunistically to keep the skim queue from growing.
		_, _ = w.TrySkim(ctx)
	}
	a := allocsPerOp(t, 500, 1000, op)
	t.Logf("funnel submit (accumulate): %.2f allocs/op", a)
	const ceiling = 110 // COARSE: funnel accumulate is async + bimodal (~40 or ~86); gross regressions only
	if a > ceiling {
		t.Errorf("funnel submit allocs/op = %.2f, want <= %d", a, ceiling)
	}
}

// TestAllocsWaveReuseCycle measures one full reuse cycle of a pooled *Wave with a
// funnel: dispatch a few inputs, drain, return to the pool. This is the guard that
// catches a pooling regression in the wave/funnel/instance lifecycle (e.g. instance
// wrappers not recycled at drain). The ops are recreated each cycle (the realistic
// pattern), so the count includes the funnel struct + fresh wavestate; the assertion
// is that it stays CONSTANT across cycles.
func TestAllocsWaveReuseCycle(t *testing.T) {
	ctx := context.Background()
	pool := sync.Pool{New: func() any { return new(streampool.Wave) }}
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})

	cycle := func() {
		w := pool.Get().(*streampool.Wave)
		funnel := streampool.NewFnFunnel[int](w, func() streampool.Accumulator[int] {
			var s int
			return streampool.FuncAccumulator[int]{
				AccumulateFn: func(_ context.Context, v int, _ error) (time.Time, error) { s += v; return time.Time{}, nil },
				FlushFn:      func(fctx context.Context) error { return collector.Submit(fctx, s) },
			}
		}, streampool.WithLimits(streampool.NewSemaphore(1)))
		for k := 0; k < 4; k++ {
			if err := funnel.Submit(ctx, 1); err != nil {
				t.Fatalf("submit: %v", err)
			}
		}
		if err := w.CloseAndSkimAll(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		pool.Put(w)
	}
	a := allocsPerOp(t, 200, 500, cycle)
	t.Logf("wave reuse cycle (funnel, 4 inputs): %.2f allocs/cycle", a)
	const ceiling = 600 // COARSE: async + bimodal per-run (~261 or ~450); catches gross regressions only
	if a > ceiling {
		t.Errorf("wave reuse cycle allocs = %.2f, want <= %d", a, ceiling)
	}
}
