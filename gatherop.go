// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/workq"
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
	g := &GatherOp[T]{}
	g.gatherFn = func(ctx context.Context, value T, err error) error {
		traceRegion := "GatherOp.gatherFn"
		defer trace.StartRegion(ctx, traceRegion).End()
		err = gatherFn(ctx, value, err)
		trace.Logf(ctx, traceRegion, "GatherOp=%p returned err=%v", g, err)
		return err
	}
	return g
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
	traceRegion := "GatherOp.Scatter"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "GatherOp=%p", g)

	ctx, meta := vetScatter(ctx, target, taskFn)
	workFn := g.newScatterWork(target, taskFn)
	return scatterNow(ctx, meta, target.job(), workFn)
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
	traceRegion := "GatherOp.TryScatter"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "GatherOp=%p", g)

	ctx, meta := vetScatter(ctx, target, taskFn)
	workFn := g.newScatterWork(target, taskFn)
	return tryScatterNow(ctx, meta, target, workFn)
}

func (g *GatherOp[T]) newScatterWork(
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) workq.WorkFunc {
	traceRegion := "GatherOp.newScatterWork"

	workID := workq.NewWorkID()
	trace.Logf(context.Background(), traceRegion, "workID=%d", workID)

	j := target.job()

	postResultFn := func(ctx context.Context, taskWorkerOutboxMap *outboxMap, value T, err error) {
		traceRegion := traceRegion + ".postResultFn"
		defer trace.StartRegion(ctx, traceRegion).End()

		// Post the gather using the task worker's outbox for the job's gather queue
		gatherOutbox := OutboxFor[workq.WorkFunc](taskWorkerOutboxMap, j.gatherOutboxKey())
		trace.Logf(ctx, traceRegion, "outbox=%p, workID=%d", gatherOutbox, workID)

		// Bind the supplied gatherFn to the result.
		boundGatherFn := func(ctx context.Context) error {
			traceRegion := traceRegion + ".boundGatherFn"
			defer trace.StartRegion(ctx, traceRegion).End()
			trace.Logf(ctx, traceRegion, "workID=%d", workID)

			return g.gatherFn(ctx, value, err)
		}

		j.postGather(ctx, gatherOutbox, boundGatherFn)
	}

	scatterWorkFn := newScatterWork(target, workID, taskFn, postResultFn)

	return j.newWork(workID, scatterWorkFn)
}
