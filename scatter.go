// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskPoolOrJob represents either a TaskPool or a Job.
// When scattering directly to a Job, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// job returns the Job associated with this target
	job() *Job
	// launch executes a task, potentially waiting if concurrency limits are reached
	launch(ctx context.Context, backpressureFn backpressureFunc, taskFn boundTask) (launched bool, err error)
	// withBackpressureProvider returns a context with the appropriate backpressure provider
	withBackpressureProvider(ctx context.Context) context.Context
}

type boundTask func(ctx context.Context, completedFn func(), ctxWithBP func(backpressureProvider) context.Context, taskWorkerOutboxMap *outboxMap)

func vetScatter[T any](
	vetted vettedContext,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) {
	if taskFn == nil {
		panic("task function must be non-nil")
	}

	// If target is nil, job() will panic directly.
	// If job() returns nil, it's a zero-value TaskPool.
	j := target.job()
	if j == nil {
		panic("task pool not bound to a job")
	}

	if vetted.hasTaskValue {
		// Don't launch if the provided context is a task context within the
		// current job, since that may lead to deadlock.
		panic("Scatter called from within Task; move call to Gather instead")
	}

	// Panic if the job is already done. This prevents tasks from being launched
	// after job completion, which would create orphaned tasks that will never
	// be gathered and could leak resources or cause unexpected behavior.
	j.panicIfDone()
}

func scatter[T any](
	vettedCtx vettedContext,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
	applyBackpressure backpressureFunc,
	postResultFn func(context.Context, *outboxMap, T, error),
) (launched bool, err error) {
	j := target.job()

	bp := getBackpressureProvider(vettedCtx.ctx, j)

	// If the job is too busy, we should wait to scatter the task.
	for {
		busy, busyChangeCh := j.gcMonitor.BusySignal()
		if !busy {
			break
		}

		if applyBackpressure == nil {
			return false, nil
		}

		_, err = applyBackpressure(vettedCtx.ctx, rdvq.Waiter{}, busyChangeCh)
		if err != nil {
			return false, err
		}
	}

	// Register the task with the job to make sure that any calls to gather will
	// block until the task is completed.
	j.state.IncrementWork()

	// Bookkeeping: make sure that the job-scope count incremented above gets
	// decremented unless the launch actually happens
	defer func() {
		if !launched {
			j.state.DecrementWork()
		}
	}()

	// Bind the task and gather functions together into a top-level function for
	// the new goroutine and hand it to the target to launch.
	launched, err = target.launch(vettedCtx.ctx, applyBackpressure, func(ctx context.Context, taskCompletedFn func(), ctxWithBPFn func(backpressureProvider) context.Context, taskWorkerOutboxMap *outboxMap) {
		// Make sure that a panic in a task function doesn't compromise the rest
		// of the job.
		var value T
		var err error = ErrTaskPanicked
		defer func() {
			if taskCompletedFn != nil {
				taskCompletedFn()
			}
			ctx = ctxWithBPFn(bp)
			postResultFn(ctx, taskWorkerOutboxMap, value, err)
		}()

		// Actually execute the task function. Since this is the top-level
		// function of a goroutine, if the task function panics the whole
		// program will terminate. The user can avoid this behavior by
		// recovering from the panic within the task function itself and then
		// returning normally with whatever results they want to pass to the
		// Gather to represent the failure. We therefore do not defer
		// posting a gather to the job's channel or otherwise attempt to
		// maintain the integrity of the task pool or overall job in case of task
		// panics.
		value, err = taskFn(ctx)
	})
	return launched, err
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task pools.
// Gathering up to 2 here balances between catching up and pausing for too long
// during a scatter.
func yieldBeforeScatter(vetted vettedContext, bp backpressureProvider) error {
	for range 2 {
		ok, err := bp.Yield(vetted)
		if !ok || err != nil {
			return err
		}
	}
	return nil
}
