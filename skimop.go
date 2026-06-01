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
// function during the bound Wave's Skim / SkimAll. Task dispatch
// lives separately on [Launcher] — a Skimmer never runs tasks of
// its own.
//
// Thread-safety and copying: a Skimmer value is designed to be
// copied. All copies share the same skim function binding, so they
// can be passed by value to goroutines or stored in structures and
// used concurrently.
type Skimmer[T any] struct {
	wave     *Wave
	handler  psgfn.Handler[T]
	workPool *omnipool.Pool[skimWork[T]]
}

// NewSkimmer binds a [psgfn.Handler] to wave for value+err dispatch
// during that Wave's drain. wave may be nil — in that case the
// Skimmer is wave-independent and resolves the target wave at each
// dispatch from the dispatching ctx (which must descend from a
// [NewWave] call, or be a task / skim / accumulate body ctx whose
// dispatching wave the framework has stamped). This enables one
// Skimmer instance to be reused across many waves.
//
// For closure-based handlers, wrap in [psgfn.HandlerFunc] at the
// call site; struct implementations of Handler[T] support the
// alloc-free hot path.
func NewSkimmer[T any](
	wave *Wave,
	handler psgfn.Handler[T],
) Skimmer[T] {
	if handler == nil {
		panic("handler must be non-nil")
	}
	return Skimmer[T]{
		wave:     wave,
		handler:  handler,
		workPool: omnipool.For[skimWork[T]](),
	}
}

// newInternalSkimmer constructs a Skimmer used by the framework for
// error-routing sinks owned by ops (Launcher, Funnel). It has no
// Wave because the framework dispatches through it via the
// lower-level submit() helper with an explicit target Pool rather
// than the public Submit API. Must not be exposed to user code —
// calling the public Submit / SubmitErr methods on it would
// dereference a nil wave.
func newInternalSkimmer[T any](
	handler psgfn.Handler[T],
) Skimmer[T] {
	return Skimmer[T]{
		handler:  handler,
		workPool: omnipool.For[skimWork[T]](),
	}
}

// Submit posts a value to the Skimmer's queue for later dispatch
// via the bound Wave's Skim / SkimAll. Sugar for
// SubmitResult(ctx, value, nil).
func (g Skimmer[T]) Submit(
	ctx context.Context,
	value T,
) error {
	return g.SubmitResult(ctx, value, nil)
}

// SubmitErr posts an err-only result to the Skimmer's queue. Sugar
// for SubmitResult(ctx, *new(T), err). Meaningful primarily when
// T = struct{} (the err-sink pattern, typically paired with
// [psgfn.ErrHandler]); for other T, the handler receives the
// type's zero value alongside the err.
func (g Skimmer[T]) SubmitErr(
	ctx context.Context,
	err error,
) error {
	var zero T
	return g.SubmitResult(ctx, zero, err)
}

// SubmitResult posts a (value, err) pair to the Skimmer's queue
// for later dispatch by the bound Wave's Skim / SkimAll. The pair
// is forwarded to the skim handler as-is; sinks that genuinely
// want both halves of a Go result tuple use this form.
func (g Skimmer[T]) SubmitResult(
	ctx context.Context,
	value T,
	err error,
) error {
	traceRegion := "Skimmer.SubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	target := resolveWave(g.wave, ctx).pool
	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.submit(ctx, meta, target, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, value, nil).
func (g Skimmer[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value T,
) (bool, error) {
	return g.TrySubmitResult(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past
// deadline. Sugar for TrySubmitResult(ctx, deadline, *new(T), err).
func (g Skimmer[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	err error,
) (bool, error) {
	var zero T
	return g.TrySubmitResult(ctx, deadline, zero, err)
}

// TrySubmitResult attempts to SubmitResult without blocking past
// deadline. See [Skimmer.TrySubmit] for return semantics.
func (g Skimmer[T]) TrySubmitResult(
	ctx context.Context,
	deadline time.Time,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Skimmer.TrySubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	target := resolveWave(g.wave, ctx).pool
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
