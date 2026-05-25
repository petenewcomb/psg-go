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
	"github.com/petenewcomb/psg-go/psgopt"
)

// Demonstrates job cancellation from the outer layer.
func ExamplePool_Cancel() {

	ctx := context.Background()

	job := psg.New(ctx)
	// This is the standard deferred call to Pool.CancelAndWait that should
	// almost always follow creation of a new Pool to ensure cleanup. It is not
	// the call to Pool.Cancel that is the subject of this example.
	defer job.CancelAndWait()

	pool := psg.NewTaskPool(job, psgopt.WithMaxConcurrency(1))

	printResult := psg.NewGatherer(
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	err := printResult.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Simulate a long-running task
			time.Sleep(20 * time.Millisecond)
			return "first task result", nil
		},
	)
	if err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which must wait for the first result to be gathered
	// because the pool's concurrency limit is one.
	fmt.Println("Launching second task")
	err = printResult.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Simulate a longer-running task
			time.Sleep(100 * time.Millisecond)
			return "second task result", nil
		},
	)
	if err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Cancel the job after gathering starts but before the second task
	// finishes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		job.Cancel()
	}()

	// Wait for all tasks to complete
	if err := job.CloseAndGatherAll(ctx); err != nil {
		fmt.Printf("Error while gathering: %v\n", err)
	}

	// Output:
	// Launching first task
	// Launching second task
	// Got "first task result", err=<nil>
	// Error while gathering: context canceled
}

// Demonstrates job cancellation from inside a task.
func ExamplePool_Cancel_task() {

	ctx := context.Background()

	job := psg.New(ctx)
	// This is the standard deferred call to Pool.CancelAndWait that should
	// almost always follow creation of a new Pool to ensure cleanup. It is not
	// the call to Pool.Cancel that is the subject of this example.
	defer job.CancelAndWait()

	pool := psg.NewTaskPool(job, psgopt.WithMaxConcurrency(1))

	printResult := psg.NewGatherer(
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	err := printResult.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			return "first task result", nil
		},
	)
	if err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Give the first task time to complete and post its result
	time.Sleep(10 * time.Millisecond)

	// Launch second task, which also provides an opportunity for the first task
	// result to be gathered.
	fmt.Println("Launching second task")
	err = printResult.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Force cancellation from inside the task. This is a way to cut
			// short the overall job due to a fatal error within a task without
			// even waiting for the task result to be gathered.
			job.Cancel()
			time.Sleep(10 * time.Millisecond)
			return "second task result", nil
		},
	)
	if err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Wait for all tasks to complete
	if err := job.CloseAndGatherAll(ctx); err != nil {
		fmt.Printf("Error while gathering: %v\n", err)
	}

	// Output:
	// Launching first task
	// Launching second task
	// Got "first task result", err=<nil>
	// Error while gathering: context canceled
}
