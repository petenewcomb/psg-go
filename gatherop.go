// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// GatherOp represents an operation that executes tasks and collects their results.
//
// Thread-safety and copying: A GatherOp value is designed to be copied. While
// a single GatherOp value does not support concurrent calls to Scatter or
// TryScatter, copies of a GatherOp can be used concurrently. All copies share
// the same gather function binding. This allows GatherOp values to be safely
// passed by value to goroutines or stored in structures.
type GatherOp[T any] struct {
	gatherFn psgfn.Gather[T]
	workPool *omnipool.Pool[gatherWork[T]]
	taskPool *omnipool.Pool[gatherTask[T]]
}

func NewGatherOp[T any](
	gatherFn psgfn.Gather[T],
) GatherOp[T] {
	if gatherFn == nil {
		panic("gather function must be non-nil")
	}
	return GatherOp[T]{
		gatherFn: gatherFn,
		workPool: omnipool.For[gatherWork[T]](),
		taskPool: omnipool.For[gatherTask[T]](),
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
func (g GatherOp[T]) Scatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) error {
	traceRegion := "GatherOp.Scatter"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetScatter(ctx, target, taskFn)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := g.newScatterWork(group, time.Time{}, target, taskFn)
	return meta.ExecuteNowOrQueue(ctx, work)
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
func (g GatherOp[T]) TryScatter(
	ctx context.Context,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) (bool, error) {
	traceRegion := "GatherOp.TryScatter"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetScatter(ctx, target, taskFn)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := g.newScatterWork(group, deadline, target, taskFn)
	ok, err := meta.TryExecuteNow(ctx, deadline, work)
	if !ok {
		work.Free()
	}
	return ok, err
}

// Integrate posts values to be gathered by the gather queue.
// This follows the same pattern as Scatter but for posting gather work instead
// of launching tasks.
func (g GatherOp[T]) Integrate(
	ctx context.Context,
	target *Job,
	value T,
	err error,
) error {
	traceRegion := "GatherOp.Integrate"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.integrate(ctx, meta, target, group, value, err)
}

// TryIntegrate attempts to post values to be gathered by the gather queue.
// Like Integrate, but returns instead of blocking if queuing would be required.
func (g GatherOp[T]) TryIntegrate(
	ctx context.Context,
	deadline time.Time,
	target *Job,
	value T,
	err error,
) (bool, error) {
	traceRegion := "GatherOp.TryIntegrate"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.tryIntegrate(ctx, meta, target, group, value, err, deadline)
}

// newTask creates a new gather task that will execute the task and integrate results
func (g GatherOp[T]) newTask(group workq.GroupID, job *Job, taskFn psgfn.Task[T]) boundTask {
	pt := g.taskPool.Get()
	pt.pool = g.taskPool
	pt.group = group
	pt.job = job
	pt.taskFn = taskFn
	pt.gatherOp = g
	return pt
}

// boundGatherWork interface allows type erasure for gatherWork instances
type boundGatherWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type gatherWork[T any] struct {
	jobWork
	workq.DownstreamWork
	job      *Job
	pool     *omnipool.Pool[gatherWork[T]]
	gatherFn psgfn.Gather[T]
	value    T
	err      error
}

// newGatherWork creates a new gather work item with the provided values
func (g GatherOp[T]) newGatherWork(group workq.GroupID, job *Job, value T, err error) *gatherWork[T] {
	w := g.workPool.Get()
	w.Init(g.workPool, group, job, g.gatherFn, value, err)
	return w
}

func (w *gatherWork[T]) Init(
	pool *omnipool.Pool[gatherWork[T]],
	group workq.GroupID,
	job *Job,
	gatherFn psgfn.Gather[T],
	value T,
	err error,
) {
	w.jobWork.Init(group, job)
	w.job = job
	w.pool = pool
	w.gatherFn = gatherFn
	w.value = value
	w.err = err
}

func (w *gatherWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "gatherWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	ex.Starting()
	ctx, meta := w.job.ctxMeta(ctx)

	meta.PushGroup(w.Group())
	defer meta.PopGroup()

	return w.gatherFn(ctx, w.value, w.err)
}

//nolint:contextcheck // background context used only for tracing
func (w *gatherWork[T]) Free() {
	traceRegion := "gatherWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.DownstreamWork.Close()
	w.jobWork.Close(w.job)
	w.pool.Put(w)
}

// integrate creates gather work and posts it to the gather queue
func (g GatherOp[T]) integrate(
	ctx context.Context,
	meta *ctxMeta,
	job *Job,
	group workq.GroupID,
	value T,
	err error,
) error {
	gatherWork := g.newGatherWork(group, job, value, err)
	postWork := job.newGatherPostWork(group, gatherWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// integrate creates gather work and posts it to the gather queue
func (g GatherOp[T]) tryIntegrate(
	ctx context.Context,
	meta *ctxMeta,
	job *Job,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	gatherWork := g.newGatherWork(group, job, value, err)
	postWork := job.newGatherPostWork(group, gatherWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}

func (g GatherOp[T]) newScatterWork(
	group workq.GroupID,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) *gatherScatterWork {
	traceRegion := "GatherOp.newScatterWork"

	w := gatherScatterWorkPool.Get()
	w.Init(group, deadline, target, g.newTask(group, target.getJob(), taskFn))

	trace.Logf(context.Background(), traceRegion, "GatherOp created %v", w)
	return w
}

type gatherScatterWork struct {
	jobWork
	deadline time.Time
	target   TaskPoolOrJob
	taskPoolScatterWork
	task boundTask
}

func (w *gatherScatterWork) Init(group workq.GroupID, deadline time.Time, target TaskPoolOrJob, task boundTask) {
	w.jobWork.Init(group, target.getJob())
	w.deadline = deadline
	w.target = target
	w.task = task
}

func (w *gatherScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "gatherScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	workFn := func(ctx context.Context, ex workq.Execution) error {
		return w.target.scatter(ctx, w.Group(), ex, w.deadline, &w.taskPoolScatterWork, w.task)
	}

	defer func() {
		if ex.Started() {
			w.task = nil // we no longer own the task
		}
	}()

	j := w.target.getJob()
	bb := j.protoBB
	if bb.ShouldBlock(ctx) != nil {
		return j.governor.Execute(ctx, ex, w.deadline, bb, workFn)
	} else {
		return workFn(ctx, ex)
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *gatherScatterWork) Free() {
	traceRegion := "gatherScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	if w.task != nil {
		w.task.Free()
	}
	w.Close(w.target.getJob())
	gatherScatterWorkPool.Put(w)
}

var gatherScatterWorkPool = omnipool.For[gatherScatterWork]()
