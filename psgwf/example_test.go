// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"

	"github.com/petenewcomb/psg-go/psgwf"
)

// Example demonstrates workflow context for API request handling where
// client disconnection cancels only that request's operations.
func Example() {
	// Create a long-running job for the API server
	job := psg.NewJob(context.Background())
	defer job.CancelAndWait()

	// Create a task pool
	pool := psg.NewTaskPool(job, 10)

	// Track completed operations for ordered output
	var mu sync.Mutex
	completed := []string{}

	// Create a gather for collecting results
	resultGather := psgwf.NewGather(func(ctx context.Context, wf psgwf.Workflow, msg string, err error) error {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			completed = append(completed, fmt.Sprintf("Error: %v", err))
		} else {
			completed = append(completed, msg)
		}
		return nil
	})

	// Simulate handling a request
	handleRequest := func(requestID string, clientCtx context.Context, sleepTime time.Duration) {
		wf := psgwf.New(clientCtx)
		defer wf.Unref()

		// Launch operation for this request
		err := resultGather.Scatter(clientCtx, pool, wf, func(ctx context.Context, wf psgwf.Workflow) (string, error) {
			select {
			case <-time.After(sleepTime):
				return fmt.Sprintf("[%s] completed", requestID), nil
			case <-wf.Ctx.Done():
				return "", fmt.Errorf("[%s] cancelled", requestID)
			}
		})

		if err != nil {
			mu.Lock()
			completed = append(completed, fmt.Sprintf("Error: scatter failed for %s: %v", requestID, err))
			mu.Unlock()
		}
	}

	// Simulate request that completes
	ctx1, cancel1 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel1()
	handleRequest("req1", ctx1, 10*time.Millisecond)

	// Simulate request where client disconnects
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	handleRequest("req2", ctx2, 50*time.Millisecond)

	// Close the job and gather all results
	if err := job.CloseAndGatherAll(context.Background()); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Print results in order
	mu.Lock()
	for _, result := range completed {
		fmt.Println(result)
	}
	mu.Unlock()

	// Output:
	// [req1] completed
	// Error: [req2] cancelled
}
