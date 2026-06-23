// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/internal/exmpclk"
)

// ExampleFunnel demonstrates how funnels can efficiently aggregate
// results from multiple tasks before emitting a funneld result.
func ExampleFunnel() {
	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	var inFlight atomic.Int32

	// Define the results array
	var results []map[string]int

	skimFn := func(ctx context.Context, result map[string]int, err error) error {
		fmt.Printf("%3dms:   skimming result counts: %v\n", msSinceStart(), result)
		// Safe because skimFn will only ever be called from the current
		// goroutine within calls to Start and SkimAll below.
		results = append(results, result)
		return err
	}

	ctx := context.Background()

	// A zero-value Wave is ready to use; it owns no context and drains via
	// CloseAndSkimAll below.
	var wave streampool.Wave

	// Limit concurrent tasks to 2.
	taskLimit := streampool.NewSemaphore(2)

	funnelPool := &wave

	// Define a result aggregation function and create a funneld skim/funnel operation
	skimmer := streampool.NewFnSkimmer(skimFn).In(&wave)

	// After Wave 2, the streampool.Accumulator factory captures the downstream
	// skimmer in its closure and Submits the aggregated map from
	// inside FlushFn — there is no framework-routed output type.
	newAccumulator := streampool.NewAccumulatorFactory(func() streampool.Accumulator[string] {
		var counts map[string]int

		return streampool.FuncAccumulator[string]{
			AccumulateFn: func(ctx context.Context, result string, err error) (time.Time, error) {
				clock.Sleep(10 * time.Millisecond)
				if counts == nil {
					fmt.Printf("%3dms:   created new funnel\n", msSinceStart())
					counts = make(map[string]int)
				}
				counts[result]++
				fmt.Printf("%3dms:   funneld %q, result counts now: %v\n", msSinceStart(), result, counts)
				return time.Time{}, nil
			},
			FlushFn: func(ctx context.Context) error {
				fmt.Printf("%3dms:   flushing result counts: %v\n", msSinceStart(), counts)
				return skimmer.Submit(ctx, counts)
			},
		}
	}, nil)

	// Create a Funnel operation. No Skimmer arg — the streampool.Accumulator
	// body routes results downstream via Submit.
	funnelOp := streampool.NewFunnel(funnelPool, newAccumulator)
	defer funnelOp.Close()

	// Build a Launcher factory: the task body submits its result to
	// funnelOp from inside the task context.
	newRunner := func(number int, delay time.Duration, result string) streampool.TaskLauncher {
		return streampool.NewTaskLauncher(func(ctx context.Context) error {
			// Simulate a long-running task
			clock.Sleep(delay)
			fmt.Printf("%3dms:   task %d (%v -> %q) complete, in-flight count now %d\n",
				msSinceStart(), number, delay, result, inFlight.Add(-1))
			return funnelOp.Submit(ctx, result)
		}, streampool.WithLimits(taskLimit))
	}

	// Launch some tasks
	fmt.Println("starting job")
	for i, spec := range []struct {
		delay  time.Duration
		result string
	}{
		{10 * time.Millisecond, "A"}, // will launch at 0ms, complete at 10ms, funnel at 20ms
		{50 * time.Millisecond, "B"}, // will launch at 0ms, complete at 50ms, funnel at 60ms
		{20 * time.Millisecond, "C"}, // will launch at 10ms, complete at 30ms, funnel at 40ms
		{40 * time.Millisecond, "D"}, // will launch at 30ms, complete at 70ms, funnel at 80ms
		{40 * time.Millisecond, "A"}, // will launch at 50ms, complete at 90ms, funnel at 100ms
	} {
		err := newRunner(i+1, spec.delay, spec.result).In(&wave).Start(ctx)
		if err != nil {
			fmt.Printf("error launching task %d (%v -> %q): %v\n", i+1, spec.delay, spec.result, err)
		}
		fmt.Printf("%3dms: launched task %d: (%v -> %q), in-flight count now %d\n",
			msSinceStart(), i+1, spec.delay, spec.result, inFlight.Add(1))
	}

	// Wait for all tasks to complete
	fmt.Printf("%3dms: skimming remaining tasks\n", msSinceStart())
	err := wave.CloseAndSkimAll(ctx)
	if err != nil {
		fmt.Printf("error during skim: %v\n", err)
	}
	fmt.Printf("%3dms: skimming complete\n", msSinceStart())

	// Print the aggregated results
	for i, result := range results {
		fmt.Printf("results[%d]=%v\n", i, result)
	}

	// Output:
	// starting job
	//   0ms: launched task 1: (10ms -> "A"), in-flight count now 1
	//   0ms: launched task 2: (50ms -> "B"), in-flight count now 2
	//  10ms:   task 1 (10ms -> "A") complete, in-flight count now 1
	//  10ms: launched task 3: (20ms -> "C"), in-flight count now 2
	//  20ms:   created new funnel
	//  20ms:   funneld "A", result counts now: map[A:1]
	//  30ms:   task 3 (20ms -> "C") complete, in-flight count now 1
	//  30ms: launched task 4: (40ms -> "D"), in-flight count now 2
	//  40ms:   funneld "C", result counts now: map[A:1 C:1]
	//  50ms:   task 2 (50ms -> "B") complete, in-flight count now 1
	//  50ms: launched task 5: (40ms -> "A"), in-flight count now 2
	//  50ms: skimming remaining tasks
	//  60ms:   funneld "B", result counts now: map[A:1 B:1 C:1]
	//  70ms:   task 4 (40ms -> "D") complete, in-flight count now 1
	//  80ms:   funneld "D", result counts now: map[A:1 B:1 C:1 D:1]
	//  90ms:   task 5 (40ms -> "A") complete, in-flight count now 0
	// 100ms:   funneld "A", result counts now: map[A:2 B:1 C:1 D:1]
	// 100ms:   flushing result counts: map[A:2 B:1 C:1 D:1]
	// 100ms:   skimming result counts: map[A:2 B:1 C:1 D:1]
	// 100ms: skimming complete
	// results[0]=map[A:2 B:1 C:1 D:1]
}
