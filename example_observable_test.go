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
	"github.com/petenewcomb/psg-go/internal/exmpclk"
	"github.com/petenewcomb/psg-go/psgfn"
)

// Observable uses psg to run a few tasks and produce logging that demonstrate
// the sequence of events.
func Example_observable() {
	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	ctx := context.Background()

	// Define a result aggregation function, which will run in the top-level
	// goroutine from within calls to Start and GatherAll.
	var results []string
	gatherer := psg.NewGatherer(
		func(ctx context.Context, result string, err error) error {
			clock.Sleep(10 * time.Millisecond)
			fmt.Printf("%3dms:   gathered result %q\n", msSinceStart(), result)
			// Safe because gather will only ever be called from the current
			// goroutine within calls to Start and GatherAll below.
			results = append(results, result)
			return err
		},
	)

	// Create a scatter-gather job
	job := psg.New(ctx)
	defer job.CancelAndWait()

	// Limit dispatch concurrency to 2.
	limit := psg.NewSemaphore(2)

	// Define a factory to bind task-specific inputs and resources into a
	// TaskRunner. The task body Submits its result to the gatherer.
	newRunner := func(taskName string) psg.TaskRunner0 {
		return psg.NewTaskRunner0(job, psgfn.TaskFunc0(func(ctx context.Context) error {
			// Simulate latency
			switch taskName {
			case "A":
				clock.Sleep(60 * time.Millisecond)
			case "B":
				clock.Sleep(10 * time.Millisecond)
			case "C":
				clock.Sleep(30 * time.Millisecond)
			}
			fmt.Printf("%3dms:   task %q complete\n", msSinceStart(), taskName)
			// Return mock data
			return gatherer.Submit(ctx, job, "result for task "+taskName, nil)
		}), psg.WithLimits(limit))
	}

	// Launch some tasks
	fmt.Println("starting job")
	for _, taskName := range []string{"A", "B", "C"} {
		err := newRunner(taskName).Start(ctx)
		if err != nil {
			fmt.Printf("error launching task %q: %v\n", taskName, err)
		}
		fmt.Printf("%3dms: launched task %q\n", msSinceStart(), taskName)
	}

	// Wait a bit to ensure stable output
	clock.Sleep(10 * time.Millisecond)

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
	//  20ms:   gathered result "result for task B"
	//  20ms: launched task "C"
	//  30ms: gathering remaining tasks
	//  50ms:   task "C" complete
	//  60ms:   task "A" complete
	//  60ms:   gathered result "result for task C"
	//  70ms:   gathered result "result for task A"
	//  70ms: gathering complete
	// results[0]="result for task B"
	// results[1]="result for task C"
	// results[2]="result for task A"
}
