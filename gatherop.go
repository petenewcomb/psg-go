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

// Gatherer represents an operation that executes tasks and collects their results.
//
// Thread-safety and copying: A Gatherer value is designed to be copied. While
// a single Gatherer value does not support concurrent calls to Start or
// TryStart, copies of a Gatherer can be used concurrently. All copies share
// the same gather function binding. This allows Gatherer values to be safely
// passed by value to goroutines or stored in structures.
type Gatherer[T any] struct {
	gatherFn psgfn.Gather[T]
	workPool *omnipool.Pool[gatherWork[T]]
	taskPool *omnipool.Pool[gatherTask[T]]
}

func NewGatherer[T any](
	gatherFn psgfn.Gather[T],
) Gatherer[T] {
	if gatherFn == nil {
		panic("gather function must be non-nil")
	}
	return Gatherer[T]{
		gatherFn: gatherFn,
		workPool: omnipool.For[gatherWork[T]](),
		taskPool: omnipool.For[gatherTask[T]](),
	}
}

// Start initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// passed to the Gatherer within a subsequent call to Start or any of the
// gathering methods of [Pool] (i.e., [Pool.Gather], [Pool.TryGather],
// [Pool.GatherAll], or [Pool.TryGatherAll]).
//
// Before launching a task, Start applies backpressure by gathering some
// already-completed tasks. This happens regardless of concurrency limits and
// helps maintain smooth execution flow. If a TaskPool is used, Start may also
// block to ensure compliance with the concurrency limit, gathering additional
// tasks until a slot becomes available. When scattering directly to a Pool,
// tasks are not subject to any concurrency limit. The context passed to Start
// may be used to cancel (e.g., with a timeout) both gathering and launch, but
// only the context associated with the task's job will be passed to the task.
//
// WARNING: Start must not be called from within a Task launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Start from the associated Gather after the Task
// completes.
//
// Start will panic if the given task pool is not yet associated with a job.
// Start returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the Gatherer's gather function.
//
// See [Task] and [Gather] for important caveats and additional detail.
func (g Gatherer[T]) Start(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) error {
	traceRegion := "Gatherer.Start"
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

// TryStart attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Start]. Like Start, it applies initial
// backpressure by gathering some already-completed tasks. Unlike Start,
// TryStart will return instead of blocking if the given target is a TaskPool
// that is already at its concurrency limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// a TaskPool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Start for more detail about how scattering works.
func (g Gatherer[T]) TryStart(
	ctx context.Context,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) (bool, error) {
	traceRegion := "Gatherer.TryStart"
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
// This follows the same pattern as Start but for posting gather work instead
// of launching tasks.
func (g Gatherer[T]) Integrate(
	ctx context.Context,
	target *Pool,
	value T,
	err error,
) error {
	traceRegion := "Gatherer.Integrate"
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
func (g Gatherer[T]) TryIntegrate(
	ctx context.Context,
	deadline time.Time,
	target *Pool,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Gatherer.TryIntegrate"
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
func (g Gatherer[T]) newTask(group workq.GroupID, job *Pool, taskFn psgfn.Task[T]) boundTask {
	pt := g.taskPool.Get()
	pt.pool = g.taskPool
	pt.group = group
	pt.job = job
	pt.taskFn = taskFn
	pt.gatherer = g
	return pt
}

// boundGatherWork interface allows type erasure for gatherWork instances
type boundGatherWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type gatherWork[T any] struct {
	poolWork
	workq.DownstreamWork
	job      *Pool
	pool     *omnipool.Pool[gatherWork[T]]
	gatherFn psgfn.Gather[T]
	value    T
	err      error
}

// newGatherWork creates a new gather work item with the provided values
func (g Gatherer[T]) newGatherWork(group workq.GroupID, job *Pool, value T, err error) *gatherWork[T] {
	w := g.workPool.Get()
	w.Init(g.workPool, group, job, g.gatherFn, value, err)
	return w
}

func (w *gatherWork[T]) Init(
	pool *omnipool.Pool[gatherWork[T]],
	group workq.GroupID,
	job *Pool,
	gatherFn psgfn.Gather[T],
	value T,
	err error,
) {
	w.poolWork.Init(group, job)
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
	w.poolWork.Close(w.job)
	w.pool.Put(w)
}

// integrate creates gather work and posts it to the gather queue
func (g Gatherer[T]) integrate(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
	group workq.GroupID,
	value T,
	err error,
) error {
	gatherWork := g.newGatherWork(group, job, value, err)
	postWork := job.newGatherPostWork(group, gatherWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// integrate creates gather work and posts it to the gather queue
func (g Gatherer[T]) tryIntegrate(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
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

func (g Gatherer[T]) newScatterWork(
	group workq.GroupID,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[T],
) *gatherScatterWork {
	traceRegion := "Gatherer.newScatterWork"

	targetScatterWork := target.newScatterWork(group, deadline, g.newTask(group, target.getJob(), taskFn))
	w := newGatherScatterWork(target.getJob(), group, deadline, targetScatterWork)

	trace.Logf(context.Background(), traceRegion, "Gatherer created %v", w)
	return w
}

type gatherScatterWork struct {
	workq.Work
	job      *Pool
	deadline time.Time
}

func newGatherScatterWork(
	job *Pool,
	group workq.GroupID,
	deadline time.Time,
	targetScatterWork workq.Work,
) *gatherScatterWork {
	w := gatherScatterWorkPool.Get()
	w.Work = targetScatterWork
	w.job = job
	w.deadline = deadline
	return w
}

func (w *gatherScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "gatherScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	workFn := w.Work.Execute
	bb := w.job.protoBB
	if bb.ShouldBlock(ctx) != nil {
		return w.job.governor.Execute(ctx, ex, w.deadline, bb, workFn)
	}
	return workFn(ctx, ex)
}

//nolint:contextcheck // background context used only for tracing
func (w *gatherScatterWork) Free() {
	traceRegion := "gatherScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Work.Free()
	gatherScatterWorkPool.Put(w)
}

var gatherScatterWorkPool = omnipool.For[gatherScatterWork]()
