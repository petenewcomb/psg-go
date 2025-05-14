// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

func vetScatter[T any](
	ctx context.Context,
	taskPool *TaskPool,
	taskFunc TaskFunc[T],
) {
	if taskFunc == nil {
		panic("task function must be non-nil")
	}

	j := taskPool.job
	if j == nil {
		panic("task pool not bound to a job")
	}

	if includesJob(ctx, j, taskContextValueKey) {
		// Don't launch if the provided context is a task context within the
		// current job, since that may lead to deadlock.
		panic("Scatter called from within TaskFunc; move call to GatherFunc instead")
	}

	// Panic if the job is already done. This prevents tasks from being launched
	// after job completion, which would create orphaned tasks that will never
	// be gathered and could leak resources or cause unexpected behavior.
	j.panicIfDone()
}

func scatter[T any](
	ctx context.Context,
	taskPool *TaskPool,
	taskFunc TaskFunc[T],
	backpressureFunc backpressureFunc,
	postResult func(context.Context, T, error),
) (launched bool, err error) {
	j := taskPool.job
	taskCtx := newTaskContext(ctx, j)

	// Register the task with the job to make sure that any calls to gather will
	// block until the task is completed.
	j.state.IncrementTasks()

	// Bookkeeping: make sure that the job-scope count incremented above gets
	// decremented unless the launch actually happens
	defer func() {
		if !launched {
			j.state.DecrementTasks()
		}
	}()

	// Bind the task and gather functions together into a top-level function for
	// the new goroutine and hand it to the task pool to launch.
	return taskPool.launch(ctx, backpressureFunc, func(ctx context.Context) {
		// Don't launch if the job's context has been canceled by the time the
		// goroutine starts.
		if j.ctx.Err() != nil {
			return
		}

		// Don't launch if the task's context has been canceled by the time the
		// goroutine starts.
		if taskCtx.Err() != nil {
			return
		}

		// Make sure that a panic in a task function doesn't compromise the rest
		// of the job.
		var value T
		var err error = ErrTaskPanic
		defer func() {
			// Decrement the task pool's in-flight count BEFORE waiting on the gather
			// channel. This makes it safe for gatherFunc to call `Scatter` with this
			// same `TaskPool` instance without deadlock, as there is guaranteed to be at
			// least one slot available.
			taskPool.decrementInFlight()

			postResult(ctx, value, err)
		}()

		// Actually execute the task function. Since this is the top-level
		// function of a goroutine, if the task function panics the whole
		// program will terminate. The user can avoid this behavior by
		// recovering from the panic within the task function itself and then
		// returning normally with whatever results they want to pass to the
		// GatherFunc to represent the failure. We therefore do not defer
		// posting a gather to the job's channel or otherwise attempt to
		// maintain the integrity of the task pool or overall job in case of task
		// panics.
		value, err = taskFunc(taskCtx)
	})
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task pools.
// Gathering up to 2 here balances between catching up and pausing for too long
// during a scatter.
func yieldBeforeScatter(ctx context.Context, bp backpressureProvider) error {
	for range 2 {
		ok, err := bp.Yield(ctx)
		if !ok || err != nil {
			return err
		}
	}
	return nil
}

type taskContextValueType struct{}

var taskContextValueKey any = taskContextValueType{}

func newTaskContext(ctx context.Context, j *Job) context.Context {
	val := j.ctx.Value(jobContextValueKey)
	return context.WithValue(ctx, taskContextValueKey, val)
}
