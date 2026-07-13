// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/ctxpool"
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
	wave     Wave
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
func (s Skimmer[T]) In(wave Wave) Skimmer[T] {
	s.wave = wave
	return s
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
func (s Skimmer[T]) Submit(
	ctx context.Context,
	value T,
) error {
	return s.SubmitResult(ctx, value, nil)
}

// SubmitErr posts an err-only result to the Skimmer's queue. Sugar
// for SubmitResult(ctx, *new(T), err). Meaningful primarily when
// T = struct{} (the err-sink pattern, typically paired with
// [ErrHandler]); for other T, the handler receives the
// type's zero value alongside the err.
func (s Skimmer[T]) SubmitErr(
	ctx context.Context,
	err error,
) error {
	var zero T
	return s.SubmitResult(ctx, zero, err)
}

// SubmitResult posts a (value, err) pair to the Skimmer's queue
// for later dispatch by the bound Wave's Skim / SkimAll. The pair
// is forwarded to the skim handler as-is; sinks that genuinely
// want both halves of a Go result tuple use this form.
func (s Skimmer[T]) SubmitResult(
	ctx context.Context,
	value T,
	err error,
) error {
	traceRegion := "Skimmer.SubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	target, ok := resolveWave(s.wave, ctx)
	if !ok {
		return ErrWaveDone // bound wave has drained and recycled
	}
	defer wavePool.Release(target)
	// Mint-or-reuse a meta: in-body submits reuse the ambient body meta; a top-level
	// op.In(wave).Submit from a bare ctx mints a fresh top-level meta (and a
	// cross-wave submit redirects into target, recording the source as parent). No
	// ctx-type restriction — a value may be submitted to a skimmer from anywhere.
	ctx, meta, owned := target.topLevelCtxMeta(ctx, func(contextType) {})
	if owned {
		// The submit uses the minted meta only synchronously (skimWork captures
		// riders by value with its own refs; nothing retains the meta), so the
		// dispatch releases it — recycling rides the unrefMeta cascade.
		defer releaseTopLevelContext(ctx)
	}
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return s.submit(ctx, meta, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, value, nil).
func (s Skimmer[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value T,
) (bool, error) {
	return s.TrySubmitResult(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past
// deadline. Sugar for TrySubmitResult(ctx, deadline, *new(T), err).
func (s Skimmer[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	err error,
) (bool, error) {
	var zero T
	return s.TrySubmitResult(ctx, deadline, zero, err)
}

// TrySubmitResult attempts to SubmitResult without blocking past
// deadline. See [Skimmer.TrySubmit] for return semantics.
func (s Skimmer[T]) TrySubmitResult(
	ctx context.Context,
	deadline time.Time,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Skimmer.TrySubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	target, ok := resolveWave(s.wave, ctx)
	if !ok {
		return false, ErrWaveDone // bound wave has drained and recycled
	}
	defer wavePool.Release(target)
	// Mint-or-reuse a meta: in-body submits reuse the ambient body meta; a top-level
	// op.In(wave).Submit from a bare ctx mints a fresh top-level meta (and a
	// cross-wave submit redirects into target, recording the source as parent). No
	// ctx-type restriction — a value may be submitted to a skimmer from anywhere.
	ctx, meta, owned := target.topLevelCtxMeta(ctx, func(contextType) {})
	if owned {
		// The submit uses the minted meta only synchronously (see SubmitResult).
		defer releaseTopLevelContext(ctx)
	}
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return s.trySubmit(ctx, meta, group, value, err, deadline)
}

// boundSkimWork interface allows type erasure for skimWork instances
type boundSkimWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type skimWork[T any] struct {
	poolWork
	workq.DownstreamWork
	wave    *waveImpl
	pool    *omnipool.Pool[skimWork[T]]
	handler Handler[T]
	value   T
	err     error
	// riders is the producing item's flow rider chain, captured at submit and held
	// (node + instance refs) until Free. A skim result is a flow CONTINUATION, not a
	// fan-in (CP-F7): the handler runs under the ITEM's riders (shadowing the
	// driver's — the item descends from it), and holding the refs keeps a tag
	// follow-up from firing while the result awaits skimming. nil for a result
	// submitted from a rider-free ctx.
	riders *flowRiderNode
	// itemMeta is the per-item child meta the handler ran under (Execute),
	// retained past the handler so Free can pass it as the LAST CARRIER of any
	// fire its rider release triggers (driver-contexts.md, "Fire"): the item's
	// refs are what end the flow here, and the item's continuation context is
	// the handler's per-item meta. nil when the work is freed without
	// executing (teardown) — the fire then takes the no-carrier fallback.
	itemMeta *ctxMeta
}

// captureRiders records the producing item's rider chain and takes the carrier
// refs (node + instance) that hold it alive from submit until Free — overlapping
// the item's own refs, so the chain never transits unreferenced.
func (wk *skimWork[T]) captureRiders(riders *flowRiderNode) {
	wk.riders = riders
	flowRefRiders(riders)
	nodeRef(riders)
}

// newSkimWork creates a new skim work item with the provided values
func (s Skimmer[T]) newSkimWork(group workq.GroupID, wv *waveImpl, value T, err error) *skimWork[T] {
	wk := s.workPool.Get()
	wk.Init(s.workPool, group, wv, s.handler, value, err)
	return wk
}

func (wk *skimWork[T]) Init(
	pool *omnipool.Pool[skimWork[T]],
	group workq.GroupID,
	wv *waveImpl,
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
	ctx, driveMeta := wk.wave.ctxMeta(ctx)

	// The handler runs under a PER-ITEM child meta of the drive meta
	// (docs/decisions/driver-contexts.md, "Skim handlers get a per-item child
	// context") rather than overriding the drive meta's riders in place — the
	// drive meta is ref'd-lifetime immutable, and an in-place override would
	// misdeliver: a rider-free item skimmed after a rider-carrying one would
	// read the previous item's — possibly already recycled — chain instead of
	// the drive's.
	//
	// parent = the drive meta, a SYNCHRONOUS derivation, not a permitRoot: the
	// handler runs on the drive goroutine, so vetNotNestedInSkim and the permit
	// walk must see through to the drive. riders = the item's chain (the CP-F7
	// nearest-wins continuation, now structural), or the drive's own for a
	// rider-free item — per item, correctly. No rider refs are taken here: the
	// item chain is held by wk's submit-time refs until Free, the drive chain by
	// the drive scope, and both cover the handler's synchronous extent; handler
	// dispatches take their own refs on borrow. exEnv is shared with the drive
	// like any derived skim meta (ownsExEnv stays false).
	meta := newCtxMeta()
	meta.wave = wk.wave
	meta.ctxType = skimContext
	meta.parent = driveMeta
	refMeta(driveMeta)
	meta.parentWaves = retainParentWaveSet(driveMeta.parentWaves)
	meta.executionEnvironment = driveMeta.executionEnvironment
	if wk.riders != nil {
		meta.riders = wk.riders
	} else {
		meta.riders = driveMeta.riders
	}
	ctx = ctxpool.WithValue(ctx, meta)
	meta.selfCtx = ctx
	// The owner ref transfers to the work item: Free passes the meta as the
	// last carrier of any fire its rider release triggers, then drops it.
	// Async work dispatched from the handler keeps the meta (and, via the
	// cascade, the drive meta) alive through its own parent ref.
	wk.itemMeta = meta

	meta.PushGroup(wk.Group())
	defer meta.PopGroup()

	return wk.handler.Handle(ctx, wk.value, wk.err)
}

//nolint:contextcheck // background context used only for tracing
func (wk *skimWork[T]) Free() {
	traceRegion := "skimWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	// Release the producing item's rider refs captured at submit (CP-F7). A release
	// that ends a follow-up's flow dispatches a wave-rooted fire whose last
	// carrier is the item — its continuation context is the handler's per-item
	// meta (alive here on the owner ref Execute transferred; nil if the work
	// never executed). Do it while wk.wave is still valid, before the recycle.
	//nolint:contextcheck // a fire dispatched here roots at the scheduler ctx by design
	flowUnrefRiders(wk.riders, wk.wave, wk.itemMeta)
	nodeUnref(wk.riders)
	wk.riders = nil
	unrefMeta(wk.itemMeta)
	wk.itemMeta = nil

	wk.DownstreamWork.Close()
	wk.poolWork.Close(wk.wave)
	wk.pool.Release(wk)
}

// submit creates skim work and posts it to the skim queue. The target wave is read
// from meta.wave (the resolved dispatch target the meta was minted for).
func (s Skimmer[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	wv := meta.wave // synchronous dispatch: the meta's wave is pinned by the dispatch
	skimWork := s.newSkimWork(group, wv, value, err)
	skimWork.captureRiders(meta.riders)
	postWork := wv.newSkimPostWork(group, skimWork, meta.ShouldBlock())
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// submit creates skim work and posts it to the skim queue
func (s Skimmer[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	wv := meta.wave // synchronous dispatch: the meta's wave is pinned by the dispatch
	skimWork := s.newSkimWork(group, wv, value, err)
	skimWork.captureRiders(meta.riders)
	postWork := wv.newSkimPostWork(group, skimWork, meta.ShouldBlock())
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}
