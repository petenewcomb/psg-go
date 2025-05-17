// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"fmt"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"

	"github.com/petenewcomb/psg-go/psgwf"
)

// Demonstrates workflow-specific cancellation in case of API client
// disconnection.
func Example_clientTimeout() {
	jobCtx, cancelJob := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelJob()

	// Create a long-running job for the API server
	job := psg.NewJob(jobCtx)
	defer job.CancelAndWait()

	// Create a task pool
	pool := psg.NewTaskPool(job, 10)

	startTime := time.Now()
	msSinceStart := func() int64 {
		// Truncate to the nearest 10ms to make the output stable across runs
		return (time.Since(startTime).Milliseconds() / 10) * 10
	}

	// Create a gather for collecting results
	gather := psgwf.NewGather(func(ctx context.Context, wf psgwf.Workflow, requestID string, err error) error {
		fmt.Printf("%2dms [%s] result gathered\n", msSinceStart(), requestID)
		return nil
	})

	newRequestTask := func(requestID string) psgwf.TaskFunc[string] {
		return func(ctx context.Context, wf psgwf.Workflow) (string, error) {
			select {
			case <-time.After(30 * time.Millisecond):
				fmt.Printf("%2dms [%s] task completed\n", msSinceStart(), requestID)
			case <-wf.Ctx.Done():
				fmt.Printf("%2dms [%s] workflow cancelled\n", msSinceStart(), requestID)
			case <-ctx.Done():
				fmt.Printf("%2dms [%s] job cancelled\n", msSinceStart(), requestID)
			}
			return requestID, nil
		}
	}

	// Simulate handling requests
	handleRequest := func(requestID string, clientCtx context.Context) {
		fmt.Printf("%2dms [%s] launching workflow\n", msSinceStart(), requestID)
		wf := psgwf.New(clientCtx)
		defer wf.Unref()
		// Launch operation
		err := gather.Scatter(context.Background(), pool, wf, newRequestTask(requestID))
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}

	fmt.Println("starting job")

	// Request 1: client disconnects early
	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel1()
	handleRequest("req1", ctx1)

	time.Sleep(10 * time.Millisecond)

	// Request 2: will complete successfully, even though request 1 was cancelled
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	handleRequest("req2", ctx2)

	time.Sleep(20 * time.Millisecond)

	// Request 3: will be canceled because the job is canceled
	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel3()
	handleRequest("req3", ctx3)

	time.Sleep(20 * time.Millisecond)

	fmt.Printf("gathering results\n")

	// Process results
	err := job.CloseAndGatherAll(context.Background())
	if err != nil {
		// For test output stability, don't report the error until req3 has had
		// a chance to report its cancellation.
		time.Sleep(10 * time.Millisecond)
		fmt.Printf("Error: %v\n", err)
	}

	fmt.Println("job ended")

	// Output:
	// starting job
	//  0ms [req1] launching workflow
	// 10ms [req2] launching workflow
	// 20ms [req1] workflow cancelled
	// 30ms [req3] launching workflow
	// 30ms [req1] result gathered
	// 40ms [req2] task completed
	// gathering results
	// 50ms [req2] result gathered
	// 60ms [req3] job cancelled
	// Error: context deadline exceeded
	// job ended
}
