// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskPoolOrJob represents either a TaskPool or a Job.
// When scattering directly to a Job, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// getJob returns the Job associated with this target
	getJob() *Job
	// Execute executes the task function in the target context
	scatter(context.Context, workq.GroupID, workq.Execution, time.Time, *taskPoolScatterWork, boundTask) error
}

type boundTask interface {
	Execute(ctx context.Context, group workq.GroupID, completedFn func(), taskWorkerOutboxMap *outboxMap)
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
	job      *Job
	taskFn   psgfn.Task[T]
	gatherOp GatherOp[T]

	pool *omnipool.Pool[gatherTask[T]]
}

// Binds type-specific task and gather functions together into a generic task
// function
func (pt *gatherTask[T]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerOutboxMap *outboxMap,
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
		// Post result using integrate
		ctx, meta := pt.job.ctxMeta(ctx)
		queueFn := meta.MayQueue()
		if (queueFn == nil) != meta.WouldBlock() {
			panic("meta.MayQueue() value does not match meta.WouldBlock()")
		}
		// startedOrQueued can only be false if context was canceled, so we ignore both return values
		_, _ = pt.gatherOp.integrate(ctx, meta, pt.job, pt.group, value, err, time.Time{}, queueFn)
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

func scatterNowOrQueue(
	ctx context.Context,
	meta *ctxMeta,
	deadline time.Time,
	target TaskPoolOrJob,
	scatterWork workq.Work,
	queueFn workq.QueueWorkFunc,
) (startedOrQueued bool, err error) {
	traceRegion := "scatterNowOrQueue"
	j := target.getJob()

	// Give previously scattered tasks a chance to run. Without this call, the
	// normal use case of scattering many tasks in a tight loop tends not to
	// give those tasks a chance to run until too many have been started,
	// especially in an environment with low parallelism. This is also an
	// essential component of backpressure, as other backpressure mechanisms
	// don't kick in until at least some tasks have completed and are therefore
	// waiting on combines or gathers.
	runtime.Gosched()

	executor := executorPool.Get()
	defer executorPool.Put(executor)
	ex := executor.BaseEx()

	queued := false
	defer func() {
		if ex.Started() || !queued {
			scatterWork.Free()
		}

		startedOrQueued = ex.Started() || queued
		if err == nil {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v", startedOrQueued)
		} else {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v, err=%v", startedOrQueued, err)
		}
	}()

	if meta.IsTopLevel() {
		// If there's outstanding ready-to-execute work that needs to be done by
		// this goroutine, do it before starting the new work. This is the main
		// backpressure mechanism that prevents unbounded queuing and minimizes
		// end-to-end latency while preserving sustained throughput.
		err := j.yield(ctx, deadline)
		if err != nil {
			return false, err
		}

		// Signal the work that it should block by making AddToListeners non-nil,
		// knowing at this point that it will block rather than addToListeners
		// because we're at the top level.
		ex.AddToListeners = func(*workq.Listeners) {
			panic("unexpected call to scatterNowOrQueue.ex.AddToListeners")
		}
	}

	if deadline.IsZero() || time.Now().Before(deadline) {
		err = scatterWork.Execute(ctx, ex)
	}

	if err == nil && !ex.Started() && queueFn != nil {
		queued = true
		queueFn(scatterWork)
	}

	return
}

var executorPool = omnipool.For[workq.Executor]()
