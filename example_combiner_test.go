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
)

// Example_combine demonstrates how combiners can efficiently aggregate
// results from multiple tasks before emitting a combined result.
func ExampleNewCombine() {
	startTime := time.Now()
	msSinceStart := func() int64 {
		// Truncate to the nearest 10ms to make the output stable across runs
		return (time.Since(startTime).Milliseconds() / 10) * 10
	}

	var inFlight atomic.Int32

	// Define a factory to bind task-specific inputs and resources into a
	// generic task function
	newTask := func(number int, delay time.Duration, result string) psg.TaskFunc[string] {
		return func(context.Context) (string, error) {
			// Simulate a long-running task
			time.Sleep(delay)
			fmt.Printf("%3dms:   task %d (%v -> %q) complete, in-flight count now %d\n",
				msSinceStart(), number, delay, result, inFlight.Add(-1))
			return result, nil
		}
	}

	newCombiner := func() psg.Combiner[string, map[string]int] {
		// Aggregation state variable shared between combine and flush
		var counts map[string]int

		return psg.FuncCombiner[string, map[string]int]{
			CombineFunc: func(ctx context.Context, result string, err error, emit psg.CombinerEmitFunc[map[string]int]) {
				time.Sleep(10 * time.Millisecond)
				if counts == nil {
					fmt.Printf("%3dms:   created new combiner\n", msSinceStart())
					counts = make(map[string]int)
				}
				counts[result]++
				fmt.Printf("%3dms:   combined %q, result counts now: %v\n", msSinceStart(), result, counts)
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[map[string]int]) {
				fmt.Printf("%3dms:   flushing result counts: %v\n", msSinceStart(), counts)
				if counts != nil {
					emit(ctx, counts, nil)
					counts = nil
				}
			},
		}
	}

	// Define the results array
	var results []map[string]int

	gatherFunc := func(ctx context.Context, result map[string]int, err error) error {
		fmt.Printf("%3dms:   gathering result counts: %v\n", msSinceStart(), result)
		// Safe because gatherFunc will only ever be called from the current
		// goroutine within calls to Scatter and GatherAll below.
		results = append(results, result)
		return err
	}

	ctx := context.Background()

	// Create a task pool with concurrency limit 2
	taskPool := psg.NewTaskPool(2)

	// Create a combiner pool with concurrency limit 1
	combinerPool := psg.NewCombinerPool(ctx, 1)

	// Create a scatter-gather job with the above task pool
	job := psg.NewJob(ctx, taskPool)
	defer job.CancelAndWait()

	// Set a flush listener to observe when all tasks have completed
	job.SetFlushListener(func() {
		fmt.Printf("%3dms: flush: all tasks completed, waiting for combiners\n", msSinceStart())
	})

	// Define a result aggregation function and create a combined gather/combine operation
	gather := psg.NewGather(gatherFunc)

	// Create a Combine operation with the gather function and inline combiner factory
	combine := psg.NewCombine(gather, combinerPool, newCombiner)

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
		err := combine.Scatter(ctx, taskPool, newTask(i+1, spec.delay, spec.result))
		if err != nil {
			fmt.Printf("error launching task %d (%v -> %q): %v\n", i+1, spec.delay, spec.result, err)
		}
		fmt.Printf("%3dms: launched task %d: (%v -> %q), in-flight count now %d\n",
			msSinceStart(), i+1, spec.delay, spec.result, inFlight.Add(1))
	}

	// Wait for all tasks to complete
	fmt.Printf("%3dms: gathering remaining tasks\n", msSinceStart())
	err := job.CloseAndGatherAll(ctx)
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
