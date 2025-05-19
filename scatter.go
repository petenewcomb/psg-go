// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

// TaskPoolOrJob represents either a TaskPool or a Job.
// When scattering directly to a Job, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// job returns the Job associated with this target
	job() *Job
	// launch executes a task, potentially waiting if concurrency limits are reached
	launch(ctx context.Context, backpressureFn backpressureFunc, taskFn boundTaskFunc) (launched bool, err error)
	// withBackpressureProvider returns a context with the appropriate backpressure provider
	withBackpressureProvider(ctx context.Context) context.Context
}

func vetScatter[T any](
	ctx context.Context,
	target TaskPoolOrJob,
	taskFunc TaskFunc[T],
) {
	if taskFunc == nil {
		panic("task function must be non-nil")
	}

	// If target is nil, job() will panic directly.
	// If job() returns nil, it's a zero-value TaskPool.
	j := target.job()
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
	target TaskPoolOrJob,
	taskFunc TaskFunc[T],
	backpressureFunc backpressureFunc,
	postResult func(context.Context, T, error),
) (launched bool, err error) {
	// Don't launch if the task's context has been canceled by the time the
	// goroutine starts.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	j := target.job()

	// Don't launch if the job's context has been canceled by the time the
	// goroutine starts.
	if err := j.ctx.Err(); err != nil {
		return false, err
	}

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
	// the new goroutine and hand it to the target to launch.
	return target.launch(ctx, backpressureFunc, func(ctx context.Context, taskCompletedFn func()) {
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
		var err error = ErrTaskPanicked
		defer func() {
			if taskCompletedFn != nil {
				taskCompletedFn()
			}
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

type taskContextValueKeyType struct{}

var taskContextValueKey any = taskContextValueKeyType{}

func newTaskContext(ctx context.Context, j *Job) context.Context {
	taskCtx := j.ctx
	taskCtx = context.WithValue(taskCtx,
		taskContextValueKey,
		j.ctx.Value(jobContextValueKey))
	taskCtx = context.WithValue(taskCtx,
		backpressureProviderContextValueKey,
		ctx.Value(backpressureProviderContextValueKey))
	return taskCtx
}
