// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go"
)

// Define a factory to bind task-specific inputs to generic task functions
func newDelayTask(number int, delay time.Duration, result string, msSinceStart func() int64) psg.TaskFunc[string] {
	return func(context.Context) (string, error) {
		// Simulate a long-running task
		time.Sleep(delay)
		fmt.Printf("%3dms:   task %d (%v -> %q) complete\n", msSinceStart(), number, delay, result)
		return result, nil
	}
}

// Example_combiner demonstrates how combiners can efficiently aggregate
// results from multiple tasks before emitting a combined result.
func Example_combiner() {
	startTime := time.Now()
	msSinceStart := func() int64 {
		// Truncate to the nearest 10ms to make the output stable across runs
		return (time.Since(startTime).Milliseconds() / 10) * 10
	}

	ctx := context.Background()

	// Define a result aggregation function using a Combiner that counts result occurrences
	var results []map[string]int
	resultCounter := psg.NewCombiner(ctx, 1,
		func() psg.CombinerFunc[string, map[string]int] {
			fmt.Printf("%3dms:   creating new result counter\n", msSinceStart())
			counts := make(map[string]int)
			return func(ctx context.Context, flush bool, result string, err error) (bool, map[string]int, error) {
				if flush {
					fmt.Printf("%3dms:   flushing result counts: %v\n", msSinceStart(), counts)
					return true, counts, nil
				}
				time.Sleep(10 * time.Millisecond)
				counts[result]++
				fmt.Printf("%3dms:   combined %q, result counts now: %v\n", msSinceStart(), result, counts)
				return false, nil, nil
			}
		},
		func(ctx context.Context, result map[string]int, err error) error {
			fmt.Printf("%3dms:   gathering result counts: %v\n", msSinceStart(), result)
			// Safe because gatherFunc will only ever be called from the current
			// goroutine within calls to Scatter and GatherAll below.
			results = append(results, result)
			return err
		},
	)

	// Create a scatter-gather pool with concurrency limit 2
	pool := psg.NewPool(2)

	// Create a scatter-gather job with the above pool
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	// Set a flush listener to observe when all tasks have completed
	job.SetFlushListener(func() {
		fmt.Printf("%3dms: flush: all tasks completed, waiting for combiners\n", msSinceStart())
	})

	// Launch some tasks
	fmt.Println("starting job")
	for i, spec := range []struct {
		delay  time.Duration
		result string
	}{
		{10 * time.Millisecond, "A"}, // will launch at 0ms, complete at 10ms, combine at 20ms
		{50 * time.Millisecond, "A"}, // will launch at 0ms, complete at 50ms, combine at 60ms
		{20 * time.Millisecond, "B"}, // will launch at 10ms, complete at 30ms, combine at 40ms
	} {
		err := resultCounter.Scatter(ctx, pool, newDelayTask(i+1, spec.delay, spec.result, msSinceStart))
		if err != nil {
			fmt.Printf("error launching task %d (%v -> %q): %v\n", i+1, spec.delay, spec.result, err)
		}
		fmt.Printf("%3dms: launched task %d: (%v -> %q)\n", msSinceStart(), i+1, spec.delay, spec.result)
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
	//   0ms: launched task 1: (10ms -> "A")
	//   0ms: launched task 2: (50ms -> "A")
	//  10ms:   task 1 (10ms -> "A") complete
	//  10ms:   creating new result counter
	//  10ms: launched task 3: (20ms -> "B")
	//  10ms: gathering remaining tasks
	//  20ms:   combined "A", result counts now: map[A:1]
	//  30ms:   task 3 (20ms -> "B") complete
	//  40ms:   combined "B", result counts now: map[A:1 B:1]
	//  50ms:   task 2 (50ms -> "A") complete
	//  60ms:   combined "A", result counts now: map[A:2 B:1]
	//  60ms: flush: all tasks completed, waiting for combiners
	//  60ms:   flushing result counts: map[A:2 B:1]
	//  60ms:   gathering result counts: map[A:2 B:1]
	//  60ms: gathering complete
	// results[0]=map[A:2 B:1]
}
