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

// TestWaveReuseAfterDrain is the motivating case for the reuse re-arm: a *Wave
// pooled via sync.Pool is driven through many drain cycles (allocation-free
// sub-waves). Each cycle creates a funnel (so the flusher re-spawns and must be
// joined on the next re-arm) plus a launcher feeding it, then CloseAndSkimAll, then
// returns the wave to the pool. Run concurrently under -race, it exercises the
// re-arm path (Done -> fresh Open), the flusher-join barrier, and per-cycle state
// reset with no cross-cycle bleed.
func TestWaveReuseAfterDrain(t *testing.T) {
	pool := sync.Pool{New: func() any { return new(streampool.Wave) }}

	const (
		goroutines = 8
		cycles     = 100
		perCycle   = 5 // sums to 1+2+3+4+5 = 15
	)

	var grandTotal atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < cycles; i++ {
				wave := pool.Get().(*streampool.Wave)

				var cycleSum atomic.Int64
				collector := streampool.NewSkimmer(streampool.HandlerFunc[int](
					func(_ context.Context, v int, err error) error {
						if err != nil {
							return err
						}
						cycleSum.Add(int64(v))
						return nil
					}))

				aggregator := streampool.NewFnFunnel(wave, func() streampool.Accumulator[int] {
					var s int
					return streampool.FuncAccumulator[int]{
						AccumulateFn: func(_ context.Context, v int, _ error) (time.Time, error) {
							s += v
							return time.Time{}, nil
						},
						FlushFn: func(fctx context.Context) error { return collector.Submit(fctx, s) },
					}
				}, nil)

				fetcher := streampool.NewLauncher(streampool.HandlerFunc[int](
					func(fctx context.Context, v int, _ error) error {
						return aggregator.Submit(fctx, v)
					}))

				for k := 1; k <= perCycle; k++ {
					if err := fetcher.In(wave).Submit(ctx, k); err != nil {
						t.Errorf("submit: %v", err)
						return
					}
				}
				if err := wave.CloseAndSkimAll(ctx); err != nil {
					t.Errorf("drain: %v", err)
					return
				}

				if got := cycleSum.Load(); got != 15 {
					t.Errorf("cycle sum = %d, want 15 (no cross-cycle bleed)", got)
					return
				}
				grandTotal.Add(cycleSum.Load())

				pool.Put(wave)
			}
		}()
	}
	wg.Wait()

	if want := int64(goroutines * cycles * 15); grandTotal.Load() != want {
		t.Fatalf("grand total = %d, want %d", grandTotal.Load(), want)
	}
}

// TestWaveReuseSequential is the minimal reuse case: one zero-value Wave variable
// driven through several drain cycles in a row (no pool, no concurrency), each
// re-arming the prior cycle's Done state.
func TestWaveReuseSequential(t *testing.T) {
	ctx := context.Background()
	var wave streampool.Wave

	for i := 0; i < 5; i++ {
		var sum atomic.Int64
		collector := streampool.NewSkimmer(streampool.HandlerFunc[int](
			func(_ context.Context, v int, _ error) error {
				sum.Add(int64(v))
				return nil
			}))
		fetcher := streampool.NewLauncher(streampool.HandlerFunc[int](
			func(fctx context.Context, v int, _ error) error {
				return collector.Submit(fctx, v)
			}))
		for k := 1; k <= 4; k++ {
			if err := fetcher.In(&wave).Submit(ctx, k); err != nil {
				t.Fatalf("cycle %d submit: %v", i, err)
			}
		}
		if err := wave.CloseAndSkimAll(ctx); err != nil {
			t.Fatalf("cycle %d drain: %v", i, err)
		}
		if got := sum.Load(); got != 10 { // 1+2+3+4
			t.Fatalf("cycle %d sum = %d, want 10", i, got)
		}
	}
}
