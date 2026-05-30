// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"fmt"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"

	"github.com/petenewcomb/psg-go/psgwf"
)

// Example demonstrates basic workflow context usage.
func Example_simple() {
	// Create a job
	job := psg.New(context.Background())
	defer job.CancelAndWait()

	// Create a task pool
	pool := job
	poolLimit := psg.NewSemaphore(10)

	// Create a gather
	gatherer := psgwf.NewGatherer(func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println(msg)
		}
		return nil
	})

	// Create a workflow
	ctx := context.Background()
	wf := psgwf.New(ctx)

	// Start a task
	runner := psgwf.NewGenericTaskRunner(pool, gatherer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			return "Hello from workflow", nil
		}, psg.WithLimits(poolLimit))
	err := runner.Start(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Process the result
	if err := job.CloseAndGatherAll(context.Background()); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Output:
	// Hello from workflow
}
