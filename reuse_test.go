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

// TestWaveManyDrainCyclesConcurrent drives many independent NewWave drain cycles
// concurrently. Each cycle constructs a fresh wave via streampool.NewWave, creates
// a funnel (so the flusher spawns) plus a launcher feeding it, then CloseAndSkimAll.
// Run concurrently under -race, it exercises many concurrent wave lifecycles and
// verifies per-cycle state isolation with no cross-cycle bleed.
func TestWaveManyDrainCyclesConcurrent(t *testing.T) {
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
				wave := streampool.NewWave()

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
				})

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
			}
		}()
	}
	wg.Wait()

	if want := int64(goroutines * cycles * 15); grandTotal.Load() != want {
		t.Fatalf("grand total = %d, want %d", grandTotal.Load(), want)
	}
}

// TestWaveSequentialDrainCycles is the minimal case: several independent NewWave
// drain cycles run in a row (no concurrency), each a fresh wave constructed via
// streampool.NewWave with no state carried across cycles.
func TestWaveSequentialDrainCycles(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		wave := streampool.NewWave()

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
			if err := fetcher.In(wave).Submit(ctx, k); err != nil {
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
