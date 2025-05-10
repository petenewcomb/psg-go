// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"
)

// Observable uses psg to run a few tasks and produce logging that demonstrate
// the sequence of events.
func Example_observable() {
	startTime := time.Now()
	msSinceStart := func() int64 {
		// Truncate to the nearest 10ms to make the output stable across runs
		return (time.Since(startTime).Milliseconds() / 10) * 10
	}

	ctx := context.Background()

	// Define a factory to bind task-specific inputs and resources into a
	// generic task function
	newTaskFunc := func(taskName string) psg.TaskFunc[string] {
		return func(context.Context) (string, error) {
			// Simulate latency
			switch taskName {
			case "A":
				time.Sleep(60 * time.Millisecond)
			case "B":
				time.Sleep(10 * time.Millisecond)
			case "C":
				time.Sleep(30 * time.Millisecond)
			}
			fmt.Printf("%3dms:   task %q complete\n", msSinceStart(), taskName)
			// Return mock data
			return "result for task " + taskName, nil
		}
	}

	// Define a result aggregation function, which will run in the top-level
	// goroutine from within calls to Scatter and GatherAll.
	var results []string
	gather := psg.NewGather(
		func(ctx context.Context, result string, err error) error {
			time.Sleep(10 * time.Millisecond)
			fmt.Printf("%3dms:   gathered result %q\n", msSinceStart(), result)
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

	// Launch some tasks
	fmt.Println("starting job")
	for _, taskName := range []string{"A", "B", "C"} {
		err := gather.Scatter(ctx, pool, newTaskFunc(taskName))
		if err != nil {
			fmt.Printf("error launching task %q: %v\n", taskName, err)
		}
		fmt.Printf("%3dms: launched task %q\n", msSinceStart(), taskName)
	}

	// Wait a bit to ensure stable output
	time.Sleep(10 * time.Millisecond)

	// Wait for all tasks to complete
	fmt.Printf("%3dms: gathering remaining tasks\n", msSinceStart())
	err := job.CloseAndGatherAll(ctx)
	if err != nil {
		fmt.Printf("error during gather: %v\n", err)
	}
	fmt.Printf("%3dms: gathering complete\n", msSinceStart())

	// Print the aggregated results
	for i, result := range results {
		fmt.Printf("results[%d]=%q\n", i, result)
	}

	// Output:
	// starting job
	//   0ms: launched task "A"
	//   0ms: launched task "B"
	//  10ms:   task "B" complete
	//  10ms: launched task "C"
	//  20ms: gathering remaining tasks
	//  30ms:   gathered result "result for task B"
	//  40ms:   task "C" complete
	//  50ms:   gathered result "result for task C"
	//  60ms:   task "A" complete
	//  70ms:   gathered result "result for task A"
	//  70ms: gathering complete
	// results[0]="result for task B"
	// results[1]="result for task C"
	// results[2]="result for task A"
}
