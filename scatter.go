// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskPoolOrJob represents either a TaskPool or a Job.
// When scattering directly to a Job, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// getJob returns the Job associated with this target
	getJob() *Job
	// Execute executes the task function in the target context
	scatter(context.Context, workq.Execution, *taskPoolScatterWork, boundTaskFunc) error
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

// Binds type-specific task and gather functions together into a generic task
// function
func bindTaskFunc[T any](
	job *Job,
	taskFn psgfn.Task[T],
	postResultFn func(context.Context, *Job, *outboxMap, T, error),
) boundTaskFunc {
	return func(ctx context.Context, completedFn func(), taskWorkerOutboxMap *outboxMap) {
		traceRegion := "bindTaskFunc.boundTaskFn"
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
			postResultFn(ctx, job, taskWorkerOutboxMap, value, err)
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
}

func tryScatterNow(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWork workq.Work,
) (bool, error) {
	return scatterNowOrQueue(ctx, meta, target, scatterWork, nil)
}

func scatterNow(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWork workq.Work,
) error {
	_, err := scatterNowOrQueue(ctx, meta, target, scatterWork, meta.MayQueue())
	return err
}

func scatterNowOrQueue(
	ctx context.Context,
	meta *ctxMeta,
	target TaskPoolOrJob,
	scatterWork workq.Work,
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

	executor := getExecutor()
	defer putExecutor(executor)
	ex := executor.BaseEx()

	queued := false
	defer func() {
		if ex.Started() || !queued {
			scatterWork.Close()
		}

		startedOrQueued = ex.Started() || queued
		if err == nil {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v", startedOrQueued)
		} else {
			trace.Logf(ctx, traceRegion, "returning startedOrQueued=%v, err=%v", startedOrQueued, err)
		}
	}()

	if meta.IsTopLevel() {
		j := target.getJob()
		err := j.yield(ctx)
		if err != nil {
			return false, err
		}

		// Signal the work that it should block making Subscribe non-nil,
		// knowing at this point that it will block rather than subscribe
		// because we're at the top level.
		ex.Subscribe = func(*workq.Coordinator) {
			panic("unexpected call to scatterNowOrQueue.ex.Subscribe")
		}
	}

	err = scatterWork.Execute(ctx, ex)

	if err == nil && !ex.Started() && queueFn != nil {
		queued = true
		queueFn(scatterWork)
	}

	return
}

var executorPool = sync.Pool{
	New: func() any {
		return &workq.Executor{}
	},
}

func getExecutor() *workq.Executor {
	return executorPool.Get().(*workq.Executor)
}

func putExecutor(e *workq.Executor) {
	e.Reset()
	executorPool.Put(e)
}
