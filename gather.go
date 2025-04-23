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
// Scatter blocks to delay launch as needed to ensure compliance with the
// concurrency limit for the given pool. This backpressure is applied by
// gathering other tasks in the job until the a slot becomes available. The
// context passed to Scatter may be used to cancel (e.g., with a timeout) both
// gathering and launch, but only the context associated with the pool's job
// will be passed to the task.
//
// WARNING: Scatter must not be called from within a TaskFunc launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated GatherFunc after the TaskFunc
// completes.
//
// Scatter will panic if the given pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the Gather's gather function.
//
// See [TaskFunc] and [GatherFunc] for important caveats and additional detail.
func (g *Gather[T]) Scatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
) error {
	_, err := g.scatter(ctx, pool, taskFunc, backpressureGather)
	return err
}

// TryScatter attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Scatter]. Unlike Scatter, TryScatter will
// return instead of blocking if the given pool is already at its concurrency
// limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// the pool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Scatter for more detail about how scattering works.
func (g *Gather[T]) TryScatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
) (bool, error) {
	return g.scatter(ctx, pool, taskFunc, backpressureDecline)
}

func (g *Gather[T]) scatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
	mode backpressureMode,
) (bool, error) {
	j := pool.job
	return scatter(ctx, pool, taskFunc, mode, func(value T, err error) {
		// Build the gather function, binding the supplied gatherFunc to the
		// result.
		gather := func(ctx context.Context) error {
			return g.gatherFunc(ctx, value, err)
		}

		// Post the gather to the job's gather channel.
		select {
		case j.gatherChannel <- gather:
		case <-j.ctx.Done():
		}
	})
}
