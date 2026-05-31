// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf_test

import (
	"context"
	"sync"
	"testing"

	psg "github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgwf"
	"github.com/stretchr/testify/assert"
)

// TestWorkflowAfterFunc verifies that AfterFuncs are called after workflow completion
func TestWorkflowAfterFunc(t *testing.T) {
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()
	_ = ctx

	// Track which AfterFuncs were called
	var called sync.Map
	var callOrder []int
	var mu sync.Mutex

	// Create workflow with multiple AfterFuncs
	wf := psgwf.New(context.Background())

	// Register multiple AfterFuncs
	for i := 0; i < 5; i++ {
		i := i // Capture loop variable
		wf = psgwf.WithAfterFunc(wf, func(ctx context.Context, wf *psgwf.Workflow) {
			called.Store(i, true)
			mu.Lock()
			callOrder = append(callOrder, i)
			mu.Unlock()

			// Verify workflow context is cancelled
			select {
			case <-wf.Ctx().Done():
				t.Errorf("AfterFunc %d: workflow context should not be cancelled", i)
			default:
				// Good - context should not be cancelled
			}
		})
	}

	// Create a simple task to ensure workflow is used
	poolLimit := psg.NewSemaphore(1)
	skimmer := psgwf.NewSkimmer(func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
		return nil
	})

	runner := psgwf.NewGenericTaskRunner(skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			return "test", nil
		}, psg.WithLimits(poolLimit))
	err := runner.Start(context.Background(), wave)
	assert.NoError(t, err)

	// Close wave and skim all results
	// During skimming, the final Unref will trigger AfterFuncs
	err = wave.CloseAndSkimAll(context.Background())
	assert.NoError(t, err)

	// Verify all AfterFuncs were called
	for i := 0; i < 5; i++ {
		if _, ok := called.Load(i); !ok {
			t.Errorf("AfterFunc %d was not called", i)
		}
	}

	// Verify randomization (order should not be sequential)
	isSequential := true
	mu.Lock()
	for i := 1; i < len(callOrder); i++ {
		if callOrder[i] != callOrder[i-1]+1 {
			isSequential = false
			break
		}
	}
	mu.Unlock()

	// With 5 functions, there's only a 1/120 chance they execute in order
	// So if they're sequential, it's very likely our randomization isn't working
	if isSequential && len(callOrder) == 5 {
		t.Logf("Warning: AfterFuncs executed in sequential order %v - randomization might not be working", callOrder)
	}
}

// TestWorkflowAfterFuncWithNewTasks verifies AfterFuncs can scatter new tasks
func TestWorkflowAfterFuncWithNewTasks(t *testing.T) {
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()
	_ = ctx
	poolLimit := psg.NewSemaphore(2)

	// Track execution
	var afterFuncRan, newTaskRan bool
	var mu sync.Mutex

	// Create workflow
	wf := psgwf.New(context.Background())

	// Register AfterFunc that scatters a new task
	wf = psgwf.WithAfterFunc(wf, func(ctx context.Context, wf *psgwf.Workflow) {
		mu.Lock()
		afterFuncRan = true
		mu.Unlock()

		// Create new workflow for new tasks
		newWf := psgwf.New(ctx)

		skimmer := psgwf.NewSkimmer(func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
			mu.Lock()
			newTaskRan = true
			mu.Unlock()
			return nil
		})

		// Start a new task from within the AfterFunc
		runner := psgwf.NewGenericTaskRunner(skimmer, newWf,
			func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
				return "new task", nil
			}, psg.WithLimits(poolLimit))
		err := runner.Start(ctx, wave)
		assert.NoError(t, err)
	})

	// Run a simple task to use the workflow
	skimmer := psgwf.NewSkimmer(func(ctx context.Context, wf *psgwf.Workflow, msg string, err error) error {
		return nil
	})

	outerRunner := psgwf.NewGenericTaskRunner(skimmer, wf,
		func(ctx context.Context, wf *psgwf.Workflow) (string, error) {
			return "original task", nil
		}, psg.WithLimits(poolLimit))
	err := outerRunner.Start(context.Background(), wave)
	assert.NoError(t, err)

	// Close wave and skim all results
	// During skimming, the final Unref will trigger AfterFuncs
	// The AfterFunc will scatter new tasks that will also be skimmed
	err = wave.CloseAndSkimAll(context.Background())
	assert.NoError(t, err)

	// Verify both AfterFunc and new task ran
	mu.Lock()
	defer mu.Unlock()
	if !afterFuncRan {
		t.Error("AfterFunc did not run")
	}
	if !newTaskRan {
		t.Error("New task scattered from AfterFunc did not run")
	}
}
