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

	"github.com/petenewcomb/psg-go/internal/exmpclk"
	"github.com/petenewcomb/psg-go/psgopt"
	"github.com/petenewcomb/psg-go/psgwf"
)

// Demonstrates workflow-specific cancellation in case of API client
// disconnection.
func Example_clientTimeout() {

	// Create a long-running job for the API server
	job := psg.New(context.Background())
	defer job.CancelAndWait()

	// Create a task pool
	pool := psg.NewTaskPool(job, psgopt.WithMaxConcurrency(10))

	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	// Create a gather for collecting results
	gatherer := psgwf.NewGatherer(func(ctx context.Context, wf *psgwf.Workflow, requestID string, err error) error {
		fmt.Printf("%2dms [%s] result gathered\n", msSinceStart(), requestID)
		return nil
	})

	newRequestTaskFn := func(requestID string) psgwf.TaskFunc[string] {
		return func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			delay := 30 * time.Millisecond
			calibrationTimer := clock.CalibrationTimer()
			select {
			case <-time.After(delay):
				calibrationTimer.Stop(delay)
				fmt.Printf("%2dms [%s] task completed\n", msSinceStart(), requestID)
			case <-wf.Ctx().Done():
				fmt.Printf("%2dms [%s] workflow cancelled\n", msSinceStart(), requestID)
			case <-ctx.Done():
				fmt.Printf("%2dms [%s] job cancelled\n", msSinceStart(), requestID)
			}
			return requestID, nil
		}
	}

	// Simulate handling requests
	handleRequest := func(ctx context.Context, requestID string, clientCtx context.Context) {
		fmt.Printf("%2dms [%s] launching workflow\n", msSinceStart(), requestID)
		wf := psgwf.New(clientCtx)
		// Launch operation
		runner := psgwf.NewGenericTaskRunner(pool, gatherer, wf, newRequestTaskFn(requestID))
		err := runner.Start(ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}

	fmt.Println("starting job")

	ctx := context.Background()

	// Request 1: client disconnects early
	clientCtx1, cancel1 := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel1()
	handleRequest(ctx, "req1", clientCtx1)

	time.Sleep(10 * time.Millisecond)

	// Request 2: will complete successfully, even though request 1 was cancelled
	clientCtx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()
	handleRequest(ctx, "req2", clientCtx2)

	time.Sleep(20 * time.Millisecond)

	// Request 3: will be canceled because the job is canceled
	clientCtx3, cancel3 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel3()
	handleRequest(ctx, "req3", clientCtx3)

	time.Sleep(20 * time.Millisecond)

	fmt.Printf("gathering results\n")

	// Process results
	err := job.CloseAndGatherAll(ctx)
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
	// 60ms [req3] task completed
	// 60ms [req3] result gathered
	// job ended
}
