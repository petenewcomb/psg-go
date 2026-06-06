// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/leakguard"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
)

// funnelOpHandleTrait implements leakguard.DupTrait for funnelOp resources.
// It manages the lifecycle of funnelOp instances through reference counting.
type funnelOpHandleTrait[T any] struct{}

func (funnelOpHandleTrait[T]) Close(c *funnelOp[T]) {
	c.unref()
}

func (funnelOpHandleTrait[T]) Dup(c *funnelOp[T]) (*funnelOp[T], error) {
	c.ref()
	return c, nil
}

func (funnelOpHandleTrait[T]) String(c *funnelOp[T]) string {
	return fmt.Sprintf("Funnel(%p)", c)
}

// Funnel represents a stateful aggregation op. Inputs flow in through
// [Funnel.Submit] (or via [Funnel.Start] for value-producing tasks);
// the user-supplied [Accumulator] processes them inside a
// FunnelPool worker. Downstream emission is the Accumulator body's
// responsibility — it calls Submit on whatever downstream sinks it has
// captured. There is no framework-mediated output type; Accumulator
// errors are surfaced via the Pool's SkimAll path.
//
// Thread-safety and copying: a Funnel value is designed to be copied.
// While a single Funnel value does not support concurrent calls to
// Start or TryStart, copies of a Funnel can be used concurrently. All
// copies share the same funnel identity and will route work to the
// same Accumulator instances.
//
// Resource management: each Funnel must be explicitly closed via
// Close(). Dup() creates independent handles that share the same
// underlying state. The funnelOp resource is cleaned up when the last
// handle is closed and all internal references (from tasks and work
// items) are released.
type Funnel[T any] struct {
	h leakguard.Handle[funnelOp[T], funnelOpHandleTrait[T]]
}

// NewFunnel creates a new Funnel operation. Pass [WithLimits] in
// opts to bind a [Limiter] (e.g. via [NewSemaphore]) that caps the
// number of concurrent funnel-work executions for this Funnel.
//
// The framework manages an internal error sink that surfaces
// Accumulator errors through the Pool's SkimAll path; the user's
// Accumulator body is responsible for routing successful results via
// Submit on whatever downstream sinks it captures.
//
//nolint:contextcheck // background context used only for tracing
func NewFunnel[T any](
	funnelPool *FunnelPool,
	funnelFactory AccumulatorFactory[T],
	opts ...OpOption,
) Funnel[T] {
	traceRegion := "NewFunnel"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if funnelPool == nil {
		panic("funnelPool must be non-nil")
	}
	if funnelFactory == nil {
		panic("funnelFactory must be non-nil")
	}

	cfg := resolveOpConfig(opts)

	innerPool := omnipool.For[funnelOp[T]]()
	inner := innerPool.Get()

	if inner.refCount.Load() != 0 {
		panic("unexpected nonzero inner.refCount")
	}
	if inner.funnelPool != nil {
		panic("unexpected non-nil inner.funnelPool")
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
	// Pool's SkimAll path.
	inner.errSink = newInternalSkimmer(NewErrHandler(func(_ context.Context, err error) error {
		return err
	}))
	inner.funnelPool = funnelPool
	inner.funnelFactory = funnelFactory
	inner.limiter = cfg.singleLimiter()
	inner.innerPool = innerPool

	h := leakguard.New[funnelOp[T], funnelOpHandleTrait[T]](inner)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Funnel(%p), handleID=%d, pool=%p",
			inner, h.HandleID(), funnelPool)
	}

	return Funnel[T]{h: h}
}

// NewFnFunnel binds closure-based factory functions to a
// FunnelPool. Convenience wrapper for
// `NewFunnel(funnelPool, NewAccumulatorFactory(newAccumulator, closeFn), opts...)`.
// Pass nil for closeFn if the factory has no factory-level state
// to release.
func NewFnFunnel[T any](
	funnelPool *FunnelPool,
	newAccumulator func() Accumulator[T],
	closeFn func() error,
	opts ...OpOption,
) Funnel[T] {
	return NewFunnel(funnelPool, NewAccumulatorFactory(newAccumulator, closeFn), opts...)
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
	funnelPool *FunnelPool,
	accumulate func(ctx context.Context, err error) (time.Time, error),
	flush func(ctx context.Context) error,
	closeFn func() error,
	opts ...OpOption,
) ErrFunnel {
	return NewFunnel(funnelPool, NewErrAccumulatorFactory(accumulate, flush, closeFn), opts...)
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

	ctx, meta := inner.funnelPool.job.ctxMeta(ctx)
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

	ctx, meta := inner.funnelPool.job.ctxMeta(ctx)
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

// refInner gets the inner funnelOp, checks if closed, and adds a reference.
// Panics if the Funnel has been closed.
// The caller must ensure a matching unref() is called.
func (c *Funnel[T]) refInner() *funnelOp[T] {
	inner := c.h.Get()
	if inner == nil {
		panic("Funnel has been closed")
	}
	inner.ref()
	return inner
}

type funnelInstanceID int64

var funnelInstanceCounter atomic.Int64

type funnelOp[T any] struct {
	refCount atomic.Int64

	// errSink is framework-owned. Accumulator errors are routed through
	// it; its handler returns err as-is so it surfaces via SkimAll.
	errSink       ErrSkimmer
	funnelPool    *FunnelPool
	funnelFactory AccumulatorFactory[T]

	// limiter caps how many funnelWorks this Funnel processes
	// concurrently. The zero Limiter (impl == nil) means unlimited.
	// Acquired in funnelWork.Execute and released when Execute
	// completes.
	limiter Limiter

	innerPool          *omnipool.Pool[funnelOp[T]]
	funnelInstancePool *omnipool.Pool[funnelInstance[T]]
	funnelWorkPool     *omnipool.Pool[funnelWork[T]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*funnelInstance[T]]
}

func (c *funnelOp[T]) Init() {
	c.funnelInstancePool = omnipool.For[funnelInstance[T]]()
	c.funnelWorkPool = omnipool.For[funnelWork[T]]()
	c.instanceQueue.Init()
}

func (c *funnelOp[T]) Reset() {
	// Reset logic is now handled in unref() when refCount hits zero.
	// We keep this empty method to satisfy the Resetter interface - if we didn't,
	// omnipool would zero the entire struct including pool pointers set by Init().
}

// ref increments the refCount to track handle ownership and internal references.
// It is called by leakguard when a handle is created via Dup(), and also used
// for internal refs (tasks, work items).
func (c *funnelOp[T]) ref() {
	newCount := c.refCount.Add(1)
	if newCount <= 1 {
		panic("ref() called with no existing references")
	}
}

// unref is called by leakguard when a handle is closed.
// It decrements refCount and cleans up if this was the last reference.
//
//nolint:contextcheck // cleanup runs via refcount, not on a caller ctx; framework-internal ctx is correct
func (c *funnelOp[T]) unref() {
	newCount := c.refCount.Add(-1)
	if newCount < 0 {
		panic("reference count underflow")
	}

	if newCount != 0 {
		return
	}

	// Last reference - we now have exclusive access, no mutex needed

	// Validate cleanup invariants
	if c.instanceCount.Load() != 0 {
		panic("instance count is not zero")
	}
	if _, ok := c.instanceQueue.TryPopFront(); ok {
		panic("instance queue was not empty")
	}

	// Call factory.Close() to release factory-level state. Errors
	// route through the framework's err path (the errSink) so they
	// surface via SkimAll.
	if c.funnelFactory != nil {
		if closeErr := c.funnelFactory.Close(); closeErr != nil {
			ctx, meta := c.funnelPool.job.ctxMeta(c.funnelPool.job.ctx)
			intErr := c.errSink.submit(
				ctx, meta, c.funnelPool.job, workq.InvalidGroupID,
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
	c.funnelPool = nil
	c.funnelFactory = nil
	c.limiter = Limiter{}
	// Keep c.innerPool - it's metadata about where to return this object

	// Return to pool
	innerPool.Put(c)
}

type funnelInstance[T any] struct {
	id funnelInstanceID
	op *funnelOp[T]

	// workID and flushGroup are the immutable [workq.Work] identity for
	// this instance when it is scheduled as a flush. Set once at creation
	// and never mutated, so the controller can read ID()/Group() without
	// holding c.mu (it sorts buffered work by group then ID). workID uses
	// the shared work-ID counter — funnelInstanceID is a separate counter
	// that could collide with other work and trip requeueBuffer's
	// equal-ID panic.
	workID     workq.WorkID
	flushGroup workq.GroupID

	mu            sync.Mutex
	refCount      int
	earliestGroup workq.GroupID
	accumulator   Accumulator[T]

	// queued reports whether this instance currently has a Ref held on
	// behalf of an in-flight Schedule on the funnel pool's timed work
	// queue. Mutated only under c.mu.
	//
	// It is deliberately distinct from flushHeapPos. flushHeapPos tracks
	// the queue's *heap* state, but delayq updates are deferred: a
	// Schedule only becomes a positive heap position once a later Drain
	// folds it in. queued records the Ref synchronously at Schedule time,
	// so a reschedule that races the fold (e.g. another worker reuses
	// this instance before the first Schedule folds) correctly observes
	// "already Ref'd" via queued and avoids a double Ref — which a
	// flushHeapPos check could not, since it would still read 0.
	queued bool

	// flushHeapPos is this instance's position in the timed work queue's
	// deadline structure, mutated only by the queue (under delayq.mu, via
	// SetPosition): 0 = never scheduled, >0 = the 1-based heap index,
	// <0 = previously scheduled and since removed/drained. See queued for
	// why heap position alone is insufficient for Ref bookkeeping.
	flushHeapPos int
}

// ID implements [workq.Work]. See workID.
func (c *funnelInstance[T]) ID() workq.WorkID { return c.workID }

// Group implements [workq.Work]. See flushGroup.
func (c *funnelInstance[T]) Group() workq.GroupID { return c.flushGroup }

// Execute implements [workq.Work]: it runs the scheduled flush once the
// instance's deadline has come due and the timed queue has surfaced it
// as fresh work. The Sender comes from the executing worker's
// environment (as in funnelWork.executeInner); any funnel worker may run
// it. The companion unref of the queued reference happens in Free.
func (c *funnelInstance[T]) Execute(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	workerCtx, meta := c.op.funnelPool.job.ctxMeta(ctx)
	cw := meta.executionEnvironment.(*cpWorker)
	c.mu.Lock()
	c.flush(workerCtx, cw.Sender())
	c.mu.Unlock()
	return nil
}

// Free implements [workq.Work]: it drops the reference held while the
// instance was queued for flushing (freeing the instance if it was the
// last). The flush itself ran in Execute; Free is the unref half of the
// old Flush, deferred to here so the controller never touches a recycled
// instance mid-buffer.
func (c *funnelInstance[T]) Free() {
	c.Unref()
}

// Position implements [delayq.Item]. The heap reads positions under
// delayq.mu so the read is consistent with the heap's own ordering;
// concurrent writers come exclusively through SetPosition, which
// synchronizes with funnel via c.mu.
func (c *funnelInstance[T]) Position() int { return c.flushHeapPos }

// SetPosition implements [delayq.Item]. It is called by the delayq
// heap under delayq.mu when an item is inserted, swapped, or removed.
// We take c.mu so funnel's read of c.queued and c.flushHeapPos
// stays consistent with the heap's view: when delayq removes c (a
// non-positive p — zero never occurs from the heap, but a negative
// removed sentinel does), the queued flag flips false here, ensuring a
// concurrent funnel that subsequently acquires c.mu correctly observes
// "no Ref outstanding" and Refs for its new Schedule.
//
// Lock ordering: delayq.mu first (held by the heap operation), then
// c.mu (taken here). funnel never takes delayq.mu while holding
// c.mu, so no deadlock.
func (c *funnelInstance[T]) SetPosition(p int) {
	c.mu.Lock()
	c.flushHeapPos = p
	if p <= 0 {
		c.queued = false
	}
	c.mu.Unlock()
}

// Must already be holding c.mu lock.
func (c *funnelInstance[T]) Ref() {
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount++
}

// Must not be holding c.mu lock.
func (c *funnelInstance[T]) Unref() {
	c.mu.Lock()
	finalRefDropped := c.unref()
	c.mu.Unlock()
	if finalRefDropped {
		c.free()
	}
}

// Must already be holding c.mu lock.
// Returns true if the final reference was dropped.
func (c *funnelInstance[T]) unref() bool {
	// Must already be holding c.mu lock
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount--
	return c.refCount == 0
}

// A call to unref() must already have returned true
func (c *funnelInstance[T]) free() {
	pool := c.op.funnelInstancePool
	op := c.op
	pool.Put(c)
	op.instanceCount.Add(-1)
	op.unref()
}

func (c *funnelInstance[T]) allocate(
	ctx context.Context,
	newAccumulator AccumulatorFactory[T],
	sender *rdvq.Sender,
) {
	traceRegion := "funnelInstance.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emitErr(ctx, sender, ErrFunnelFactoryPanicked)
		}
	}()
	c.accumulator = newAccumulator.NewAccumulator()
	panicked = false
	if c.accumulator == nil {
		c.emitErr(ctx, sender, ErrFunnelFactoryReturnedNil)
		c.accumulator = &errAccumulator[T]{err: ErrFunnelFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Funnel(%p) returning new accumulator=%v", c.op, c.accumulator)
	}
}

// emitErr surfaces an Accumulator error through the framework-owned error
// sink. The errSink's handler returns the error to the caller of
// Pool.SkimAll. Successful results are not surfaced this way — the
// Accumulator body is expected to Submit those to user-owned downstream
// sinks directly.
func (c *funnelInstance[T]) emitErr(ctx context.Context, sender *rdvq.Sender, accErr error) {
	traceRegion := "funnelInstance.emitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	if accErr == nil {
		return
	}
	ctx, meta := c.op.funnelPool.job.ctxMeta(ctx)
	err := c.op.errSink.submit(
		ctx, meta, c.op.funnelPool.job, c.earliestGroup, struct{}{}, accErr)
	if err != nil && ctx.Err() == nil {
		panic(fmt.Sprintf("unexpected non-cancelation error: %v", err))
	}
	_ = sender
}

func (c *funnelInstance[T]) funnel(
	ctx context.Context,
	sender *rdvq.Sender,
	input T,
	inputErr error,
) {

	traceRegion := "funnelInstance.funnel"
	defer trace.StartRegion(ctx, traceRegion).End()

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, sender, ErrFunnelPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Accumulate on accumulator=%v", c.accumulator)
	newFlushDeadline, err := c.accumulator.Accumulate(ctx, input, inputErr)
	didNotPanic = true

	if err != nil {
		c.emitErr(ctx, sender, err)
	}

	workQueue := &c.op.funnelPool.workQueue
	switch {
	case !newFlushDeadline.IsZero() && time.Until(newFlushDeadline) <= 0:
		// Already-past deadline — flush inline.
		if c.queued {
			workQueue.Remove(c)
			c.queued = false
		}
		c.flush(ctx, sender)
	default:
		// Either a future deadline or no deadline (zero). In the
		// no-deadline case the accumulator stays alive until the
		// pool's job-end flush sweep picks it up; we still schedule
		// the instance on the timed work queue — with a far-future
		// placeholder deadline — so that sweep finds it.
		deadline := newFlushDeadline
		if deadline.IsZero() {
			deadline = time.Now().Add(maxFlushAllSkew)
		}
		if !c.queued {
			c.Ref()
			c.queued = true
		}
		workQueue.Schedule(c, deadline)
	}
}

// Must not already hold c.mu
func (c *funnelInstance[T]) Flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "funnelInstance.Flush"
	defer trace.StartRegion(ctx, traceRegion).End()

	c.mu.Lock()
	defer func() {
		finalRefDropped := c.unref()
		c.mu.Unlock()
		if finalRefDropped {
			c.free()
		}
	}()
	c.flush(ctx, sender)
}

// Must already hold c.mu
func (c *funnelInstance[T]) flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "funnelInstance.flush"

	accumulator := c.accumulator
	if accumulator == nil {
		// already flushed, ignore
		return
	}
	c.accumulator = nil

	// Release the per-instance flush barrier reference acquired at
	// allocation. Deferred so a panicking Flush still releases it, and
	// ordered after the accumulator.Flush body below so that any
	// downstream Submit performed by Flush takes its work reference
	// before this reference drops — totalReferences cannot transiently
	// reach zero across an emitting flush.
	defer c.op.funnelPool.job.state.DecrementReference()

	panicked := true // Assume the worst
	defer func() {
		if panicked {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, sender, ErrFunnelFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on accumulator=%v", accumulator)
	err := accumulator.Flush(ctx)
	panicked = false
	if err != nil {
		c.emitErr(ctx, sender, err)
	}
}

func (c *funnelOp[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	funnelWork := c.newFunnelWork(group, value, err, meta.wave)
	postWork := c.funnelPool.newFunnelPostWork(group, funnelWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

func (c *funnelOp[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	// Create funnel work directly with values
	funnelWork := c.newFunnelWork(group, value, err, meta.wave)
	postWork := c.funnelPool.newFunnelPostWork(group, funnelWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}

// boundFunnelWork interface allows type erasure for funnelWork instances
type boundFunnelWork interface {
	workq.Work
	Funnel(ctx context.Context, sender *rdvq.Sender)
	Waiting(*workq.Governor)
}

type funnelWork[T any] struct {
	poolWork
	workq.DownstreamWork
	op       *funnelOp[T]
	input    T
	inputErr error
	// wave is the dispatching wave; stamped onto the funnel worker's
	// ctxMeta during executeInner so nil-wave op dispatches from the
	// Accumulate / Flush body can resolve it.
	wave *Wave
}

func (c *funnelOp[T]) newFunnelWork(group workq.GroupID, value T, err error, wave *Wave) *funnelWork[T] {
	w := c.funnelWorkPool.Get()
	w.Init(group, c, value, err, wave)
	return w
}

func (w *funnelWork[T]) Init(group workq.GroupID, op *funnelOp[T], input T, inputErr error, wave *Wave) {
	w.poolWork.Init(group, op.funnelPool.job)
	w.op = op
	w.input = input
	w.inputErr = inputErr
	w.wave = wave
	op.funnelPool.inFlight.Increment()
	op.ref() // Add reference for the funnel work
}

func (w *funnelWork[T]) Funnel(ctx context.Context, sender *rdvq.Sender) {
	var hbc *funnelInstance[T]
	for {
		hbc, _ = w.op.instanceQueue.TryPopFront()
		if hbc == nil {
			break
		}
		hbc.mu.Lock()
		if hbc.accumulator != nil {
			if w.Group() < hbc.earliestGroup {
				hbc.earliestGroup = w.Group()
			}
			break // still holding hbc.mu lock
		}
		// Was flushed, so unref
		finalRefDropped := hbc.unref()
		hbc.mu.Unlock()
		if finalRefDropped {
			hbc.free()
		}
	}
	if hbc == nil {
		hbc = w.op.funnelInstancePool.Get()
		hbc.mu.Lock()
		hbc.refCount = 1
		w.op.ref()
		// Per-instance flush barrier: hold one job reference for the
		// instance's whole live lifetime (here until flush() runs). This
		// keeps the job out of Done while the accumulator is unflushed,
		// regardless of which worker eventually flushes it. Released in
		// flush().
		w.op.funnelPool.job.state.IncrementReference()
		w.op.instanceCount.Add(1)
		hbc.op = w.op
		hbc.id = funnelInstanceID(funnelInstanceCounter.Add(1))
		hbc.workID = workq.NewWorkID()
		hbc.earliestGroup = w.Group()
		hbc.flushGroup = w.Group()
		// Reset the timed-queue position to the never-scheduled state. A
		// reused instance retains the negative removed sentinel from its
		// previous life; clearing it keeps Position's tri-state honest so
		// a stray Expedite of a fresh instance is caught (see delayq.Item).
		hbc.flushHeapPos = 0
		hbc.allocate(ctx, w.op.funnelFactory, sender)
	}
	defer func() {
		flushed := hbc.accumulator == nil
		finalRefDropped := flushed && hbc.unref()
		hbc.mu.Unlock()
		if !flushed {
			w.op.instanceQueue.PushBack(hbc)
		} else if finalRefDropped {
			hbc.free()
		}
	}()
	hbc.funnel(ctx, sender, w.input, w.inputErr)
}

func (w *funnelWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "funnelWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "funnelWork(%p), %v", w, w)

	if w.op.limiter.impl == nil {
		return w.executeInner(ctx, ex)
	}

	limiter := w.op.limiter
	acquired := false
	defer func() {
		if acquired {
			limiter.impl.release()
		}
	}()
	wb := workq.WaitBehavior{
		BlockBehavior: w.op.funnelPool.job.protoBB,
		ShouldWait: func() bool {
			if acquired {
				return false
			}
			acquired = limiter.impl.tryAcquire()
			return !acquired
		},
	}
	return workq.ExecuteOrWait(ctx, ex, time.Time{}, limiter.impl.notifier(), wb,
		w.executeInner)
}

func (w *funnelWork[T]) executeInner(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	workerCtx, meta := w.op.funnelPool.job.ctxMeta(ctx)
	cw := meta.executionEnvironment.(*cpWorker)
	cw.PushGroup(w.Group())
	defer cw.PopGroup()
	// Stamp the dispatching wave onto the worker's per-worker
	// ctxMeta so nil-wave op dispatches from inside the Accumulate /
	// Flush body resolve to it. The worker's ctxMeta is exclusive to
	// this goroutine for the funnel's lifetime; mutation is race-free
	// as long as user code doesn't capture ctx into a goroutine that
	// outlives the body.
	prevWave := meta.wave
	meta.wave = w.wave
	defer func() { meta.wave = prevWave }()
	cw.executeFunnel(workerCtx, w)
	return nil
}

func (w *funnelWork[T]) Free() {
	traceRegion := "funnelWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "funnelWork(%p), %v", w, w)

	w.op.funnelPool.inFlight.Decrement()

	w.DownstreamWork.Close()
	w.poolWork.Close(w.op.funnelPool.job)

	pool := w.op.funnelWorkPool
	w.op.unref()
	pool.Put(w)
}
