// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskPoolOrJob represents either a TaskPool or a Pool.
// When scattering directly to a Pool, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// getJob returns the Pool associated with this target
	getJob() *Pool
	// newScatterWork creates work for scattering a task
	newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work
}

type boundTask interface {
	Execute(ctx context.Context, group workq.GroupID, completedFn func(), taskWorkerSender *rdvq.Sender)
	Free()
}

func vetScatter[T any](
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) (context.Context, *ctxMeta) {
	if taskFn == nil {
		panic("task function must be non-nil")
	}

	j := target.getJob()

	ctx, meta := j.topLevelCtxMeta(ctx, func(ctxType contextType) {
		switch ctxType {
		case topLevelContext, gatherContext, combineContext:
		// These are valid for scattering
		default:
			panic(fmt.Sprintf(
				"Scatter called from %v context but allowed only by top-level, gather, or combine context",
				ctxType))
		}
	})

	// Panic if the job is already done. This prevents tasks from being launched
	// after job completion, which would create orphaned tasks that will never
	// be gathered and could leak resources or cause unexpected behavior.
	j.panicIfDone()

	return ctx, meta
}

type gatherTask[T any] struct {
	group    workq.GroupID
	job      *Pool
	taskFn   psgfn.Task[T]
	gatherer Gatherer[T]

	pool *omnipool.Pool[gatherTask[T]]
}

// Binds type-specific task and gather functions together into a generic task
// function
func (pt *gatherTask[T]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	traceRegion := "gatherTask.execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Make sure that a panic in a task function doesn't compromise the rest
	// of the job.
	var value T
	var err error = ErrTaskPanicked
	defer func() {
		if completedFn != nil {
			completedFn()
		}
		if err != nil {
			trace.Logf(ctx, traceRegion, "posting task err=%v", err)
		}
		ctx, meta := pt.job.ctxMeta(ctx)
		intErr := pt.gatherer.integrate(ctx, meta, pt.job, pt.group, value, err)
		if intErr != nil && ctx.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
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
	trace.WithRegion(ctx, traceRegion+".taskFn", func() {
		value, err = pt.taskFn(ctx)
	})
}

// Free implements boundTask interface
func (pt *gatherTask[T]) Free() {
	pt.pool.Put(pt)
}
