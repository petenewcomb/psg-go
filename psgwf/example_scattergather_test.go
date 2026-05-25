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

// Example_scatterGather demonstrates using workflow context to cancel
// related operations when one fails.
func Example_scatterGather() {
	// Create a job
	job := psg.New(context.Background())
	defer job.CancelAndWait()

	// Create a task pool with limited concurrency to control timing
	pool := psg.NewTaskPool(job, psgopt.WithMaxConcurrency(3))

	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	// Create a gather for collecting results
	gatherOp := psgwf.NewGatherOp(func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
		if err != nil {
			fmt.Printf("%3dms Error: %v\n", msSinceStart(), err)
		} else {
			fmt.Printf("%3dms %s\n", msSinceStart(), msg)
		}
		return nil
	})

	// Create workflow for this request
	wf := psgwf.New(context.Background())

	fmt.Println("Starting scatter-gather example")
	fmt.Printf("%3dms Starting tasks\n", msSinceStart())

	// First task completes quickly
	err := gatherOp.Scatter(context.Background(), pool, wf, func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
		fmt.Printf("%3dms Quick task started\n", msSinceStart())
		clock.Sleep(10 * time.Millisecond)
		fmt.Printf("%3dms Quick task completed\n", msSinceStart())
		return "Quick result", nil
	})
	if err != nil {
		fmt.Printf("%3dms Error starting quick task: %v\n", msSinceStart(), err)
	}

	// Sleep to ensure quick task completes and result is gathered before starting failing task
	clock.Sleep(20 * time.Millisecond)

	// Second task fails and cancels workflow
	err = gatherOp.Scatter(context.Background(), pool, wf, func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
		fmt.Printf("%3dms Failing task started\n", msSinceStart())
		clock.Sleep(30 * time.Millisecond)
		fmt.Printf("%3dms Failing task failed - cancelling workflow\n", msSinceStart())
		wf.Ctx().Cancel(fmt.Errorf("critical failure"))
		return "", fmt.Errorf("task failed")
	})
	if err != nil {
		fmt.Printf("%3dms Error starting failing task: %v\n", msSinceStart(), err)
	}

	// Sleep to ensure failing task starts before slow task
	clock.Sleep(10 * time.Millisecond)

	// Third task should be cancelled
	err = gatherOp.Scatter(context.Background(), pool, wf, func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
		fmt.Printf("%3dms Slow task started\n", msSinceStart())
		select {
		case <-time.After(100 * time.Millisecond):
			fmt.Printf("%3dms Slow task completed\n", msSinceStart())
			return "Slow result", nil
		case <-wf.Ctx().Done():
			// Add a small delay to ensure the cancellation prints after the failure
			clock.Sleep(10 * time.Millisecond)
			fmt.Printf("%3dms Slow task cancelled\n", msSinceStart())
			return "", context.Canceled
		}
	})
	if err != nil {
		fmt.Printf("%3dms Error starting slow task: %v\n", msSinceStart(), err)
	}

	// Wait to ensure all tasks have been processed
	clock.Sleep(10 * time.Millisecond)

	// Gather all results
	err = job.CloseAndGatherAll(context.Background())
	if err != nil {
		fmt.Printf("%3dms Error gathering: %v\n", msSinceStart(), err)
	}

	// Output:
	// Starting scatter-gather example
	//   0ms Starting tasks
	//   0ms Quick task started
	//  10ms Quick task completed
	//  20ms Quick result
	//  20ms Failing task started
	//  30ms Slow task started
	//  50ms Failing task failed - cancelling workflow
	//  50ms Error: task failed
	//  60ms Slow task cancelled
	//  60ms Error: context canceled
}
