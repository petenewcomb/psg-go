// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/assert"
)

type workFuncItem struct {
	WorkItem
	workFn WorkFunc
}

func newWorkItem(workFn WorkFunc) *workFuncItem {
	wi := &workFuncItem{}
	wi.Init(workFn)
	return wi
}

func (wi *workFuncItem) Init(workFn WorkFunc) {
	wi.WorkItem.Init(NewGroupID())
	wi.workFn = workFn
}

func (wi *workFuncItem) Execute(ctx context.Context, ex Execution) error {
	return wi.workFn(ctx, ex)
}

func TestAccepted_ExecuteOne_NoWork(t *testing.T) {
	q := Accepted{}
	q.Init()

	// TryAddWorkFunc that provides no work
	addWorkFn := func(context.Context, QueueWorkFunc) error {
		// No work available
		return nil
	}

	result, err := q.TryExecuteOne(context.Background(), addWorkFn)
	assert.False(t, result)
	assert.NoError(t, err)
}

func TestAccepted_ExecuteOne_EndOfWork(t *testing.T) {
	q := Accepted{}
	q.Init()

	// TryAddWorkFunc that provides no work
	addWorkFn := func(context.Context, QueueWorkFunc) error {
		// No work available
		return ErrEndOfWork
	}

	result, err := q.TryExecuteOne(context.Background(), addWorkFn)
	assert.False(t, result)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEndOfWork)

	result, err = q.TryExecuteOne(context.Background(), nil)
	assert.False(t, result)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEndOfWork)
}

func TestAccepted_ExecuteOne_NewWork_Success(t *testing.T) {
	q := Accepted{}
	q.Init()

	executed := false
	work := newWorkItem(func(ctx context.Context, ex Execution) error {
		ex.Starting()
		executed = true
		return nil
	})

	// TryAddWorkFunc that provides one work item
	addWorkFn := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(work)
		return nil
	}

	result, _ := q.TryExecuteOne(context.Background(), addWorkFn)
	if !result {
		t.Error("Expected true when work was processed")
	}
	if !executed {
		t.Error("Expected work to be executed")
	}
}

func TestAccepted_ExecuteOne_NewWork_Deferred(t *testing.T) {
	q := Accepted{}
	q.Init()

	tryCount := 0
	executed := false
	work := newWorkItem(func(ctx context.Context, ex Execution) error {
		tryCount++
		if tryCount == 1 {
			// First try (non-blocking) - defer
			return nil
		}
		// Second try (with notification) - execute
		ex.Starting()
		executed = true
		return nil
	})

	// AddWorkFunc that provides one work item
	addWorkFn := func(
		ctx context.Context,
		queueFn QueueWorkFunc,
		waiters *rdvq.Waiters,
		confirmWaitFn func() bool,
	) (RenotifyFunc, error) {
		queueFn(work)
		return nil, nil
	}

	_ = q.ExecuteOne(context.Background(), addWorkFn) // blocking to retry deferred work
	if !executed {
		t.Error("Expected work to be executed")
	}
	if tryCount != 2 {
		t.Errorf("Expected work to be tried twice, got %d", tryCount)
	}
}

func TestAccepted_ExecuteOne_MultipleNewWork_OneSucceeds(t *testing.T) {
	q := Accepted{}
	q.Init()

	work1Executed := false
	work1 := newWorkItem(func(ctx context.Context, ex Execution) error {
		return nil // Always not ready
	})

	work2Executed := false
	work2 := newWorkItem(func(ctx context.Context, ex Execution) error {
		ex.Starting()
		work2Executed = true
		return nil // Always succeeds
	})

	work3Executed := false
	work3 := newWorkItem(func(ctx context.Context, ex Execution) error {
		work3Executed = true
		return nil // Should not be executed due to early success
	})

	// TryAddWorkFunc that provides multiple work items
	addWorkFn := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(work1)
		queueFn(work2)
		queueFn(work3)
		return nil
	}

	result, _ := q.TryExecuteOne(context.Background(), addWorkFn)
	if !result {
		t.Error("Expected true when work was processed")
	}
	if work1Executed {
		t.Error("work1 should not have been executed (it was not ready)")
	}
	if !work2Executed {
		t.Error("work2 should have been executed")
	}
	if work3Executed {
		t.Error("work3 should not have been executed (early return)")
	}
}

func TestAccepted_ExecuteOne_DeferredWork_Priority(t *testing.T) {
	q := Accepted{}
	q.Init()

	// First, add work that will become deferred
	deferredExecuted := false
	deferredWork := newWorkItem(func(ctx context.Context, ex Execution) error {
		// No call to ex.Starting(), will be deferred
		return nil
	})

	addWorkFn1 := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(deferredWork)
		return nil
	}

	result1, _ := q.TryExecuteOne(context.Background(), addWorkFn1)
	if result1 {
		t.Error("Expected false when work not ready")
	}

	// Now add new work - it should have priority over deferred work
	newWorkExecuted := false
	newWork := newWorkItem(func(ctx context.Context, ex Execution) error {
		ex.Starting()
		newWorkExecuted = true
		return nil
	})

	addWorkFn2 := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(newWork)
		return nil
	}

	result2, _ := q.TryExecuteOne(context.Background(), addWorkFn2)
	if !result2 {
		t.Error("Expected true when new work was processed")
	}
	if !newWorkExecuted {
		t.Error("Expected new work to be executed")
	}
	if deferredExecuted {
		t.Error("Deferred work should not have been executed (new work has priority)")
	}
}

func TestAccepted_ExecuteOne_NoNewWork_ProcessesDeferred(t *testing.T) {
	q := Accepted{}
	q.Init()

	// First, add work that is not ready and will become deferred
	deferredTryCount := 0
	deferredWork := newWorkItem(func(ctx context.Context, ex Execution) error {
		deferredTryCount++
		if deferredTryCount == 1 {
			// First try does not call ex.Starting(), becomes deferred
			return nil
		}
		// Second try executes
		ex.Starting()
		return nil
	})

	addWorkFn1 := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(deferredWork)
		return nil
	}

	result1, _ := q.TryExecuteOne(context.Background(), addWorkFn1)
	if result1 {
		t.Error("Expected false when work not ready")
	}

	// Now try again with no new work - should process deferred work
	addWorkFn2 := func(ctx context.Context, queueFn QueueWorkFunc) error {
		// No new work
		return nil
	}

	result2, _ := q.TryExecuteOne(context.Background(), addWorkFn2)
	if !result2 {
		t.Error("Expected true when deferred work was processed")
	}
	if deferredTryCount != 2 {
		t.Errorf("Expected deferred work to be tried twice, got %d", deferredTryCount)
	}
}

func TestAccepted_ExecuteOne_Blocking_RetriesWithNotification(t *testing.T) {
	q := Accepted{}
	q.Init()

	tryCount := 0
	notifyReceived := false
	work := newWorkItem(func(ctx context.Context, ex Execution) error {
		tryCount++
		if tryCount == 1 {
			// First try does not call ex.Starting(), becomes deferred
			return nil
		}
		if tryCount == 2 && ex.ShouldBlockOrListen() {
			// Second try with notification - still not ready
			notifyReceived = true
			return nil
		}
		// Third try - now ready
		ex.Starting()
		return nil
	})

	addWorkFn := func(
		ctx context.Context,
		queueFn QueueWorkFunc,
		waiters *rdvq.Waiters,
		confirmWaitFn func() bool,
	) (RenotifyFunc, error) {
		if tryCount == 0 {
			queueFn(work)
			return func() {}, nil
		}
		if confirmWaitFn != nil {
			confirmWaitFn()
		}
		return nil, nil // No new work on subsequent calls
	}

	_ = q.ExecuteOne(context.Background(), addWorkFn) // blocking
	if !notifyReceived {
		t.Error("Expected notification function to be called")
	}
	if tryCount < 2 {
		t.Errorf("Expected at least 2 tries, got %d", tryCount)
	}
}

func TestAccepted_ExecuteOne_ExQueueFunction(t *testing.T) {
	q := Accepted{}
	q.Init()

	var executionOrder []string

	// Work item that queues additional work
	parentWork := newWorkItem(func(ctx context.Context, ex Execution) error {
		ex.Starting()
		executionOrder = append(executionOrder, "parent")

		// Queue a child work item
		childWork := newWorkItem(func(ctx context.Context, ex Execution) error {
			ex.Starting()
			executionOrder = append(executionOrder, "child")
			return nil
		})
		ex.Queue(childWork)

		return nil
	})

	// TryAddWorkFunc that provides the parent work item
	addWorkFn := func(ctx context.Context, queueFn QueueWorkFunc) error {
		queueFn(parentWork)
		return nil
	}

	// Execute the parent work - should execute immediately
	result1, _ := q.TryExecuteOne(context.Background(), addWorkFn)
	if !result1 {
		t.Error("Expected true when parent work was processed")
	}

	// Execute again - should process the queued child work
	addWorkFn2 := func(ctx context.Context, queueFn QueueWorkFunc) error {
		// No new work
		return nil
	}

	result2, _ := q.TryExecuteOne(context.Background(), addWorkFn2)
	if !result2 {
		t.Error("Expected true when child work was processed")
	}

	// Verify execution order
	expectedOrder := []string{"parent", "child"}
	if len(executionOrder) != len(expectedOrder) {
		t.Errorf("Expected %d executions, got %d", len(expectedOrder), len(executionOrder))
	}
	for i, expected := range expectedOrder {
		if i >= len(executionOrder) || executionOrder[i] != expected {
			t.Errorf("Expected execution order %v, got %v", expectedOrder, executionOrder)
			break
		}
	}
}
