// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"fmt"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"

	"github.com/petenewcomb/streampool/psgwf"
)

// Example demonstrates basic workflow context usage.
func Example_simple() {
	// Create a wave
	ctx := context.Background()
	var wave streampool.Wave

	poolLimit := streampool.NewSemaphore(10)

	// Create a skim
	skimmer := psgwf.NewSkimmer(&wave, func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println(msg)
		}
		return nil
	})

	// Create a workflow
	wf := psgwf.New(ctx)

	// Start a task
	runner := psgwf.NewGenericLauncher(&wave, skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			return "Hello from workflow", nil
		}, poolLimit)
	err := runner.Start(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Process the result
	if err := wave.CloseAndSkimAll(ctx); err != nil {
		fmt.Printf("Error: %v\n", err)
	}

	// Output:
	// Hello from workflow
}
