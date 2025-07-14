// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskPoolOrJob represents either a TaskPool or a Job.
// When scattering directly to a Job, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// job returns the Job associated with this target
	job() *Job
	// newScatterWork creates a work item that will execute the task function in the target context
	newScatterWork(workID workq.WorkID, taskFn boundTaskFunc) workq.WorkFunc
}

type boundTaskFunc func(ctx context.Context, completedFn func(), taskWorkerOutboxMap *outboxMap)

func vetScatter[T any](
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) (context.Context, *ctxMeta) {
	if taskFn == nil {
		panic("task function must be non-nil")
	}

	// If target is nil, job() will panic directly.
	// If job() returns nil, it's a zero-value TaskPool.
	j := target.job()
	if j == nil {
		panic("task pool not bound to a job")
	}

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

func newScatterWork[T any](
	target TaskPoolOrJob,
	workID workq.WorkID,
	taskFn psgfn.Task[T],
	postResultFn func(context.Context, *outboxMap, T, error),
) workq.WorkFunc {
	traceRegion := "newScatterWork"

	// Bind the task and gather functions together into a generic task function
	boundTaskFn := func(ctx context.Context, completedFn func(), taskWorkerOutboxMap *outboxMap) {
		traceRegion := traceRegion + ".boundTaskFn"
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
		trace.WithRegion(ctx, traceRegion+".taskFn", func() {
			value, err = taskFn(ctx)
		})
	}

	// Pass to the target to wrap up as a work function
	return target.newScatterWork(workID, boundTaskFn)
}

func tryScatterNow(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWorkFn workq.WorkFunc,
) (bool, error) {
	return scatterNowOrQueue(ctx, meta, target, scatterWorkFn, nil)
}

func scatterNow(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWorkFn workq.WorkFunc,
) error {
	_, err := scatterNowOrQueue(ctx, meta, target, scatterWorkFn, meta.QueueWork)
	return err
}

func scatterNowOrQueue(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWorkFn workq.WorkFunc,
	queueFn workq.QueueWorkFunc,
) (startedOrQueued bool, err error) {
	traceRegion := "scatterNowOrQueue"

	// Give previously scattered tasks a chance to run. Without this call, the
	// normal use case of scattering many tasks in a tight loop tends not to
	// give those tasks a chance to run until they've all been started. This
	// call to runtime.GoSched not only not only smooths task startup, it allows
	// backpressure to operate more effectively, spacing out tasks to avoid
	// boom-and-bust cycles of activity more reminiscent of batch processing.
	runtime.Gosched()

	j := target.job()

	ex := workq.Execution{
		Blocking: func() {},
		Starting: func() {
			startedOrQueued = true
			trace.Logf(ctx, traceRegion+".Starting", "started=true")
		},
	}

	defer func() {
		if !startedOrQueued {
			// Notify scatterWorkFn that it has been abandoned
			err = errors.Join(err, scatterWorkFn(ctx, workq.Execution{}))
		}
		if err == nil {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v", startedOrQueued)
		} else {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v, err=%v", startedOrQueued, err)
		}
	}()

	if meta.IsTopLevel() {
		err := j.yield(ctx)
		if err != nil {
			return false, err
		}
		ex.Subscribe = func(c *workq.Coordinator) {
			panic("should be blocking, not subscribing for notification")
		}
	}

	err = scatterWorkFn(ctx, ex)

	if err == nil && !startedOrQueued && queueFn != nil {
		startedOrQueued = true
		queueFn(scatterWorkFn)
		trace.Logf(ctx, traceRegion, "scatter queued")
	}

	return
}
