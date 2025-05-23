// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go"
)

// Demonstrates job cancellation from the outer layer.
func ExampleJob_Cancel_outer() {

	ctx := context.Background()

	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)

	// This is the standard deferred call to Job.CancelAndWait that should
	// almost always follow creation of a new Job to ensure cleanup. It is not
	// the call to Job.Cancel that is the subject of this example.
	defer job.CancelAndWait()

	printResult := func(ctx context.Context, result string, err error) error {
		fmt.Printf("Got %q, err=%v\n", result, err)
		return nil
	}

	// Launch first task
	fmt.Println("Launching first task")
	err := psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Simulate a long-running task
			time.Sleep(100 * time.Millisecond)
			return "first task result", nil
		},
		printResult,
	)
	if err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which must wait for the first result to be gathered
	// because the pool's concurrency limit is one.
	fmt.Println("Launching second task")
	err = psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Simulate a long-running task
			time.Sleep(100 * time.Millisecond)
			return "second task result", nil
		},
		printResult,
	)
	if err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Allow the second task to complete and be waiting for its result to be
	// gathered.
	time.Sleep(100 * time.Millisecond)

	// Force cancellation from the outer level before the second task is
	// gathered.
	job.Cancel()

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
func ExampleJob_Cancel_task() {

	ctx := context.Background()

	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)

	// This is the standard deferred call to Job.CancelAndWait that should
	// almost always follow creation of a new Job to ensure cleanup. It is not
	// the call to Job.Cancel that is the subject of this example.
	defer job.CancelAndWait()

	printResult := func(ctx context.Context, result string, err error) error {
		fmt.Printf("Got %q, err=%v\n", result, err)
		return nil
	}

	// Launch first task
	fmt.Println("Launching first task")
	err := psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			return "first task result", nil
		},
		printResult,
	)
	if err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which also provides an opportunity for the first task
	// result to be gathered.
	fmt.Println("Launching second task")
	err = psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			// Force cancellation from inside the task. This is a way to cut
			// stop the overall job due to a fatal error within a task without
			// even waiting for the task result to be gathered.
			job.Cancel()
			return "second task result", nil
		},
		printResult,
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

// Demonstrates job cancellation from inside a gather function.
func ExampleJob_Cancel_gather() {

	ctx := context.Background()

	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)

	// This is the standard deferred call to Job.CancelAndWait that should
	// almost always follow creation of a new Job to ensure cleanup. It is not
	// the call to Job.Cancel that is the subject of this example.
	defer job.CancelAndWait()

	printResult := func(ctx context.Context, result string, err error) error {
		fmt.Printf("Got %q, err=%v\n", result, err)
		// Force cancellation
		job.Cancel()
		return nil
	}

	// Launch first task
	fmt.Println("Launching first task")
	err := psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			return "first task result", nil
		},
		printResult,
	)
	if err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which also provides an opportunity for the first task
	// result to be gathered.
	fmt.Println("Launching second task")
	err = psg.Scatter(
		ctx,
		pool,
		func(context.Context) (string, error) {
			return "second task result", nil
		},
		printResult,
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
