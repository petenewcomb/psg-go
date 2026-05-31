// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/exmpclk"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
)

// ExampleCombiner demonstrates how combiners can efficiently aggregate
// results from multiple tasks before emitting a combined result.
func ExampleCombiner() {
	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	var inFlight atomic.Int32

	// Define the results array
	var results []map[string]int

	gatherFn := func(ctx context.Context, result map[string]int, err error) error {
		fmt.Printf("%3dms:   gathering result counts: %v\n", msSinceStart(), result)
		// Safe because gatherFn will only ever be called from the current
		// goroutine within calls to Start and GatherAll below.
		results = append(results, result)
		return err
	}

	ctx := context.Background()

	// Create a scatter-gather wave with flush listener to observe when all tasks have completed
	ctx, wave := psg.NewWave(ctx, psg.WithPoolOptions(psgopt.WithFlushListener(func() {
		fmt.Printf("%3dms: flush: all tasks completed, waiting for combiners\n", msSinceStart())
	})))
	defer wave.CancelAndWait()

	// Limit concurrent tasks to 2.
	taskLimit := psg.NewSemaphore(2)

	// Create a combiner pool and disable the idle timeout
	combinerPool := psg.NewCombinerPool(wave.Pool(), psgopt.WithIdleTimeout(-1))

	// Define a result aggregation function and create a combined gather/combine operation
	gatherer := psg.NewGatherer(psgfn.HandlerFunc[map[string]int](gatherFn))

	// After Wave 2, the Accumulator factory captures the downstream
	// gatherer in its closure and Submits the aggregated map from
	// inside FlushFn — there is no framework-routed output type.
	newAccumulator := func() psgfn.Accumulator[string] {
		var counts map[string]int

		return psgfn.FuncAccumulator[string]{
			AccumulateFn: func(ctx context.Context, result string, err error) (time.Time, error) {
				clock.Sleep(10 * time.Millisecond)
				if counts == nil {
					fmt.Printf("%3dms:   created new combiner\n", msSinceStart())
					counts = make(map[string]int)
				}
				counts[result]++
				fmt.Printf("%3dms:   combined %q, result counts now: %v\n", msSinceStart(), result, counts)
				return time.Time{}, nil
			},
			FlushFn: func(ctx context.Context) error {
				fmt.Printf("%3dms:   flushing result counts: %v\n", msSinceStart(), counts)
				return gatherer.Submit(ctx, wave, counts)
			},
		}
	}

	// Create a Combine operation. No Gatherer arg — the Accumulator
	// body routes results downstream via Submit.
	combineOp := psg.NewCombiner(combinerPool, newAccumulator)
	defer combineOp.Close()

	// Build a TaskRunner factory: the task body submits its result to
	// combineOp from inside the task context.
	newRunner := func(number int, delay time.Duration, result string) psg.TaskRunner0 {
		return psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			// Simulate a long-running task
			clock.Sleep(delay)
			fmt.Printf("%3dms:   task %d (%v -> %q) complete, in-flight count now %d\n",
				msSinceStart(), number, delay, result, inFlight.Add(-1))
			return combineOp.Submit(ctx, result)
		}), psg.WithLimits(taskLimit))
	}

	// Launch some tasks
	fmt.Println("starting job")
	for i, spec := range []struct {
		delay  time.Duration
		result string
	}{
		{10 * time.Millisecond, "A"}, // will launch at 0ms, complete at 10ms, combine at 20ms
		{50 * time.Millisecond, "B"}, // will launch at 0ms, complete at 50ms, combine at 60ms
		{20 * time.Millisecond, "C"}, // will launch at 10ms, complete at 30ms, combine at 40ms
		{40 * time.Millisecond, "D"}, // will launch at 30ms, complete at 70ms, combine at 80ms
		{40 * time.Millisecond, "A"}, // will launch at 50ms, complete at 90ms, combine at 100ms
	} {
		err := newRunner(i+1, spec.delay, spec.result).Start(ctx, wave)
		if err != nil {
			fmt.Printf("error launching task %d (%v -> %q): %v\n", i+1, spec.delay, spec.result, err)
		}
		fmt.Printf("%3dms: launched task %d: (%v -> %q), in-flight count now %d\n",
			msSinceStart(), i+1, spec.delay, spec.result, inFlight.Add(1))
	}

	// Wait for all tasks to complete
	fmt.Printf("%3dms: gathering remaining tasks\n", msSinceStart())
	err := wave.CloseAndGatherAll(ctx)
	if err != nil {
		fmt.Printf("error during gather: %v\n", err)
	}
	fmt.Printf("%3dms: gathering complete\n", msSinceStart())

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
	//  20ms:   created new combiner
	//  20ms:   combined "A", result counts now: map[A:1]
	//  30ms:   task 3 (20ms -> "C") complete, in-flight count now 1
	//  30ms: launched task 4: (40ms -> "D"), in-flight count now 2
	//  40ms:   combined "C", result counts now: map[A:1 C:1]
	//  50ms:   task 2 (50ms -> "B") complete, in-flight count now 1
	//  50ms: launched task 5: (40ms -> "A"), in-flight count now 2
	//  50ms: gathering remaining tasks
	//  60ms:   combined "B", result counts now: map[A:1 B:1 C:1]
	//  70ms:   task 4 (40ms -> "D") complete, in-flight count now 1
	//  80ms:   combined "D", result counts now: map[A:1 B:1 C:1 D:1]
	//  90ms:   task 5 (40ms -> "A") complete, in-flight count now 0
	// 100ms:   combined "A", result counts now: map[A:2 B:1 C:1 D:1]
	// 100ms: flush: all tasks completed, waiting for combiners
	// 100ms:   flushing result counts: map[A:2 B:1 C:1 D:1]
	// 100ms:   gathering result counts: map[A:2 B:1 C:1 D:1]
	// 100ms: gathering complete
	// results[0]=map[A:2 B:1 C:1 D:1]
}
