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

// Launcher[T] dispatches a [psgfn.Handler[T]] onto a [Wave]'s
// underlying worker pool. Each dispatch (Submit / Start /
// TrySubmit / TryStart) invokes Handle once on a worker goroutine.
// Result delivery is the handler's responsibility — the body calls
// Submit on whatever downstream sinks it captures. If Handle
// returns a non-nil error, the framework routes it through an
// internal sink so it surfaces via the Wave's SkimAll path.
//
// For the no-arg case (T = struct{}), wrap a func(ctx) error in
// [psgfn.Task] and dispatch via [Launcher.Start]. For the err-
// receiving void case, wrap a func(ctx, err) error in
// [psgfn.ErrHandler]. For arity > 1, define a struct holding the
// fields and use [Launcher][YourStruct].
//
// Concurrency limiting: pass [WithLimits] at construction time to
// bind a [Limiter] (e.g. via [NewSemaphore]) that caps the number
// of in-flight dispatches.
//
// Thread-safety and copying: a Launcher value is designed to be
// copied. All copies share the same handler binding, limiter (if
// any), and internal error sink, so they can be passed by value or
// stored in structures and used concurrently.
type Launcher[T any] struct {
	wave     *Wave
	handler  psgfn.Handler[T]
	limiter  Limiter
	errSink  Skimmer[struct{}]
	workPool *omnipool.Pool[launcherWork[T]]
}

// NewLauncher binds a [psgfn.Handler[T]] to wave. wave may be nil
// to defer wave binding to the dispatching ctx at Submit / Start
// time (see [NewSkimmer] for the resolution rules). Pass
// [WithLimits] in opts to bind one or more [Limiter]s that throttle
// dispatch.
//
// The framework manages an internal error sink that surfaces
// unexpected errors returned by Handle through the dispatching
// Wave's SkimAll path.
func NewLauncher[T any](wave *Wave, handler psgfn.Handler[T], opts ...OpOption) Launcher[T] {
	if handler == nil {
		panic("handler must be non-nil")
	}
	cfg := resolveOpConfig(opts)
	return Launcher[T]{
		wave:     wave,
		handler:  handler,
		limiter:  cfg.singleLimiter(),
		errSink:  newTaskErrSink(),
		workPool: omnipool.For[launcherWork[T]](),
	}
}

// Submit dispatches Handle(ctx, value, nil) on the bound Wave's
// worker pool. Before launching, Submit applies backpressure by
// skimming some already-completed work. If a Limiter is at its
// concurrency limit, Submit blocks until a slot becomes available.
// The ctx may be used to cancel both skimming and launch; only the
// ctx associated with the Wave's Pool is passed to Handle.
//
// Returns a non-nil error if the ctx is canceled or if a skim
// function returns an error. If the returned error is non-nil, the
// handler will not have been invoked.
//
// WARNING: Submit must not be called from inside a task body
// launched on the same wave, since this can deadlock when a
// concurrency limit is reached. Call Submit from an associated
// Skim or Accumulate body instead. Submit attempts to detect this
// and panics, but the detection works only when the ctx passed to
// Submit descends from the ctx passed to Handle.
func (r Launcher[T]) Submit(ctx context.Context, value T) error {
	traceRegion := "Launcher.Submit"
	defer trace.StartRegion(ctx, traceRegion).End()
	return r.dispatch(ctx, time.Time{}, value, false)
}

// TrySubmit attempts to dispatch Handle(ctx, value, nil) without
// blocking past deadline. Returns (true, nil) on success, (false,
// nil) if a [Limiter] held the dispatch back and the deadline
// expired before a permit became available, or (false, non-nil) for
// any other failure.
func (r Launcher[T]) TrySubmit(ctx context.Context, deadline time.Time, value T) (bool, error) {
	traceRegion := "Launcher.TrySubmit"
	defer trace.StartRegion(ctx, traceRegion).End()
	err := r.dispatch(ctx, deadline, value, true)
	if err == nil {
		return true, nil
	}
	// TODO Thread C: distinguish the "dispatch held back past deadline"
	// case from genuine errors and return (false, nil) for the former.
	return false, err
}

// Start is sugar for Submit(ctx, *new(T)). Meaningful primarily
// when T's zero value is conventional (T = struct{} with a [psgfn.Task]
// handler is the common case); for other T, Start dispatches with
// the type's zero value.
func (r Launcher[T]) Start(ctx context.Context) error {
	var zero T
	return r.Submit(ctx, zero)
}

// TryStart is sugar for TrySubmit(ctx, deadline, *new(T)). See
// [Launcher.Start] and [Launcher.TrySubmit].
func (r Launcher[T]) TryStart(ctx context.Context, deadline time.Time) (bool, error) {
	var zero T
	return r.TrySubmit(ctx, deadline, zero)
}

//nolint:contextcheck // background context used only for tracing
func (r Launcher[T]) dispatch(ctx context.Context, deadline time.Time, value T, isTry bool) error {
	wave := resolveWave(r.wave, ctx)
	pool := wave.pool
	ctx, meta := vetStart(ctx, pool)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(pool, group, deadline, value, wave)
	if isTry {
		ok, err := meta.TryExecuteNow(ctx, deadline, work)
		if !ok {
			work.Free()
		}
		if err != nil {
			return err
		}
		if !ok {
			// caller's TrySubmit returns (false, nil)
			return nil
		}
		return nil
	}
	return meta.ExecuteNowOrQueue(ctx, work)
}

func (r Launcher[T]) newScatterWork(
	pool *Pool, group workq.GroupID, deadline time.Time, value T, wave *Wave,
) *launcherScatterWork {
	inner := r.newTask(pool, group, value)
	taskWork := pool.newTaskWork(group, inner, limiterCompletedFn(r.limiter), wave)
	postWork := pool.newTaskPostWork(group, deadline, taskWork)
	gated := postWork
	if r.limiter.impl != nil {
		gated = newLimiterScatterWork(pool, deadline, gated, r.limiter)
	}
	return newLauncherScatterWork(pool, deadline, gated)
}

func (r Launcher[T]) newTask(pool *Pool, group workq.GroupID, value T) boundTask {
	w := r.workPool.Get()
	w.pool = r.workPool
	w.job = pool
	w.group = group
	w.handler = r.handler
	w.value = value
	w.errSink = r.errSink
	return w
}

type launcherWork[T any] struct {
	pool    *omnipool.Pool[launcherWork[T]]
	job     *Pool
	group   workq.GroupID
	handler psgfn.Handler[T]
	value   T
	errSink Skimmer[struct{}]
}

func (w *launcherWork[T]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	_ = group
	_ = taskWorkerSender
	traceRegion := "launcherWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var err error = ErrTaskPanicked
	defer func() {
		if completedFn != nil {
			completedFn()
		}
		if err == nil {
			return
		}
		trace.Logf(ctx, traceRegion, "routing handler err=%v to errSink", err)
		ctx2, meta := w.job.ctxMeta(ctx)
		intErr := w.errSink.submit(ctx2, meta, w.job, w.group, struct{}{}, err)
		if intErr != nil && ctx2.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".handler", func() {
		err = w.handler.Handle(ctx, w.value, nil)
	})
}

func (w *launcherWork[T]) Free() {
	var zero T
	w.value = zero
	w.pool.Put(w)
}

// newTaskErrSink returns a Skimmer[struct{}] whose handler returns
// the input error as-is, surfacing unexpected handler errors via
// the Wave's SkimAll path.
func newTaskErrSink() Skimmer[struct{}] {
	return newInternalSkimmer(psgfn.HandlerFunc[struct{}](func(ctx context.Context, _ struct{}, err error) error {
		return err
	}))
}

// vetStart validates that the given pool and ctx are suitable for
// dispatching a handler. It checks that the calling ctx is one of
// the allowed types (top-level, skim, or funnel) and that the pool
// is not yet done. Panics on misuse.
func vetStart(
	ctx context.Context,
	pool *Pool,
) (context.Context, *ctxMeta) {
	ctx, meta := pool.topLevelCtxMeta(ctx, func(ctxType contextType) {
		switch ctxType {
		case topLevelContext, skimContext, funnelContext:
			// These are valid for starting a task
		default:
			panic(fmt.Sprintf(
				"Start called from %v context but allowed only by top-level, skim, or funnel context",
				ctxType))
		}
	})

	pool.panicIfDone()

	return ctx, meta
}

// launcherScatterWork wraps the target's inner scatter work with
// the owning Pool's backpressure (protoBB). Mirrors
// skimScatterWork's role in the pre-Wave-3 codepath.
type launcherScatterWork struct {
	workq.Work
	job      *Pool
	deadline time.Time
}

func newLauncherScatterWork(
	job *Pool,
	deadline time.Time,
	targetScatterWork workq.Work,
) *launcherScatterWork {
	w := launcherScatterWorkPool.Get()
	w.Work = targetScatterWork
	w.job = job
	w.deadline = deadline
	return w
}

func (w *launcherScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "launcherScatterWork.Execute"
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
func (w *launcherScatterWork) Free() {
	traceRegion := "launcherScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Work.Free()
	launcherScatterWorkPool.Put(w)
}

var launcherScatterWorkPool = omnipool.For[launcherScatterWork]()
