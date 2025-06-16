// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

// A GatherFunc is a function that processes the result of a completed
// [TaskFunc]. It receives the result and error values from the [TaskFunc]
// execution, allowing it to handle both successful and failed task executions.
//
// The GatherFunc is called when completed task results are processed by
// [Scatter], [Job.GatherOne], [Job.TryGatherOne], [Job.GatherAll], or
// [Job.TryGatherAll]. Execution of a GatherFunc will block processing of
// subsequent task results, adding to backpressure. If such backpressure is
// undesirable, consider launching expensive gathering logic in another
// asynchronous task using [Scatter]. Unlike [TaskFunc], it is safe to call
// [Scatter] from within a GatherFunc.
//
// If multiple goroutines may call [Scatter], [Job.GatherOne],
// [Job.TryGatherOne], [Job.GatherAll], or [Job.TryGatherAll] concurrently, then
// every GatherFunc used in the job must be thread-safe.
type GatherFunc[T any] = func(context.Context, T, error) error

type Gather[T any] struct {
	gatherFunc GatherFunc[T]
}

func NewGather[T any](
	gatherFunc GatherFunc[T],
) *Gather[T] {
	if gatherFunc == nil {
		panic("gather function must be non-nil")
	}
	return &Gather[T]{
		gatherFunc: gatherFunc,
	}
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// passed to the Gather within a subsequent call to Scatter or any of the
// gathering methods of [Job] (i.e., [Job.GatherOne], [Job.TryGatherOne],
// [Job.GatherAll], or [Job.TryGatherAll]).
//
// Before launching a task, Scatter applies backpressure by gathering some
// already-completed tasks. This happens regardless of concurrency limits and
// helps maintain smooth execution flow. If a TaskPool is used, Scatter may also
// block to ensure compliance with the concurrency limit, gathering additional
// tasks until a slot becomes available. When scattering directly to a Job,
// tasks are not subject to any concurrency limit. The context passed to Scatter
// may be used to cancel (e.g., with a timeout) both gathering and launch, but
// only the context associated with the task's job will be passed to the task.
//
// WARNING: Scatter must not be called from within a TaskFunc launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated GatherFunc after the TaskFunc
// completes.
//
// Scatter will panic if the given task pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the Gather's gather function.
//
// See [TaskFunc] and [GatherFunc] for important caveats and additional detail.
func (g *Gather[T]) Scatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFunc TaskFunc[T],
) error {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFunc)

	doScatter := func(vettedCtx vettedContext) error {
		launched, err := g.scatter(vettedCtx, j, target, true, taskFunc)
		if !launched && err == nil {
			panic("task function was not launched, but no error was returned")
		}
		return err
	}

	if vettedCtx.inGather {
		// Make sure the job doesn't shut down until this scatter has been done.
		j.state.IncrementWork()
		bp := getBackpressureProvider(vettedCtx.ctx, j)
		bp.QueueWork(func(ctx context.Context) error {
			defer j.state.DecrementWork()
			vettedCtx := j.vettedContext(ctx)
			return doScatter(vettedCtx)
		})
		return nil
	}

	ctx = j.gatherContext(vettedCtx)

	if err := j.processOutstandingWork(ctx); err != nil {
		return err
	}

	return doScatter(vettedCtx)
}

// TryScatter attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Scatter]. Like Scatter, it applies initial
// backpressure by gathering some already-completed tasks. Unlike Scatter,
// TryScatter will return instead of blocking if the given target is a TaskPool
// that is already at its concurrency limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// a TaskPool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Scatter for more detail about how scattering works.
func (g *Gather[T]) TryScatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFunc TaskFunc[T],
) (bool, error) {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFunc)

	if !vettedCtx.inGather {
		if err := j.processOutstandingWork(ctx); err != nil {
			return false, err
		}
	}

	return g.scatter(vettedCtx, j, target, false, taskFunc)
}

func (g *Gather[T]) scatter(
	vettedCtx vettedContext,
	j *Job,
	target TaskPoolOrJob,
	block bool,
	taskFunc TaskFunc[T],
) (bool, error) {
	bp := getBackpressureProvider(vettedCtx.ctx, j)

	if err := yieldBeforeScatter(vettedCtx, bp); err != nil {
		return false, err
	}

	var bpf backpressureFunc
	if block {
		bpf = bp.Block
	}

	return scatter(vettedCtx, target, taskFunc, bpf, func(ctx context.Context, value T, err error) {
		// Build the gather function, binding the supplied gatherFunc to the
		// result.
		gather := func(ctx context.Context) error {
			return g.gatherFunc(ctx, value, err)
		}

		// Post the gather using the idle worker queue optimization
		j.postGather(ctx, gather)
	})
}
