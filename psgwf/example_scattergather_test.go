// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"fmt"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"

	"github.com/petenewcomb/streampool/internal/exmpclk"
	"github.com/petenewcomb/streampool/psgwf"
)

// Example_scatterSkim demonstrates using workflow context to cancel
// related operations when one fails.
func Example_scatterSkim() {
	// Create a wave
	var wave streampool.Wave

	poolLimit := streampool.NewSemaphore(3)

	var clock exmpclk.ExampleClock
	clock.Start()
	msSinceStart := func() int64 {
		return clock.Elapsed(10 * time.Millisecond).Milliseconds()
	}

	// Create a skim for collecting results
	skimmer := psgwf.NewSkimmer(&wave, func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
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
	quickRunner := psgwf.NewGenericLauncher(&wave, skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			fmt.Printf("%3dms Quick task started\n", msSinceStart())
			clock.Sleep(10 * time.Millisecond)
			fmt.Printf("%3dms Quick task completed\n", msSinceStart())
			return "Quick result", nil
		}, streampool.WithLimits(poolLimit))
	err := quickRunner.Start(context.Background())
	if err != nil {
		fmt.Printf("%3dms Error starting quick task: %v\n", msSinceStart(), err)
	}

	// Sleep to ensure quick task completes and result is skimmed before starting failing task
	clock.Sleep(20 * time.Millisecond)

	// Second task fails and cancels workflow
	failingRunner := psgwf.NewGenericLauncher(&wave, skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			fmt.Printf("%3dms Failing task started\n", msSinceStart())
			clock.Sleep(30 * time.Millisecond)
			fmt.Printf("%3dms Failing task failed - cancelling workflow\n", msSinceStart())
			wf.Ctx().Cancel(fmt.Errorf("critical failure"))
			return "", fmt.Errorf("task failed")
		}, streampool.WithLimits(poolLimit))
	err = failingRunner.Start(context.Background())
	if err != nil {
		fmt.Printf("%3dms Error starting failing task: %v\n", msSinceStart(), err)
	}

	// Sleep to ensure failing task starts before slow task
	clock.Sleep(10 * time.Millisecond)

	// Third task should be cancelled
	slowRunner := psgwf.NewGenericLauncher(&wave, skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
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
		}, streampool.WithLimits(poolLimit))
	err = slowRunner.Start(context.Background())
	if err != nil {
		fmt.Printf("%3dms Error starting slow task: %v\n", msSinceStart(), err)
	}

	// Wait to ensure all tasks have been processed
	clock.Sleep(10 * time.Millisecond)

	// Skim all results
	err = wave.CloseAndSkimAll(context.Background())
	if err != nil {
		fmt.Printf("%3dms Error skimming: %v\n", msSinceStart(), err)
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
