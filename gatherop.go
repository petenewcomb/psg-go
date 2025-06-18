// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/psgfn"
)

type GatherOp[T any] struct {
	gatherFn psgfn.Gather[T]
}

func NewGatherOp[T any](
	gatherFn psgfn.Gather[T],
) *GatherOp[T] {
	if gatherFn == nil {
		panic("gather function must be non-nil")
	}
	return &GatherOp[T]{
		gatherFn: gatherFn,
	}
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// passed to the GatherOp within a subsequent call to Scatter or any of the
// gathering methods of [Job] (i.e., [Job.Gather], [Job.TryGather],
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
// WARNING: Scatter must not be called from within a Task launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated Gather after the Task
// completes.
//
// Scatter will panic if the given task pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the GatherOp's gather function.
//
// See [Task] and [Gather] for important caveats and additional detail.
func (g *GatherOp[T]) Scatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) error {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFn)

	doScatter := func(vettedCtx vettedContext) error {
		launched, err := g.scatter(vettedCtx, j, target, true, taskFn)
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
func (g *GatherOp[T]) TryScatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) (bool, error) {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFn)

	if !vettedCtx.inGather {
		if err := j.processOutstandingWork(ctx); err != nil {
			return false, err
		}
	}

	return g.scatter(vettedCtx, j, target, false, taskFn)
}

func (g *GatherOp[T]) scatter(
	vettedCtx vettedContext,
	j *Job,
	target TaskPoolOrJob,
	block bool,
	taskFn psgfn.Task[T],
) (bool, error) {
	bp := getBackpressureProvider(vettedCtx.ctx, j)

	if err := yieldBeforeScatter(vettedCtx, bp); err != nil {
		return false, err
	}

	var bpf backpressureFunc
	if block {
		bpf = bp.Block
	}

	return scatter(vettedCtx, target, taskFn, bpf, func(ctx context.Context, value T, err error) {
		// Build the gather function, binding the supplied gatherFn to the
		// result.
		gatherFn := func(ctx context.Context) error {
			return g.gatherFn(ctx, value, err)
		}

		// Post the gather using the idle worker queue optimization
		j.postGather(ctx, gatherFn)
	})
}
