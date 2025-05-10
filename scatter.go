// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

func vetScatter[T any](
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
) {
	if taskFunc == nil {
		panic("task function must be non-nil")
	}

	j := pool.job
	if j == nil {
		panic("pool not bound to a job")
	}

	if includesJob(ctx, j) {
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
	pool *Pool,
	taskFunc TaskFunc[T],
	backpressureFunc backpressureFunc,
	postResult func(T, error),
) (launched bool, err error) {
	j := pool.job

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
	// the new goroutine and hand it to the pool to launch.
	return pool.launch(ctx, backpressureFunc, func(ctx context.Context) {
		// Don't launch if the context has been canceled by the time the
		// goroutine starts.
		if ctx.Err() != nil {
			return
		}

		// Actually execute the task function. Since this is the top-level
		// function of a goroutine, if the task function panics the whole
		// program will terminate. The user can avoid this behavior by
		// recovering from the panic within the task function itself and then
		// returning normally with whatever results they want to pass to the
		// GatherFunc to represent the failure. We therefore do not defer
		// posting a gather to the job's channel or otherwise attempt to
		// maintain the integrity of the pool or overall job in case of task
		// panics.
		value, err := taskFunc(ctx)

		// Decrement the pool's in-flight count BEFORE waiting on the gather
		// channel. This makes it safe for gatherFunc to call `Scatter` with this
		// same `Pool` instance without deadlock, as there is guaranteed to be at
		// least one slot available.
		pool.decrementInFlight()

		postResult(value, err)
	})
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited pools.
// Gathering up to 2 here balances between catching up and pausing for too long
// during a scatter.
func yieldBeforeScatter(ctx, ctx2 context.Context, bp backpressureProvider) error {
	for range 2 {
		ok, err := bp.Yield(ctx, ctx2)
		if !ok || err != nil {
			return err
		}
	}
	return nil
}
