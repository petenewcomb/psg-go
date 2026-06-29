// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/workq"
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
	handler  Handler[T]
	workPool *omnipool.Pool[skimWork[T]]
}

// NewSkimmer creates a Skimmer for value+err dispatch during a Wave's
// drain. The op is wave-agnostic: each dispatch resolves the target wave
// from the ambient body ctx, or bind one explicitly with [Skimmer.In]
// (required at top level). One Skimmer can thus be reused across waves.
//
// For closure-based handlers, wrap in [HandlerFunc] at the
// call site; struct implementations of Handler[T] support the
// alloc-free hot path.
func NewSkimmer[T any](
	handler Handler[T],
) Skimmer[T] {
	if handler == nil {
		panic("handler must be non-nil")
	}
	return Skimmer[T]{
		handler:  handler,
		workPool: omnipool.For[skimWork[T]](),
	}
}

// In returns a copy of the Skimmer bound to wave, so its dispatches place
// work in wave instead of the ambient (body-ctx) wave. Use at top level (no
// ambient wave) or to redirect work into another wave.
func (g Skimmer[T]) In(wave *Wave) Skimmer[T] {
	g.wave = wave
	return g
}

// NewFnSkimmer creates a Skimmer from a closure-based handler for
// drain-time dispatch. Convenience wrapper for
// `NewSkimmer(NewHandler(handle))`. T is inferred from handle's value
// parameter. Wave-agnostic; see [NewSkimmer] and [Skimmer.In].
func NewFnSkimmer[T any](
	handle func(ctx context.Context, value T, err error) error,
) Skimmer[T] {
	return NewSkimmer(NewHandler(handle))
}

// ErrSkimmer is the [Skimmer][struct{}] case viewed as an err sink
// — a drain-time sink that processes err results via an
// err-receiving handler. Typically constructed via [NewErrSkimmer],
// which pairs with the [ErrHandler] / [ErrHandlerFunc] adapter.
type ErrSkimmer = Skimmer[struct{}]

// NewErrSkimmer creates a Skimmer for an err-receiving handler for
// drain-time dispatch. Convenience wrapper for
// `NewSkimmer(NewErrHandler(handle))`. Wave-agnostic; see [NewSkimmer]
// and [Skimmer.In].
func NewErrSkimmer(handle func(ctx context.Context, err error) error) ErrSkimmer {
	return NewSkimmer(NewErrHandler(handle))
}

// newInternalSkimmer constructs a Skimmer used by the framework for
// error-routing sinks owned by ops (Launcher, Funnel). It has no
// Wave because the framework dispatches through it via the
// lower-level submit() helper with an explicit target Wave rather
// than the public Submit API. Must not be exposed to user code —
// calling the public Submit / SubmitErr methods on it would
// dereference a nil wave.
func newInternalSkimmer[T any](
	handler Handler[T],
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
// [ErrHandler]); for other T, the handler receives the
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

	target := resolveWave(g.wave, ctx)
	target.ensureArmed() // dispatch entry: re-arm a drained wave for a new cycle
	// Mint-or-reuse a meta: in-body submits reuse the ambient body meta; a top-level
	// op.In(&wave).Submit from a bare ctx mints a fresh top-level meta (and a
	// cross-wave submit redirects into target, recording the source as parent). No
	// ctx-type restriction — a value may be submitted to a skimmer from anywhere.
	// Skimmer submit does not yet recycle its minted meta (follow-on); owned ignored.
	ctx, meta, _ := target.topLevelCtxMeta(ctx, func(contextType) {})
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.submit(ctx, meta, group, value, err)
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

	target := resolveWave(g.wave, ctx)
	target.ensureArmed() // dispatch entry: re-arm a drained wave for a new cycle
	// Mint-or-reuse a meta: in-body submits reuse the ambient body meta; a top-level
	// op.In(&wave).Submit from a bare ctx mints a fresh top-level meta (and a
	// cross-wave submit redirects into target, recording the source as parent). No
	// ctx-type restriction — a value may be submitted to a skimmer from anywhere.
	// Skimmer submit does not yet recycle its minted meta (follow-on); owned ignored.
	ctx, meta, _ := target.topLevelCtxMeta(ctx, func(contextType) {})
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.trySubmit(ctx, meta, group, value, err, deadline)
}

// boundSkimWork interface allows type erasure for skimWork instances
type boundSkimWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type skimWork[T any] struct {
	poolWork
	workq.DownstreamWork
	wave    *Wave
	pool    *omnipool.Pool[skimWork[T]]
	handler Handler[T]
	value   T
	err     error
}

// newSkimWork creates a new skim work item with the provided values
func (g Skimmer[T]) newSkimWork(group workq.GroupID, wv *Wave, value T, err error) *skimWork[T] {
	wk := g.workPool.Get()
	wk.Init(g.workPool, group, wv, g.handler, value, err)
	return wk
}

func (wk *skimWork[T]) Init(
	pool *omnipool.Pool[skimWork[T]],
	group workq.GroupID,
	wv *Wave,
	handler Handler[T],
	value T,
	err error,
) {
	wk.poolWork.Init(group, wv)
	wk.wave = wv
	wk.pool = pool
	wk.handler = handler
	wk.value = value
	wk.err = err
}

func (wk *skimWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "skimWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", wk)

	ex.Starting()
	ctx, meta := wk.wave.ctxMeta(ctx)

	meta.PushGroup(wk.Group())
	defer meta.PopGroup()

	return wk.handler.Handle(ctx, wk.value, wk.err)
}

//nolint:contextcheck // background context used only for tracing
func (wk *skimWork[T]) Free() {
	traceRegion := "skimWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	wk.DownstreamWork.Close()
	wk.poolWork.Close(wk.wave)
	wk.pool.Put(wk)
}

// submit creates skim work and posts it to the skim queue. The target wave is read
// from meta.wave (the resolved dispatch target the meta was minted for).
func (g Skimmer[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	skimWork := g.newSkimWork(group, meta.wave, value, err)
	postWork := meta.wave.newSkimPostWork(group, skimWork, meta.ShouldBlock())
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// submit creates skim work and posts it to the skim queue
func (g Skimmer[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	skimWork := g.newSkimWork(group, meta.wave, value, err)
	postWork := meta.wave.newSkimPostWork(group, skimWork, meta.ShouldBlock())
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}
