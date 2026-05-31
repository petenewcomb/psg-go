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

// Skimmer is a terminal sink: values arrive via [Skimmer.Submit] /
// [Skimmer.SubmitErr] and are dispatched to the user-supplied skim
// function during the supplied Wave's Skim / SkimAll. Task
// dispatch lives separately on [TaskRunner] — a Skimmer never runs
// tasks of its own.
//
// Thread-safety and copying: a Skimmer value is designed to be
// copied. All copies share the same skim function binding, so they
// can be passed by value to goroutines or stored in structures and
// used concurrently.
type Skimmer[T any] struct {
	handler  psgfn.Handler[T]
	workPool *omnipool.Pool[skimWork[T]]
}

// NewSkimmer binds a [psgfn.Handler] for value+err dispatch during
// the bound Wave's drain. The Skimmer is Wave-independent: callers
// supply a [Wave] at each [Skimmer.Submit] / [Skimmer.SubmitErr]
// call. For closure-based handlers, wrap in [psgfn.HandlerFunc][T]
// at the call site; struct implementations of Handler[T] support
// the alloc-free hot path.
func NewSkimmer[T any](
	handler psgfn.Handler[T],
) Skimmer[T] {
	if handler == nil {
		panic("handler must be non-nil")
	}
	return Skimmer[T]{
		handler:  handler,
		workPool: omnipool.For[skimWork[T]](),
	}
}

// Submit posts a value to the Skimmer's queue for later dispatch via
// the Wave's Skim / SkimAll. Convenience sugar for SubmitErr with
// a nil error.
func (g Skimmer[T]) Submit(
	ctx context.Context,
	wave *Wave,
	value T,
) error {
	return g.SubmitErr(ctx, wave, value, nil)
}

// SubmitErr posts a (value, err) pair to the Skimmer's queue for
// later dispatch by the Wave's Skim / SkimAll. err is delivered
// to the skim handler alongside value; use nil when reporting a
// successful result.
func (g Skimmer[T]) SubmitErr(
	ctx context.Context,
	wave *Wave,
	value T,
	err error,
) error {
	if wave == nil {
		panic("wave must be non-nil")
	}
	traceRegion := "Skimmer.SubmitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	target := wave.pool
	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.submit(ctx, meta, target, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline. See
// [Skimmer.Submit].
func (g Skimmer[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	wave *Wave,
	value T,
) (bool, error) {
	return g.TrySubmitErr(ctx, deadline, wave, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past deadline.
// See [Skimmer.SubmitErr].
func (g Skimmer[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	wave *Wave,
	value T,
	err error,
) (bool, error) {
	if wave == nil {
		panic("wave must be non-nil")
	}
	traceRegion := "Skimmer.TrySubmitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	target := wave.pool
	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.trySubmit(ctx, meta, target, group, value, err, deadline)
}

// boundSkimWork interface allows type erasure for skimWork instances
type boundSkimWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type skimWork[T any] struct {
	poolWork
	workq.DownstreamWork
	job     *Pool
	pool    *omnipool.Pool[skimWork[T]]
	handler psgfn.Handler[T]
	value   T
	err     error
}

// newSkimWork creates a new skim work item with the provided values
func (g Skimmer[T]) newSkimWork(group workq.GroupID, job *Pool, value T, err error) *skimWork[T] {
	w := g.workPool.Get()
	w.Init(g.workPool, group, job, g.handler, value, err)
	return w
}

func (w *skimWork[T]) Init(
	pool *omnipool.Pool[skimWork[T]],
	group workq.GroupID,
	job *Pool,
	handler psgfn.Handler[T],
	value T,
	err error,
) {
	w.poolWork.Init(group, job)
	w.job = job
	w.pool = pool
	w.handler = handler
	w.value = value
	w.err = err
}

func (w *skimWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "skimWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	ex.Starting()
	ctx, meta := w.job.ctxMeta(ctx)

	meta.PushGroup(w.Group())
	defer meta.PopGroup()

	return w.handler.Handle(ctx, w.value, w.err)
}

//nolint:contextcheck // background context used only for tracing
func (w *skimWork[T]) Free() {
	traceRegion := "skimWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.DownstreamWork.Close()
	w.poolWork.Close(w.job)
	w.pool.Put(w)
}

// submit creates skim work and posts it to the skim queue
func (g Skimmer[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
	group workq.GroupID,
	value T,
	err error,
) error {
	skimWork := g.newSkimWork(group, job, value, err)
	postWork := job.newSkimPostWork(group, skimWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// submit creates skim work and posts it to the skim queue
func (g Skimmer[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	skimWork := g.newSkimWork(group, job, value, err)
	postWork := job.newSkimPostWork(group, skimWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}
