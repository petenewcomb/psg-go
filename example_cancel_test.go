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

// Demonstrates cancellation from the outer layer. A Wave owns no context;
// cancellation is driven through the context you pass to the wave's drive and
// dispatch calls — cancel it and the drain returns, leaving in-flight bodies to
// observe their own (descendant) contexts.
func ExampleWave_cancellation() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wave streampool.Wave

	limit := streampool.NewSemaphore(1)

	printResult := streampool.NewFnSkimmer(
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	firstRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// Simulate a long-running task
		time.Sleep(20 * time.Millisecond)
		return printResult.Submit(ctx, "first task result")
	}).WithLimits(limit)
	if err := firstRunner.In(&wave).Start(ctx); err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Launch second task, which must wait for the first result to be skimmed
	// because the Limiter only grants one permit at a time.
	fmt.Println("Launching second task")
	secondRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// Simulate a longer-running task
		time.Sleep(100 * time.Millisecond)
		return printResult.Submit(ctx, "second task result")
	}).WithLimits(limit)
	if err := secondRunner.In(&wave).Start(ctx); err != nil {
		fmt.Printf("Failed to launch second task: %v\n", err)
	}

	// Cancel the drive context after skimming starts but before the second
	// task finishes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
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

// Demonstrates cancellation triggered from inside a task — a way to cut the
// overall wave short on a fatal error without waiting for results to be skimmed.
// The task cancels the drive context (captured in a closure); the drain then
// returns context.Canceled.
func ExampleWave_cancellation_task() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wave streampool.Wave

	limit := streampool.NewSemaphore(1)

	printResult := streampool.NewFnSkimmer(
		func(ctx context.Context, result string, err error) error {
			fmt.Printf("Got %q, err=%v\n", result, err)
			return nil
		},
	)

	// Launch first task
	fmt.Println("Launching first task")
	firstRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		return printResult.Submit(ctx, "first task result")
	}).WithLimits(limit)
	if err := firstRunner.In(&wave).Start(ctx); err != nil {
		fmt.Printf("Failed to launch first task: %v\n", err)
	}

	// Give the first task time to complete and post its result
	time.Sleep(10 * time.Millisecond)

	// Launch second task, which also provides an opportunity for the first task
	// result to be skimmed.
	fmt.Println("Launching second task")
	secondRunner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		// Cancel the drive context from inside the task to cut the wave short.
		cancel()
		time.Sleep(10 * time.Millisecond)
		return printResult.Submit(ctx, "second task result")
	}).WithLimits(limit)
	if err := secondRunner.In(&wave).Start(ctx); err != nil {
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
