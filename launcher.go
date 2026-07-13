// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Launcher[T] dispatches a [Handler[T]] onto a [Wave]'s
// underlying worker pool. Each dispatch (Submit / Start /
// TrySubmit / TryStart) invokes Handle once on a worker goroutine.
// Result delivery is the handler's responsibility — the body calls
// Submit on whatever downstream sinks it captures. If Handle
// returns a non-nil error, the framework routes it through an
// internal sink so it surfaces via the Wave's SkimAll path.
//
// For the no-arg case (T = struct{}), wrap a func(ctx) error in
// [Task] and dispatch via [Launcher.Start]. For the err-
// receiving void case, wrap a func(ctx, err) error in
// [ErrHandler]. For arity > 1, define a struct holding the
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
	wave    Wave
	handler Handler[T]
	// bindings are the limiters this op acquires from, in canonical global acquisition
	// order (ascending pool rank); nil ⟹ unlimited. Each carries an optional weigher
	// (nil ⟹ weight 1). Bound via WithLimits/WithWeightLimits/WithLimiterSet/
	// WithWeightLimiterSet. Every dispatch acquires all of them jointly, in order (the
	// deadlock-free discipline); a duplicate limiter across binder calls panics.
	bindings []binding[T]
	errSink  ErrSkimmer
	workPool *omnipool.Pool[launcherWork[T]]
}

// NewLauncher creates a Launcher for handler. The op is wave-agnostic:
// each dispatch resolves the target wave from the ambient body ctx, or
// bind one explicitly with [Launcher.In] (required at top level, where
// there is no ambient wave). Chain [Launcher.WithLimits] (plain) or
// [Launcher.WithWeightLimits] (weighted) to throttle dispatch.
//
// The framework manages an internal error sink that surfaces
// unexpected errors returned by Handle through the dispatching
// Wave's SkimAll path.
func NewLauncher[T any](handler Handler[T]) Launcher[T] {
	if handler == nil {
		panic("handler must be non-nil")
	}
	return Launcher[T]{
		handler:  handler,
		errSink:  newTaskErrSink(),
		workPool: omnipool.For[launcherWork[T]](),
	}
}

// WithLimits returns a copy of the Launcher bound to the given plain [Limiter]s (the
// In(wave) copy-with-modification pattern), so each dispatch acquires a weight-1 permit
// from each before the work runs. Bind a weight-capable limiter instead with
// [Launcher.WithWeightLimits].
//
// Several limiters (across any mix of the binder methods) AND-compose: every dispatch
// acquires all of them jointly, in the canonical global acquisition order, deadlock-free.
// A duplicate limiter panics.
func (r Launcher[T]) WithLimits(limiters ...Limiter) Launcher[T] {
	for _, l := range limiters {
		if l.pool != nil { // the zero (unlimited) Limiter gates nothing
			r.bindings = addBinding(r.bindings, l.pool, nil)
		}
	}
	return r
}

// WithWeightLimits returns a copy of the Launcher bound to the given [WeightLimiter]
// bindings, so each dispatch acquires a permit of weight weigh(value) from the weighted
// limiter. See [Launcher.WithLimits] for the plain case and joint AND-composition.
func (r Launcher[T]) WithWeightLimits(wls ...WeightLimiter[T]) Launcher[T] {
	for _, wl := range wls {
		r.bindings = addBinding(r.bindings, wl.limiter.weightedPool(), wl.weigh)
	}
	return r
}

// WithLimiterSet returns a copy of the Launcher bound to every plain limiter in the
// canonicalized [LimiterSet]. See [Launcher.WithLimits].
func (r Launcher[T]) WithLimiterSet(s LimiterSet) Launcher[T] {
	for _, pool := range s.pools {
		r.bindings = addBinding(r.bindings, pool, nil)
	}
	return r
}

// WithWeightLimiterSet returns a copy of the Launcher bound to every weighted binding in
// the canonicalized [WeightLimiterSet]. See [Launcher.WithWeightLimits].
func (r Launcher[T]) WithWeightLimiterSet(s WeightLimiterSet[T]) Launcher[T] {
	for _, b := range s.bindings {
		r.bindings = addBinding(r.bindings, b.pool, b.weigh)
	}
	return r
}

// In returns a copy of the Launcher bound to wave, so its dispatches place
// work in wave instead of the ambient (body-ctx) wave. Use at top level (no
// ambient wave) or to redirect work into another wave.
func (r Launcher[T]) In(wave Wave) Launcher[T] {
	r.wave = wave
	return r
}

// NewFnLauncher creates a Launcher from a closure-based handler.
// Convenience wrapper for `NewLauncher(NewHandler(handle))`.
// T is inferred from handle's value parameter, sparing the user the
// [T] annotation. Wave-agnostic; see [NewLauncher] and [Launcher.In].
func NewFnLauncher[T any](
	handle func(ctx context.Context, value T, err error) error,
) Launcher[T] {
	return NewLauncher(NewHandler(handle))
}

// TaskLauncher is the [Launcher][struct{}] case — a launcher for
// no-arg task bodies (typically constructed via [NewTaskLauncher],
// which pairs with the [Task] / [TaskFunc] no-input adapter).
type TaskLauncher = Launcher[struct{}]

// NewTaskLauncher creates a Launcher for a no-arg task body. Convenience
// wrapper for `NewLauncher(NewTask(task))`. Wave-agnostic; see
// [NewLauncher] and [Launcher.In].
func NewTaskLauncher(task func(ctx context.Context) error) TaskLauncher {
	return NewLauncher(NewTask(task))
}

// ErrLauncher is the [Launcher][struct{}] case viewed as an err
// sink — same underlying type as [TaskLauncher], named for intent.
// Typically constructed via [NewErrLauncher], which pairs with the
// [ErrHandler] / [ErrHandlerFunc] err-receiving adapter.
type ErrLauncher = Launcher[struct{}]

// NewErrLauncher creates a Launcher for an err-receiving handler.
// Convenience wrapper for `NewLauncher(NewErrHandler(handle))`.
// Wave-agnostic; see [NewLauncher] and [Launcher.In].
func NewErrLauncher(handle func(ctx context.Context, err error) error) ErrLauncher {
	return NewLauncher(NewErrHandler(handle))
}

// Submit dispatches Handle(ctx, value, nil) on the bound Wave's
// worker pool. Sugar for SubmitResult(ctx, value, nil).
//
// Before launching, Submit applies backpressure by skimming some
// already-completed work. If a Limiter is at its concurrency limit,
// Submit blocks until a slot becomes available. The ctx may be used
// to cancel both skimming and launch; only the ctx associated with
// the Wave's Wave is passed to Handle.
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
	return r.SubmitResult(ctx, value, nil)
}

// SubmitErr dispatches Handle(ctx, *new(T), err) on the bound
// Wave's worker pool. Sugar for SubmitResult(ctx, *new(T), err).
// Meaningful primarily when T = struct{} (the err-sink pattern,
// typically paired with [ErrHandler]); for other T, the
// handler receives the type's zero value alongside the err.
func (r Launcher[T]) SubmitErr(ctx context.Context, err error) error {
	var zero T
	return r.SubmitResult(ctx, zero, err)
}

// SubmitResult dispatches Handle(ctx, value, err) on the bound
// Wave's worker pool. The (value, err) pair is forwarded to the
// handler as-is; sinks that genuinely want both halves of a Go
// result tuple use this form. See [Launcher.Submit] for backpressure
// and ctx behavior.
func (r Launcher[T]) SubmitResult(ctx context.Context, value T, err error) error {
	traceRegion := "Launcher.SubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	_, derr := r.dispatch(ctx, Forever, value, err, false)
	return derr
}

// TrySubmit attempts to Submit without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, value, nil).
func (r Launcher[T]) TrySubmit(ctx context.Context, deadline time.Time, value T) (bool, error) {
	return r.TrySubmitResult(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, *new(T), err).
func (r Launcher[T]) TrySubmitErr(ctx context.Context, deadline time.Time, err error) (bool, error) {
	var zero T
	return r.TrySubmitResult(ctx, deadline, zero, err)
}

// TrySubmitResult attempts to SubmitResult without blocking past
// deadline. Returns (true, nil) on success, (false, nil) if a
// [Limiter] held the dispatch back and the deadline expired before
// a permit became available, or (false, non-nil) for any other
// failure.
func (r Launcher[T]) TrySubmitResult(ctx context.Context, deadline time.Time, value T, err error) (bool, error) {
	traceRegion := "Launcher.TrySubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	return r.dispatch(ctx, deadline, value, err, true)
}

// Start is sugar for Submit(ctx, *new(T)). Meaningful primarily
// when T's zero value is conventional (T = struct{} with a [Task]
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
func (r Launcher[T]) dispatch(
	ctx context.Context, deadline time.Time, value T, callerErr error, isTry bool,
) (bool, error) {
	wv, ok := resolveWave(r.wave, ctx)
	if !ok {
		return false, ErrWaveDone // bound wave has drained and recycled
	}
	defer wavePool.Release(wv)
	// Borrow the task body from the ORIGINAL (caller) ctx, not the meta-stamped ctx
	// vetStart returns: the meta-stamped ctx is a fresh ctxpool child each dispatch, so
	// rooting the body there defeats ctxpool's per-source-ctx reuse (newChildPool +
	// AfterFunc + a fresh child every call). The caller ctx is stable across a batch, so
	// its child pool and body children recycle. Cancellation/meta/parent are unaffected:
	// the meta-stamped ctx adds no cancellation, the body meta is self-contained
	// (parent is a field), and metaFromContext resolves the nearest child either way.
	srcCtx := ctx
	ctx, meta, owned := vetStart(ctx, wv)
	if owned {
		// The body is rooted at srcCtx (above), not this meta-stamped ctx, so the meta
		// is used only for this synchronous dispatch and can be recycled afterward.
		// Deferred BEFORE meta.Unlock so it runs AFTER it (LIFO).
		defer releaseTopLevelContext(ctx)
	}
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := r.newScatterWork(srcCtx, wv, group, deadline, value, callerErr)
	if isTry {
		ok, err := meta.TryExecuteNow(ctx, deadline, work)
		if !ok {
			work.Free()
		}
		return ok, err
	}
	err := meta.ExecuteNowOrQueue(ctx, work)
	return err == nil, err
}

//nolint:contextcheck // submitCtx is the body-ctx borrow source threaded to newTaskWork, not a propagated arg
func (r Launcher[T]) newScatterWork(
	submitCtx context.Context, wv *waveImpl, group workq.GroupID, deadline time.Time, value T, callerErr error,
) *launcherScatterWork {
	inner := r.newTask(wv, group, value, callerErr)
	var h *heldPermit
	if len(r.bindings) > 0 {
		// Build one per-limiter hold per binding, in canonical global acquisition order
		// (r.bindings is already sorted by pool rank): the lowest-rank binding is the head
		// handle, the higher-rank ones its rest, acquired jointly in that order at the gate.
		m, _ := metaFromContext(submitCtx)
		h = newHold(wv, m, r.bindings[0], value)
		for _, b := range r.bindings[1:] {
			h.rest = append(h.rest, newHold(wv, m, b, value))
		}
	}
	taskWork := wv.newTaskWork(submitCtx, group, inner, h)
	postWork := wv.newTaskPostWork(group, deadline, taskWork)
	gated := postWork
	if h != nil {
		gated = newLimiterScatterWork(wv, gated, h)
	}
	return newLauncherScatterWork(wv, deadline, gated)
}

// newHold takes a pooled heldPermit and stamps it for one binding: its body-wave cache for
// the binding's Pool (mkdir-p'ing the forest along the dispatching ancestry m, resolved at
// dispatch where that ancestry is available; the permit is acquired at the gate) and the
// weight the gate acquires (the weigher applied to value, else 1 for a plain binding).
func newHold[T any](wv *waveImpl, m *ctxMeta, b binding[T], value T) *heldPermit {
	h := heldPermitPool.Get()
	h.ownCache = wv.ensureCache(m, b.pool)
	h.weight = 1
	if b.weigh != nil {
		h.weight = b.weigh(value)
	}
	return h
}

func (r Launcher[T]) newTask(wv *waveImpl, group workq.GroupID, value T, callerErr error) *launcherWork[T] {
	wk := r.workPool.Get()
	wk.pool = r.workPool
	wk.wave = wv
	wk.group = group
	wk.handler = r.handler
	wk.value = value
	wk.callerErr = callerErr
	wk.errSink = r.errSink
	return wk
}

type launcherWork[T any] struct {
	pool      *omnipool.Pool[launcherWork[T]]
	wave      *waveImpl
	group     workq.GroupID
	handler   Handler[T]
	value     T
	callerErr error
	errSink   ErrSkimmer
}

func (wk *launcherWork[T]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
) {
	_ = group
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
		ctx2, meta := wk.wave.ctxMeta(ctx)
		intErr := wk.errSink.submit(ctx2, meta, wk.group, struct{}{}, err)
		if intErr != nil && ctx2.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".handler", func() {
		err = wk.handler.Handle(ctx, wk.value, wk.callerErr)
	})
}

func (wk *launcherWork[T]) Free() {
	var zero T
	wk.value = zero
	wk.callerErr = nil
	wk.pool.Release(wk)
}

// newTaskErrSink returns an ErrSkimmer whose handler returns the
// input error as-is, surfacing unexpected handler errors via the
// Wave's SkimAll path.
func newTaskErrSink() ErrSkimmer {
	return newInternalSkimmer(NewErrHandler(func(_ context.Context, err error) error {
		return err
	}))
}

// vetStart validates that the given wave and ctx are suitable for
// dispatching a handler. It checks that the calling ctx is one of
// the allowed types (top-level, skim, or funnel) and that the wave
// is not yet done. Panics on misuse.
// The bool return is topLevelCtxMeta's owned signal: true when a fresh top-level meta
// was minted (and so should be released after dispatch), false when an ambient meta
// was reused.
func vetStart(
	ctx context.Context,
	wv *waveImpl,
) (context.Context, *ctxMeta, bool) {
	ctx, meta, owned := wv.topLevelCtxMeta(ctx, func(ctxType contextType) {
		switch ctxType {
		case topLevelContext, skimContext, funnelContext:
			// These are valid for starting a task
		default:
			panic(fmt.Sprintf(
				"Start called from %v context but allowed only by top-level, skim, or funnel context",
				ctxType))
		}
	})

	wv.panicIfDone()

	return ctx, meta, owned
}

// launcherScatterWork wraps the target's inner scatter work with
// the owning Wave's backpressure (protoBB).
type launcherScatterWork struct {
	workq.Work
	wave     *waveImpl
	deadline time.Time
}

func newLauncherScatterWork(
	wv *waveImpl,
	deadline time.Time,
	targetScatterWork workq.Work,
) *launcherScatterWork {
	wk := launcherScatterWorkPool.Get()
	wk.Work = targetScatterWork
	wk.wave = wv
	wk.deadline = deadline
	return wk
}

func (wk *launcherScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "launcherScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", wk)

	workFn := wk.Work.Execute
	bb := wk.wave.protoBB
	if bb.ShouldBlock(ctx) != nil {
		return wk.wave.governor.Execute(ctx, ex, wk.deadline, bb, workFn)
	}
	return workFn(ctx, ex)
}

//nolint:contextcheck // background context used only for tracing
func (wk *launcherScatterWork) Free() {
	traceRegion := "launcherScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	wk.Work.Free()
	launcherScatterWorkPool.Release(wk)
}

var launcherScatterWorkPool = omnipool.For[launcherScatterWork]()
