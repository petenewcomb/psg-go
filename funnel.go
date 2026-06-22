// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/leakguard"
	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/workq"
)

// funnelHandleTrait implements leakguard.DupTrait for funnel resources.
// It manages the lifecycle of funnel instances through reference counting.
type funnelHandleTrait[T any] struct{}

func (funnelHandleTrait[T]) Close(c *funnel[T]) {
	c.unref()
}

func (funnelHandleTrait[T]) Dup(c *funnel[T]) (*funnel[T], error) {
	c.ref()
	return c, nil
}

func (funnelHandleTrait[T]) String(c *funnel[T]) string {
	return fmt.Sprintf("Funnel(%p)", c)
}

// Funnel represents a stateful aggregation operation. Inputs flow in through
// [Funnel.Submit] (or via [Funnel.Start] for value-producing tasks);
// the user-supplied [Accumulator] processes them inside a
// funnelEngine worker. Downstream emission is the Accumulator body's
// responsibility — it calls Submit on whatever downstream sinks it has
// captured. There is no framework-mediated output type; Accumulator
// errors are surfaced via the Wave's SkimAll path.
//
// Thread-safety and copying: a Funnel value is designed to be copied.
// While a single Funnel value does not support concurrent calls to
// Start or TryStart, copies of a Funnel can be used concurrently. All
// copies share the same funnel identity and will route work to the
// same Accumulator instances.
//
// Resource management: each Funnel must be explicitly closed via
// Close(). Dup() creates independent handles that share the same
// underlying state. The funnel resource is cleaned up when the last
// handle is closed and all internal references (from tasks and work
// items) are released.
type Funnel[T any] struct {
	h leakguard.Handle[funnel[T], funnelHandleTrait[T]]
}

// NewFunnel creates a new Funnel operation. Pass [WithLimits] in
// opts to bind a [Limiter] (e.g. via [NewSemaphore]) that caps the
// number of concurrent funnel-work executions for this Funnel.
//
// The framework manages an internal error sink that surfaces
// Accumulator errors through the Wave's SkimAll path; the user's
// Accumulator body is responsible for routing successful results via
// Submit on whatever downstream sinks it captures.
//
//nolint:contextcheck // background context used only for tracing
func NewFunnel[T any](
	wave *Wave,
	funnelFactory AccumulatorFactory[T],
	opts ...OpOption,
) Funnel[T] {
	traceRegion := "NewFunnel"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if wave == nil {
		panic("wave must be non-nil")
	}
	if funnelFactory == nil {
		panic("funnelFactory must be non-nil")
	}
	// The funnel engine is an internal per-WAVE detail (batch-scoped flush +
	// backpressure), shared by all funnels on this wave; lazily created here.
	fe := wave.funnelEngine()

	cfg := resolveOpConfig(opts)

	innerPool := omnipool.For[funnel[T]]()
	inner := innerPool.Get()

	if inner.refCount.Load() != 0 {
		panic("unexpected nonzero inner.refCount")
	}
	if inner.fEngine != nil {
		panic("unexpected non-nil inner.fEngine")
	}
	if inner.funnelFactory != nil {
		panic("unexpected non-nil inner.funnelFactory")
	}
	if inner.instanceCount.Load() != 0 {
		panic("unexpected nonzero inner.instanceCount")
	}

	inner.refCount.Store(1)
	// Framework-owned error sink: Accumulator errors flow through this
	// ErrSkimmer whose handler returns err as-is, surfacing via the
	// Wave's SkimAll path.
	inner.errSink = newInternalSkimmer(NewErrHandler(func(_ context.Context, err error) error {
		return err
	}))
	inner.fEngine = fe
	inner.funnelFactory = funnelFactory
	inner.limiter = cfg.singleLimiter()
	inner.innerPool = innerPool

	h := leakguard.New[funnel[T], funnelHandleTrait[T]](inner)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Funnel(%p), handleID=%d, pool=%p",
			inner, h.HandleID(), fe)
	}

	return Funnel[T]{h: h}
}

// NewFnFunnel binds closure-based factory functions to a Funnel.
// Convenience wrapper for
// `NewFunnel(wave, NewAccumulatorFactory(newAccumulator, closeFn), opts...)`.
// Pass nil for closeFn if the factory has no factory-level state
// to release.
func NewFnFunnel[T any](
	wave *Wave,
	newAccumulator func() Accumulator[T],
	closeFn func() error,
	opts ...OpOption,
) Funnel[T] {
	return NewFunnel(wave, NewAccumulatorFactory(newAccumulator, closeFn), opts...)
}

// ErrFunnel is the [Funnel][struct{}] case viewed as an err
// aggregator — a funnel whose Accumulator receives err inputs (the
// value half is always void). Typically constructed via
// [NewErrFunnel], which wires a closure-based factory whose
// accumulator delivers errs through an err-receiving signature.
type ErrFunnel = Funnel[struct{}]

// NewErrFunnel constructs a [Funnel][struct{}] whose per-instance
// accumulator receives err inputs via an err-receiving signature.
// Convenience wrapper for [NewFunnel] + [NewErrAccumulatorFactory]
// — uses the direct-fn-storage err-only adapter so the framework
// adds no closure allocations of its own.
//
// accumulate is called for every [Funnel.SubmitErr] (and matching
// SubmitResult-with-non-nil-err); it returns the flush deadline
// (zero for "no specific deadline") and any error.
// flush is optional (pass nil for a no-op flush). closeFn is the
// factory-level cleanup hook (see [AccumulatorFactory.Close]); pass
// nil for no-op. For per-instance state, use [NewFunnel] +
// [NewAccumulatorFactory] with a [NewErrAccumulator] inside the
// factory closure.
func NewErrFunnel(
	wave *Wave,
	accumulate func(ctx context.Context, err error) (time.Time, error),
	flush func(ctx context.Context) error,
	closeFn func() error,
	opts ...OpOption,
) ErrFunnel {
	return NewFunnel(wave, NewErrAccumulatorFactory(accumulate, flush, closeFn), opts...)
}

// Submit posts a value to the Funnel. Sugar for
// SubmitResult(ctx, value, nil).
func (c *Funnel[T]) Submit(
	ctx context.Context,
	value T,
) error {
	return c.SubmitResult(ctx, value, nil)
}

// SubmitErr posts an err-only result to the Funnel. Sugar for
// SubmitResult(ctx, *new(T), err). Meaningful primarily when
// T = struct{}; for other T, the Accumulator receives the type's
// zero value alongside the err.
func (c *Funnel[T]) SubmitErr(
	ctx context.Context,
	err error,
) error {
	var zero T
	return c.SubmitResult(ctx, zero, err)
}

// SubmitResult posts a (value, err) pair to the Funnel. The pair
// is forwarded to the Accumulator as-is; sinks that genuinely want
// both halves of a Go result tuple use this form.
func (c *Funnel[T]) SubmitResult(
	ctx context.Context,
	value T,
	err error,
) error {
	traceRegion := "Funnel.SubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.refInner()
	defer inner.unref()
	trace.Logf(ctx, traceRegion, "Funnel(%p)", inner)

	ctx, meta := inner.fEngine.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.submit(ctx, meta, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, value, nil).
func (c *Funnel[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value T,
) (bool, error) {
	return c.TrySubmitResult(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past
// deadline. Sugar for TrySubmitResult(ctx, deadline, *new(T), err).
func (c *Funnel[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	err error,
) (bool, error) {
	var zero T
	return c.TrySubmitResult(ctx, deadline, zero, err)
}

// TrySubmitResult attempts to SubmitResult without blocking past
// deadline. See [Funnel.TrySubmit] for return semantics.
func (c *Funnel[T]) TrySubmitResult(
	ctx context.Context,
	deadline time.Time,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Funnel.TrySubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.refInner()
	defer inner.unref()
	trace.Logf(ctx, traceRegion, "Funnel(%p)", inner)

	ctx, meta := inner.fEngine.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.trySubmit(ctx, meta, group, value, err, deadline)
}

// Dup creates a duplicate handle to the same underlying Funnel.
// Like file descriptor duplication, this creates a new handle that shares
// the same underlying funnel state but requires its own Close() call.
// This is useful for passing Funnel handles to different goroutines
// or async operations that need their own lifecycle management.
func (c *Funnel[T]) Dup() Funnel[T] {
	h, err := leakguard.Dup(c.h)
	if err != nil {
		panic(fmt.Sprintf("Dup() failed: %v", err))
	}
	return Funnel[T]{h: h}
}

// Close releases this handle to the Funnel. Each handle (including dups)
// must be closed exactly once. The underlying funnel state is cleaned up
// when the last handle is closed.
func (c *Funnel[T]) Close() {
	c.h.Close()
}

// refInner gets the inner funnel, checks if closed, and adds a reference.
// Panics if the Funnel has been closed.
// The caller must ensure a matching unref() is called.
func (c *Funnel[T]) refInner() *funnel[T] {
	inner := c.h.Get()
	if inner == nil {
		panic("Funnel has been closed")
	}
	inner.ref()
	return inner
}

type funnel[T any] struct {
	refCount atomic.Int64

	// errSink is framework-owned. Accumulator errors are routed through
	// it; its handler returns err as-is so it surfaces via SkimAll.
	errSink       ErrSkimmer
	fEngine       *funnelEngine
	funnelFactory AccumulatorFactory[T]

	// limiter caps how many funnelWorks this Funnel processes
	// concurrently. The zero Limiter (impl == nil) means unlimited.
	// Acquired in funnelWork.Execute and released when Execute
	// completes.
	limiter Limiter

	innerPool          *omnipool.Pool[funnel[T]]
	funnelInstancePool *omnipool.Pool[funnelInstance[T]]
	funnelWorkPool     *omnipool.Pool[funnelWork[T]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*funnelInstance[T]]
}

func (c *funnel[T]) Init() {
	c.funnelInstancePool = omnipool.For[funnelInstance[T]]()
	c.funnelWorkPool = omnipool.For[funnelWork[T]]()
	c.instanceQueue.Init()
}

func (c *funnel[T]) Reset() {
	// Reset logic is now handled in unref() when refCount hits zero.
	// We keep this empty method to satisfy the Resetter interface - if we didn't,
	// omnipool would zero the entire struct including pool pointers set by Init().
}

// ref increments the refCount to track handle ownership and internal references.
// It is called by leakguard when a handle is created via Dup(), and also used
// for internal refs (tasks, work items).
func (c *funnel[T]) ref() {
	newCount := c.refCount.Add(1)
	if newCount <= 1 {
		panic("ref() called with no existing references")
	}
}

// dropInstanceLiveness drops the funnel-liveness reference an instance holds
// from creation until its flush: it decrements the live-instance count
// and unrefs the funnel. Called by the party that performs an instance's
// flush, after releasing that instance's c.mu (so a teardown it triggers
// never recycles an instance whose c.mu is still held).
func (c *funnel[T]) dropInstanceLiveness() {
	c.instanceCount.Add(-1)
	c.unref()
}

// unref is called by leakguard when a handle is closed.
// It decrements refCount and cleans up if this was the last reference.
//
//nolint:contextcheck // cleanup runs via refcount, not on a caller ctx; framework-internal ctx is correct
func (c *funnel[T]) unref() {
	newCount := c.refCount.Add(-1)
	if newCount < 0 {
		panic("reference count underflow")
	}

	if newCount != 0 {
		return
	}

	// Last reference - we now have exclusive access, no mutex needed.
	// funnel-liveness drops at flush, so by now every instance has flushed.
	if c.instanceCount.Load() != 0 {
		panic("instance count is not zero")
	}

	// Drain the reuse cache: spent shells linger in instanceQueue (an nbcq
	// has no mid-queue removal) after their flush dropped funnel-liveness, so
	// the funnel can reach teardown with them still cached. Return them to the
	// pool here — pure cache cleanup, since they hold no live references.
	// No instance c.mu can be held now: any active funnelWork would hold an
	// funnel reference, so refCount would not have reached zero.
	for {
		inst, ok := c.instanceQueue.TryPopFront()
		if !ok {
			break
		}
		if inst.accumulator != nil {
			panic("live instance in queue at funnel teardown")
		}
		c.funnelInstancePool.Put(inst)
	}

	// Call factory.Close() to release factory-level state. Errors
	// route through the framework's err path (the errSink) so they
	// surface via SkimAll.
	if c.funnelFactory != nil {
		if closeErr := c.funnelFactory.Close(); closeErr != nil {
			ctx, meta := c.fEngine.job.ctxMeta(c.fEngine.job.ctx)
			intErr := c.errSink.submit(
				ctx, meta, c.fEngine.job, workq.InvalidGroupID,
				struct{}{}, closeErr,
			)
			if intErr != nil && ctx.Err() == nil {
				panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
			}
		}
	}

	// Save innerPool before clearing
	innerPool := c.innerPool

	// Clear all fields
	c.errSink = ErrSkimmer{}
	c.fEngine = nil
	c.funnelFactory = nil
	c.limiter = Limiter{}
	// Keep c.innerPool - it's metadata about where to return this object

	// Return to pool
	innerPool.Put(c)
}

type funnelInstance[T any] struct {
	// ScheduledWorkItem supplies the immutable [workq.Work] identity
	// (ID/Group, Init'd once at creation) and the scheduled-queue
	// position bookkeeping. The identity is never mutated after Init, so
	// the controller can read ID()/Group() without holding c.mu (it sorts
	// buffered work by group then ID). Group is the instance's flush group
	// — distinct from earliestGroup below, which tracks the lowest input
	// group seen. Free is overridden (see below); the scheduled-queue
	// position is owned entirely by delayq (mutated only under its mutex),
	// so this type never hooks position changes — which is what keeps
	// delayq's mutex from ever waiting on c.mu (the deadlock fix).
	workq.ScheduledWorkItem

	funnel *funnel[T]

	mu            sync.Mutex
	earliestGroup workq.GroupID

	// accumulator is the live user state. Non-nil means the instance is
	// live; flush sets it nil exactly once, which is the sole liveness
	// signal — there is no per-instance reference count. Mutated only
	// under mu.
	accumulator Accumulator[T]
}

// Execute implements [workq.Work]: it runs the scheduled flush once the
// instance's deadline has come due and the scheduled-work queue has
// surfaced it as fresh work. Any funnel worker may run it. This is party "D"
// in the lifetime model: it flushes and drops funnel-liveness, then never touches
// the instance again (see [funnelInstance.forceFlush]), so an owner
// reuse-pop is free to recycle the spent shell the instant Execute
// releases c.mu.
func (c *funnelInstance[T]) Execute(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	workerCtx, _ := c.funnel.fEngine.job.ctxMeta(ctx)
	c.forceFlush(workerCtx)
	return nil
}

// Free implements [workq.Work] as a no-op. A deadline-driven flush
// completes everything it needs — the user flush, the job barrier, and
// funnel-liveness — inside [funnelInstance.Execute] before releasing c.mu and
// never touches the instance again. Because an owner reuse-pop may recycle
// the spent shell the instant c.mu is released (before the controller
// gets here to call Free), Free must not read any instance field.
func (c *funnelInstance[T]) Free() {}

// forceFlush flushes the instance out-of-band — for a deadline-driven
// [funnelInstance.Execute] or the end-of-work sweep — and, if this call
// performed the flush, drops funnel-liveness. It captures the funnel before taking
// c.mu and uses only that local afterward, so it never touches the
// instance object once c.mu is released (rule R2): the spent shell is
// then safe for an owner reuse-pop to recycle concurrently.
func (c *funnelInstance[T]) forceFlush(ctx context.Context) {
	f := c.funnel
	var didFlush bool
	func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		didFlush = c.flush(ctx)
	}()
	if didFlush {
		f.dropInstanceLiveness()
	}
}

func (c *funnelInstance[T]) allocate(
	ctx context.Context,
	newAccumulator AccumulatorFactory[T],
) {
	traceRegion := "funnelInstance.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emitErr(ctx, ErrFunnelFactoryPanicked)
		}
	}()
	c.accumulator = newAccumulator.NewAccumulator()
	panicked = false
	if c.accumulator == nil {
		c.emitErr(ctx, ErrFunnelFactoryReturnedNil)
		c.accumulator = &errAccumulator[T]{err: ErrFunnelFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Funnel(%p) returning new accumulator=%v", c.funnel, c.accumulator)
	}
}

// emitErr surfaces an Accumulator error through the framework-owned error
// sink. The errSink's handler returns the error to the caller of
// Wave.SkimAll. Successful results are not surfaced this way — the
// Accumulator body is expected to Submit those to user-owned downstream
// sinks directly.
func (c *funnelInstance[T]) emitErr(ctx context.Context, accErr error) {
	traceRegion := "funnelInstance.emitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	if accErr == nil {
		return
	}
	ctx, meta := c.funnel.fEngine.job.ctxMeta(ctx)
	err := c.funnel.errSink.submit(
		ctx, meta, c.funnel.fEngine.job, c.earliestGroup, struct{}{}, accErr)
	if err != nil && ctx.Err() == nil {
		panic(fmt.Sprintf("unexpected non-cancelation error: %v", err))
	}
}

func (c *funnelInstance[T]) accumulate(
	ctx context.Context,
	input T,
	inputErr error,
) {

	traceRegion := "funnelInstance.funnel"
	defer trace.StartRegion(ctx, traceRegion).End()

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, ErrFunnelPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Accumulate on accumulator=%v", c.accumulator)
	newFlushDeadline, err := c.accumulator.Accumulate(ctx, input, inputErr)
	didNotPanic = true

	if err != nil {
		c.emitErr(ctx, err)
	}

	workQueue := &c.funnel.fEngine.workQueue
	switch {
	case !newFlushDeadline.IsZero() && time.Until(newFlushDeadline) <= 0:
		// Already-past deadline — flush inline, but only if no
		// deadline-driven Execute has already claimed this instance.
		// ClaimForFlush removes any pending heap entry and grants the
		// flush; if it returns false the instance was already drained, so
		// its pending Execute will flush the data just accumulated and we
		// leave the accumulator live (rule R1).
		if workQueue.ClaimForFlush(c) {
			c.flush(ctx)
		}
	default:
		// Either a future deadline or no deadline (zero). In the
		// no-deadline case the accumulator stays alive until the
		// pool's job-end flush sweep picks it up; we still schedule
		// the instance on the scheduled work queue — with a far-future
		// placeholder deadline — so that sweep finds it.
		//
		// Reschedule re-adds (or updates) the heap entry unless the
		// instance was already drained, in which case it returns false and
		// we leave it to the pending Execute (rule R1).
		deadline := newFlushDeadline
		if deadline.IsZero() {
			deadline = time.Now().Add(maxFlushAllSkew)
		}
		workQueue.Reschedule(c, deadline)
	}
}

// Must already hold c.mu. Returns whether this call performed the flush
// (false if the instance was already flushed). Drops the per-instance job
// barrier reference, but NOT funnel-liveness or the pooled object — the caller
// does that after releasing c.mu (the flusher drops funnel-liveness; the owner
// lineage recycles).
func (c *funnelInstance[T]) flush(ctx context.Context) bool {
	traceRegion := "funnelInstance.flush"

	accumulator := c.accumulator
	if accumulator == nil {
		// already flushed, ignore
		return false
	}
	c.accumulator = nil

	// Release the per-instance flush barrier reference acquired at
	// allocation. Deferred so a panicking Flush still releases it, and
	// ordered after the accumulator.Flush body below so that any
	// downstream Submit performed by Flush takes its work reference
	// before this reference drops — totalReferences cannot transiently
	// reach zero across an emitting flush.
	defer c.funnel.fEngine.job.state.DecrementReference()

	panicked := true // Assume the worst
	defer func() {
		if panicked {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, ErrFunnelFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on accumulator=%v", accumulator)
	err := accumulator.Flush(ctx)
	panicked = false
	if err != nil {
		c.emitErr(ctx, err)
	}
	return true
}

func (c *funnel[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	funnelWork := c.newFunnelWork(group, value, err, meta.wave)
	postWork := c.fEngine.newFunnelPostWork(group, funnelWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

func (c *funnel[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	// Create funnel work directly with values
	funnelWork := c.newFunnelWork(group, value, err, meta.wave)
	postWork := c.fEngine.newFunnelPostWork(group, funnelWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}

// boundFunnelWork interface allows type erasure for funnelWork instances
type boundFunnelWork interface {
	workq.Work
	Funnel(ctx context.Context)
	Waiting(*workq.Governor)
}

type funnelWork[T any] struct {
	poolWork
	workq.DownstreamWork
	funnel   *funnel[T]
	input    T
	inputErr error
	// req is the Limiter request handle this work's admission runs
	// through; nil for unlimited operations, created lazily at the first gate
	// attempt and persisting across postponed retries (stable identity).
	// The funnelWork owns its lifecycle: released at body end in Execute
	// (or idempotently in Free for never-executed work), recycled in Free.
	req request
	// wave is the dispatching wave; stamped onto the funnel worker's
	// ctxMeta during executeInner so nil-wave dispatches from the
	// Accumulate / Flush body can resolve it.
	wave *Wave
}

// funnelWork is the applicant its Limiter request is opened for:
// accessors box lazily, only when a sizing limiter actually reads them.
func (w *funnelWork[T]) Processor() any {
	return w.funnel.funnelFactory
}

func (w *funnelWork[T]) Value() any {
	return w.input
}

func (w *funnelWork[T]) Err() error {
	return w.inputErr
}

func (c *funnel[T]) newFunnelWork(group workq.GroupID, value T, err error, wave *Wave) *funnelWork[T] {
	w := c.funnelWorkPool.Get()
	w.Init(group, c, value, err, wave)
	return w
}

func (w *funnelWork[T]) Init(group workq.GroupID, f *funnel[T], input T, inputErr error, wave *Wave) {
	w.poolWork.Init(group, f.fEngine.job)
	w.funnel = f
	w.input = input
	w.inputErr = inputErr
	w.wave = wave
	f.ref() // Add reference for the funnel work
}

func (w *funnelWork[T]) Funnel(ctx context.Context) {
	// This is the owner lineage ("A"/"C"): an instance is either cached in
	// instanceQueue or being processed here, never both.
	var hbc *funnelInstance[T]
	for {
		hbc, _ = w.funnel.instanceQueue.TryPopFront()
		if hbc == nil {
			break
		}
		hbc.mu.Lock()
		if hbc.accumulator != nil {
			if w.Group() < hbc.earliestGroup {
				hbc.earliestGroup = w.Group()
			}
			break // still holding hbc.mu lock — reuse this live instance
		}
		// Spent shell: a deadline-driven Execute already flushed it (which
		// dropped funnel-liveness and the barrier) and left it cached here. As
		// the owner, recycle it and keep looking for a live instance.
		f := hbc.funnel
		hbc.mu.Unlock()
		f.funnelInstancePool.Put(hbc)
	}
	if hbc == nil {
		hbc = w.funnel.funnelInstancePool.Get()
		hbc.mu.Lock()
		// funnel-liveness: one reference per instance, taken at creation and
		// dropped at flush (see [funnel.dropInstanceLiveness]).
		w.funnel.ref()
		// Per-instance flush barrier: hold one job reference for the
		// instance's whole live lifetime (until flush() runs). This keeps
		// the job out of Done while the accumulator is unflushed,
		// regardless of which worker eventually flushes it. Released in
		// flush().
		w.funnel.fEngine.job.state.IncrementReference()
		w.funnel.instanceCount.Add(1)
		hbc.funnel = w.funnel
		// Reset and re-Init the embedded work item: a fresh work ID, the
		// flush group, and a zeroed (never-scheduled) heap position. The
		// reset matters for reuse — a pooled instance retains the negative
		// removed sentinel from its previous life, and clearing it keeps
		// the position tri-state honest so a stray Expedite of a fresh
		// instance is caught (see delayq.Item).
		hbc.ScheduledWorkItem = workq.ScheduledWorkItem{}
		hbc.Init(w.Group())
		hbc.earliestGroup = w.Group()
		hbc.allocate(ctx, w.funnel.funnelFactory)
	}
	defer func() {
		// If funnel() flushed inline, it did so via ClaimForFlush, which
		// guarantees no deadline-driven Execute also holds this instance —
		// so this lineage owns the spent shell outright: drop funnel-liveness
		// and recycle. Otherwise the instance is still live; cache it.
		spent := hbc.accumulator == nil
		f := hbc.funnel
		hbc.mu.Unlock()
		if spent {
			f.dropInstanceLiveness()
			f.funnelInstancePool.Put(hbc)
		} else {
			f.instanceQueue.PushBack(hbc)
		}
	}()
	hbc.accumulate(ctx, w.input, w.inputErr)
}

func (w *funnelWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "funnelWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "funnelWork(%p), %v", w, w)

	if w.funnel.limiter.impl == nil {
		return w.executeInner(ctx, ex)
	}

	if w.req == nil {
		w.req = w.funnel.limiter.impl.newRequest(w)
	}
	held, err := acquireOrWait(ctx, ex, time.Time{}, w.funnel.fEngine.job.protoBB, w.req)
	if err != nil || !held {
		return err
	}
	// The funnel permit scopes exactly the body run: executeInner always
	// starts, so the postpone-after-grant case doesn't arise here.
	// Released on return (panic-inclusive); Free's release is then an
	// idempotent no-op before the recycle.
	defer w.req.release()
	return w.executeInner(ctx, ex)
}

func (w *funnelWork[T]) executeInner(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	// Run the funnel body on a global-pool worker under a borrowed per-wave
	// execShell (mirrors taskWork.Execute). runInShell supplies the body context +
	// ctxMeta (wave + worker E + the held limiter request); we push the funnel's
	// group onto that exEnv so nil-wave dispatches from inside the Accumulate /
	// Flush body resolve to it. The legacy cpWorker flush-signal subscription and
	// IncrementCompleted metric are dropped (the end-of-work flush is the per-wave
	// flusher; IncrementCompleted was write-only).
	return runInShell(ctx, w.wave, funnelContext, w.req, func(shellCtx context.Context) error {
		meta, _ := metaFromContext(shellCtx)
		meta.PushGroup(w.Group())
		defer meta.PopGroup()
		w.Funnel(shellCtx)
		return nil
	})
}

func (w *funnelWork[T]) Free() {
	traceRegion := "funnelWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "funnelWork(%p), %v", w, w)

	if w.req != nil {
		// Normal completion already released at body end; this is the
		// idempotent backstop for work freed without executing
		// (cancellation drain) — by-state: abandon PENDING / give back
		// HELD.
		w.req.release()
		freeRequest(w.req)
		w.req = nil
	}

	w.DownstreamWork.Close()
	w.poolWork.Close(w.funnel.fEngine.job)

	pool := w.funnel.funnelWorkPool
	w.funnel.unref()
	pool.Put(w)
}

// errAccumulator is the framework's substitute Accumulator used when a user
// FunnelFactory misbehaves (returns nil or panics during construction).
// Every call simply surfaces the recorded error; nothing accumulates.
type errAccumulator[T any] struct {
	err error
}

func (c errAccumulator[T]) Accumulate(ctx context.Context, value T, err error) (time.Time, error) {
	return time.Now(), c.err
}

func (c errAccumulator[T]) Flush(ctx context.Context) error {
	return c.err
}
