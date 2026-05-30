// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// TaskRunner0 dispatches a no-argument [psgfn.Task0] onto its Pool's
// worker pool. Each call to [TaskRunner0.Start] (or
// [TaskRunner0.TryStart]) launches one Run invocation. Result delivery
// is the task body's responsibility — Run calls Submit on whatever
// downstream sinks it captures. If Run returns a non-nil error, the
// framework routes it through an internal sink so it surfaces via the
// owning Pool's [Pool.GatherAll].
//
// Concurrency limiting: pass [WithLimits] at construction time to bind
// a [Limiter] (e.g. via [NewSemaphore]) that caps the number of
// in-flight dispatches.
//
// Thread-safety and copying: a TaskRunner value is designed to be
// copied. All copies share the same binding to Pool, task, limiter
// (if any), and internal error sink, so they can be passed by value or
// stored in structures and used concurrently.
type TaskRunner0 struct {
	pool     *Pool
	task     psgfn.Task0
	limiter  Limiter
	errSink  Gatherer[struct{}]
	workPool *omnipool.Pool[taskRunnerWork0]
}

// NewTaskRunner0 binds a [psgfn.Task0] to a [Pool] and returns a
// [TaskRunner0]. Pass [WithLimits] in opts to bind one or more
// [Limiter]s that throttle dispatch. The framework manages an internal
// error sink that surfaces unexpected errors returned by Task.Run
// through the Pool's GatherAll path.
func NewTaskRunner0(pool *Pool, task psgfn.Task0, opts ...OpOption) TaskRunner0 {
	if pool == nil {
		panic("pool must be non-nil")
	}
	if task == nil {
		panic("task must be non-nil")
	}
	cfg := resolveOpConfig(opts)
	return TaskRunner0{
		pool:     pool,
		task:     task,
		limiter:  cfg.singleLimiter(),
		errSink:  newTaskErrSink(),
		workPool: omnipool.For[taskRunnerWork0](),
	}
}

// Start launches the task on a worker goroutine. Before launching, Start
// applies backpressure by gathering some already-completed tasks. If the
// target is a [TaskPool] at its concurrency limit, Start blocks until a
// slot becomes available. The ctx may be used to cancel both gathering
// and launch; only the ctx associated with the task's Pool is passed
// to Run.
//
// Returns a non-nil error if the ctx is canceled or if a gather function
// returns an error. If the returned error is non-nil, the task will not
// have been launched.
//
// WARNING: Start must not be called from inside a Task launched on the
// same Pool, since this can deadlock when a concurrency limit is
// reached. Call Start from an associated Gather or Accumulate body
// instead. Start attempts to detect this and panics, but the detection
// works only when the ctx passed to Start descends from the ctx passed
// to Run.
//
//nolint:contextcheck // background context used only for tracing
func (r TaskRunner0) Start(ctx context.Context) error {
	traceRegion := "TaskRunner0.Start"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, time.Time{})
	return meta.ExecuteNowOrQueue(ctx, work)
}

// TryStart attempts to launch the task without blocking past deadline.
// Returns (true, nil) on success, (false, nil) if a [Limiter] held the
// dispatch back and the deadline expired before a permit became
// available, or (false, non-nil) for any other failure.
//
//nolint:contextcheck // background context used only for tracing
func (r TaskRunner0) TryStart(ctx context.Context, deadline time.Time) (bool, error) {
	traceRegion := "TaskRunner0.TryStart"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, deadline)
	ok, err := meta.TryExecuteNow(ctx, deadline, work)
	if !ok {
		work.Free()
	}
	return ok, err
}

func (r TaskRunner0) newScatterWork(group workq.GroupID, deadline time.Time) *taskRunnerScatterWork {
	inner := r.newTask(group)
	taskWork := r.pool.newTaskWork(group, inner, limiterCompletedFn(r.limiter))
	postWork := r.pool.newTaskPostWork(group, deadline, taskWork)
	gated := postWork
	if r.limiter.impl != nil {
		gated = newLimiterScatterWork(r.pool, deadline, gated, r.limiter)
	}
	return newTaskRunnerScatterWork(r.pool, deadline, gated)
}

func (r TaskRunner0) newTask(group workq.GroupID) boundTask {
	w := r.workPool.Get()
	w.pool = r.workPool
	w.job = r.pool
	w.group = group
	w.task = r.task
	w.errSink = r.errSink
	return w
}

type taskRunnerWork0 struct {
	pool    *omnipool.Pool[taskRunnerWork0]
	job     *Pool
	group   workq.GroupID
	task    psgfn.Task0
	errSink Gatherer[struct{}]
}

func (w *taskRunnerWork0) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	_ = group
	_ = taskWorkerSender
	traceRegion := "taskRunnerWork0.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var err error = ErrTaskPanicked
	defer func() {
		if completedFn != nil {
			completedFn()
		}
		if err == nil {
			return
		}
		trace.Logf(ctx, traceRegion, "routing task err=%v to errSink", err)
		ctx2, meta := w.job.ctxMeta(ctx)
		intErr := w.errSink.submit(ctx2, meta, w.job, w.group, struct{}{}, err)
		if intErr != nil && ctx2.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".task", func() {
		err = w.task.Run(ctx)
	})
}

func (w *taskRunnerWork0) Free() {
	w.pool.Put(w)
}

// TaskRunner[T] dispatches a single-argument [psgfn.Task[T]] onto its
// Pool's worker pool. See [TaskRunner0] for shared semantics.
type TaskRunner[T any] struct {
	pool     *Pool
	task     psgfn.Task[T]
	limiter  Limiter
	errSink  Gatherer[struct{}]
	workPool *omnipool.Pool[taskRunnerWork[T]]
}

// NewTaskRunner binds a [psgfn.Task[T]] to a [Pool] and returns a
// [TaskRunner[T]]. See [NewTaskRunner0].
func NewTaskRunner[T any](pool *Pool, task psgfn.Task[T], opts ...OpOption) TaskRunner[T] {
	if pool == nil {
		panic("pool must be non-nil")
	}
	if task == nil {
		panic("task must be non-nil")
	}
	cfg := resolveOpConfig(opts)
	return TaskRunner[T]{
		pool:     pool,
		task:     task,
		limiter:  cfg.singleLimiter(),
		errSink:  newTaskErrSink(),
		workPool: omnipool.For[taskRunnerWork[T]](),
	}
}

// Start launches Run(ctx, arg) on a worker goroutine. See
// [TaskRunner0.Start] for backpressure and ctx behavior.
func (r TaskRunner[T]) Start(ctx context.Context, arg T) error {
	traceRegion := "TaskRunner.Start"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, time.Time{}, arg)
	return meta.ExecuteNowOrQueue(ctx, work)
}

// TryStart attempts to launch Run(ctx, arg) without blocking. See
// [TaskRunner0.TryStart].
func (r TaskRunner[T]) TryStart(ctx context.Context, deadline time.Time, arg T) (bool, error) {
	traceRegion := "TaskRunner.TryStart"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, deadline, arg)
	ok, err := meta.TryExecuteNow(ctx, deadline, work)
	if !ok {
		work.Free()
	}
	return ok, err
}

func (r TaskRunner[T]) newScatterWork(group workq.GroupID, deadline time.Time, arg T) *taskRunnerScatterWork {
	inner := r.newTask(group, arg)
	taskWork := r.pool.newTaskWork(group, inner, limiterCompletedFn(r.limiter))
	postWork := r.pool.newTaskPostWork(group, deadline, taskWork)
	gated := postWork
	if r.limiter.impl != nil {
		gated = newLimiterScatterWork(r.pool, deadline, gated, r.limiter)
	}
	return newTaskRunnerScatterWork(r.pool, deadline, gated)
}

func (r TaskRunner[T]) newTask(group workq.GroupID, arg T) boundTask {
	w := r.workPool.Get()
	w.pool = r.workPool
	w.job = r.pool
	w.group = group
	w.task = r.task
	w.arg = arg
	w.errSink = r.errSink
	return w
}

type taskRunnerWork[T any] struct {
	pool    *omnipool.Pool[taskRunnerWork[T]]
	job     *Pool
	group   workq.GroupID
	task    psgfn.Task[T]
	arg     T
	errSink Gatherer[struct{}]
}

func (w *taskRunnerWork[T]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	_ = group
	_ = taskWorkerSender
	traceRegion := "taskRunnerWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var err error = ErrTaskPanicked
	defer func() {
		if completedFn != nil {
			completedFn()
		}
		if err == nil {
			return
		}
		trace.Logf(ctx, traceRegion, "routing task err=%v to errSink", err)
		ctx2, meta := w.job.ctxMeta(ctx)
		intErr := w.errSink.submit(ctx2, meta, w.job, w.group, struct{}{}, err)
		if intErr != nil && ctx2.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".task", func() {
		err = w.task.Run(ctx, w.arg)
	})
}

func (w *taskRunnerWork[T]) Free() {
	var zero T
	w.arg = zero
	w.pool.Put(w)
}

// TaskRunner2[T1, T2] dispatches a two-argument [psgfn.Task2[T1, T2]]
// onto its Pool's worker pool. See [TaskRunner0] for shared semantics.
type TaskRunner2[T1, T2 any] struct {
	pool     *Pool
	task     psgfn.Task2[T1, T2]
	limiter  Limiter
	errSink  Gatherer[struct{}]
	workPool *omnipool.Pool[taskRunnerWork2[T1, T2]]
}

// NewTaskRunner2 binds a [psgfn.Task2[T1, T2]] to a [Pool] and returns
// a [TaskRunner2[T1, T2]]. See [NewTaskRunner0].
func NewTaskRunner2[T1, T2 any](pool *Pool, task psgfn.Task2[T1, T2], opts ...OpOption) TaskRunner2[T1, T2] {
	if pool == nil {
		panic("pool must be non-nil")
	}
	if task == nil {
		panic("task must be non-nil")
	}
	cfg := resolveOpConfig(opts)
	return TaskRunner2[T1, T2]{
		pool:     pool,
		task:     task,
		limiter:  cfg.singleLimiter(),
		errSink:  newTaskErrSink(),
		workPool: omnipool.For[taskRunnerWork2[T1, T2]](),
	}
}

// Start launches Run(ctx, arg1, arg2) on a worker goroutine. See
// [TaskRunner0.Start].
func (r TaskRunner2[T1, T2]) Start(ctx context.Context, arg1 T1, arg2 T2) error {
	traceRegion := "TaskRunner2.Start"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, time.Time{}, arg1, arg2)
	return meta.ExecuteNowOrQueue(ctx, work)
}

// TryStart attempts to launch Run(ctx, arg1, arg2) without blocking.
// See [TaskRunner0.TryStart].
func (r TaskRunner2[T1, T2]) TryStart(ctx context.Context, deadline time.Time, arg1 T1, arg2 T2) (bool, error) {
	traceRegion := "TaskRunner2.TryStart"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := vetStart(ctx, r.pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(group, deadline, arg1, arg2)
	ok, err := meta.TryExecuteNow(ctx, deadline, work)
	if !ok {
		work.Free()
	}
	return ok, err
}

func (r TaskRunner2[T1, T2]) newScatterWork(
	group workq.GroupID, deadline time.Time, arg1 T1, arg2 T2,
) *taskRunnerScatterWork {
	inner := r.newTask(group, arg1, arg2)
	taskWork := r.pool.newTaskWork(group, inner, limiterCompletedFn(r.limiter))
	postWork := r.pool.newTaskPostWork(group, deadline, taskWork)
	gated := postWork
	if r.limiter.impl != nil {
		gated = newLimiterScatterWork(r.pool, deadline, gated, r.limiter)
	}
	return newTaskRunnerScatterWork(r.pool, deadline, gated)
}

func (r TaskRunner2[T1, T2]) newTask(group workq.GroupID, arg1 T1, arg2 T2) boundTask {
	w := r.workPool.Get()
	w.pool = r.workPool
	w.job = r.pool
	w.group = group
	w.task = r.task
	w.arg1 = arg1
	w.arg2 = arg2
	w.errSink = r.errSink
	return w
}

type taskRunnerWork2[T1, T2 any] struct {
	pool    *omnipool.Pool[taskRunnerWork2[T1, T2]]
	job     *Pool
	group   workq.GroupID
	task    psgfn.Task2[T1, T2]
	arg1    T1
	arg2    T2
	errSink Gatherer[struct{}]
}

func (w *taskRunnerWork2[T1, T2]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	_ = group
	_ = taskWorkerSender
	traceRegion := "taskRunnerWork2.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var err error = ErrTaskPanicked
	defer func() {
		if completedFn != nil {
			completedFn()
		}
		if err == nil {
			return
		}
		trace.Logf(ctx, traceRegion, "routing task err=%v to errSink", err)
		ctx2, meta := w.job.ctxMeta(ctx)
		intErr := w.errSink.submit(ctx2, meta, w.job, w.group, struct{}{}, err)
		if intErr != nil && ctx2.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".task", func() {
		err = w.task.Run(ctx, w.arg1, w.arg2)
	})
}

func (w *taskRunnerWork2[T1, T2]) Free() {
	var zero1 T1
	var zero2 T2
	w.arg1 = zero1
	w.arg2 = zero2
	w.pool.Put(w)
}

// newTaskErrSink returns a Gatherer[struct{}] whose handler returns
// the input error as-is, surfacing unexpected Task.Run errors via the
// Pool's GatherAll path.
func newTaskErrSink() Gatherer[struct{}] {
	return NewGatherer(func(ctx context.Context, _ struct{}, err error) error {
		return err
	})
}

// vetStart validates that the given pool and ctx are suitable for
// launching a task. It checks that the calling ctx is one of the
// allowed types (top-level, gather, or combine) and that the pool is
// not yet done. Panics on misuse.
func vetStart(
	ctx context.Context,
	pool *Pool,
) (context.Context, *ctxMeta) {
	ctx, meta := pool.topLevelCtxMeta(ctx, func(ctxType contextType) {
		switch ctxType {
		case topLevelContext, gatherContext, combineContext:
			// These are valid for starting a task
		default:
			panic(fmt.Sprintf(
				"Start called from %v context but allowed only by top-level, gather, or combine context",
				ctxType))
		}
	})

	pool.panicIfDone()

	return ctx, meta
}

// taskRunnerScatterWork wraps the target's inner scatter work with the
// owning Pool's backpressure (protoBB). Mirrors gatherScatterWork's
// role in the pre-Wave-3 codepath.
type taskRunnerScatterWork struct {
	workq.Work
	job      *Pool
	deadline time.Time
}

func newTaskRunnerScatterWork(
	job *Pool,
	deadline time.Time,
	targetScatterWork workq.Work,
) *taskRunnerScatterWork {
	w := taskRunnerScatterWorkPool.Get()
	w.Work = targetScatterWork
	w.job = job
	w.deadline = deadline
	return w
}

func (w *taskRunnerScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskRunnerScatterWork.Execute"
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
func (w *taskRunnerScatterWork) Free() {
	traceRegion := "taskRunnerScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Work.Free()
	taskRunnerScatterWorkPool.Put(w)
}

var taskRunnerScatterWorkPool = omnipool.For[taskRunnerScatterWork]()
