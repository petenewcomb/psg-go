// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"fmt"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"
)

// Demonstrates job cancellation from the outer layer.
func ExampleWave_Cancel() {

	ctx := context.Background()

	ctx, wave := streampool.NewWave(ctx)
	// This is the standard deferred call to Wave.CancelAndWait that should
	// almost always follow creation of a new Wave to ensure cleanup. It is
	// not the call to Wave.Cancel that is the subject of this example.
	defer wave.CancelAndWait()

	limit := streampool.NewSemaphore(nil, 1)

	printResult := streampool.NewFnSkimmer(wave,
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	firstRunner := streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
		// Simulate a long-running task
		time.Sleep(20 * time.Millisecond)
		return printResult.Submit(ctx, "first task result")
	}, streampool.WithLimits(limit))
	if err := firstRunner.Start(ctx); err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which must wait for the first result to be skimmed
	// because the Limiter only grants one permit at a time.
	fmt.Println("Launching second task")
	secondRunner := streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
		// Simulate a longer-running task
		time.Sleep(100 * time.Millisecond)
		return printResult.Submit(ctx, "second task result")
	}, streampool.WithLimits(limit))
	if err := secondRunner.Start(ctx); err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Cancel the wave after skimming starts but before the second task
	// finishes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		wave.Cancel()
	}()

	// Wait for all tasks to complete
	if err := wave.CloseAndSkimAll(ctx); err != nil {
		fmt.Printf("Error while skimming: %v\n", err)
	}

	// Output:
	// Launching first task
	// Launching second task
	// Got "first task result", err=<nil>
	// Error while skimming: context canceled
}

// Demonstrates job cancellation from inside a task.
func ExampleWave_Cancel_task() {

	ctx := context.Background()

	ctx, wave := streampool.NewWave(ctx)
	// This is the standard deferred call to Wave.CancelAndWait that should
	// almost always follow creation of a new Wave to ensure cleanup. It is
	// not the call to Wave.Cancel that is the subject of this example.
	defer wave.CancelAndWait()

	limit := streampool.NewSemaphore(nil, 1)

	printResult := streampool.NewFnSkimmer(wave,
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	firstRunner := streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
		return printResult.Submit(ctx, "first task result")
	}, streampool.WithLimits(limit))
	if err := firstRunner.Start(ctx); err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Give the first task time to complete and post its result
	time.Sleep(10 * time.Millisecond)

	// Launch second task, which also provides an opportunity for the first task
	// result to be skimmed.
	fmt.Println("Launching second task")
	secondRunner := streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
		// Force cancellation from inside the task. This is a way to cut
		// short the overall wave due to a fatal error within a task without
		// even waiting for the task result to be skimmed.
		wave.Cancel()
		time.Sleep(10 * time.Millisecond)
		return printResult.Submit(ctx, "second task result")
	}, streampool.WithLimits(limit))
	if err := secondRunner.Start(ctx); err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Wait for all tasks to complete
	if err := wave.CloseAndSkimAll(ctx); err != nil {
		fmt.Printf("Error while skimming: %v\n", err)
	}

	// Output:
	// Launching first task
	// Launching second task
	// Got "first task result", err=<nil>
	// Error while skimming: context canceled
}
