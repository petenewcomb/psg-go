// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
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

	wave := streampool.NewWave()

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
	if err := firstRunner.In(wave).Start(ctx); err != nil {
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
	if err := secondRunner.In(wave).Start(ctx); err != nil {
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

	wave := streampool.NewWave()

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
	if err := firstRunner.In(wave).Start(ctx); err != nil {
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
	if err := secondRunner.In(wave).Start(ctx); err != nil {
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

// Demonstrates PER-REQUEST cancellation on a shared wave. Cancelling the whole
// drive context (see [ExampleWave_cancellation]) stops everything; often you want
// each request to own its OWN cancellation scope so cancelling one request leaves
// its siblings untouched. Carry the request's cancelable context as a flow value:
// [WithFlow] makes it a rider that every body under the request inherits — even
// async task bodies that run after WithFlow returns — and a task reads it with
// [FlowKey.From] to honor that request's cancellation independently. (A follow-up
// under the same key, via [FlowKey.FollowUp], can additionally cancel the scope at
// the flow's true end for cleanup on the success path.)
func ExampleWithFlow_perRequestCancellation() {
	wave := streampool.NewWave()
	reqCtx := streampool.NewFlowKey[context.Context]()

	// Collect results and print them sorted, so the example output is stable
	// regardless of which request's work finishes first.
	var mu sync.Mutex
	var results []string
	report := streampool.NewFnSkimmer(func(_ context.Context, line string, _ error) error {
		mu.Lock()
		results = append(results, line)
		mu.Unlock()
		return nil
	})

	// handle wraps one request in its own flow scope carrying that request's
	// cancelable context. The job context (jobCtx) roots the flow; the client's
	// context (clientCtx) rides as the flow value. The task honors the request's
	// cancellation; unrelated requests sharing the wave are unaffected.
	handle := func(jobCtx context.Context, id string, clientCtx context.Context, proceed <-chan struct{}) error {
		return streampool.WithFlow(jobCtx, func(fctx context.Context) error {
			task := streampool.NewTaskLauncher(func(ctx context.Context) error {
				rc, _ := reqCtx.From(ctx) // this request's own cancel scope
				select {
				case <-rc.Done():
					return report.Submit(ctx, id+": cancelled")
				case <-proceed:
					return report.Submit(ctx, id+": completed")
				}
			})
			return task.In(wave).Start(fctx)
		}, reqCtx.Value(clientCtx))
	}

	// req-A's client disconnects (its context is cancelled); req-B proceeds.
	jobCtx := context.Background()
	ctxA, cancelA := context.WithCancel(context.Background())
	proceedB := make(chan struct{})
	_ = handle(jobCtx, "req-A", ctxA, nil) // nil proceed: only cancellation frees it
	_ = handle(jobCtx, "req-B", context.Background(), proceedB)

	cancelA()       // cancels only req-A's in-flight work
	close(proceedB) // lets req-B complete

	if err := wave.CloseAndSkimAll(context.Background()); err != nil {
		fmt.Printf("skim error: %v\n", err)
	}
	sort.Strings(results)
	for _, line := range results {
		fmt.Println(line)
	}
	// Output:
	// req-A: cancelled
	// req-B: completed
}
