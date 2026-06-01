// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/delayq"
	"github.com/petenewcomb/psg-go/internal/leakguard"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
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
// the user-supplied [psgfn.Accumulator] processes them inside a
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
	funnelFactory psgfn.FunnelFactory[T],
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
	// Skimmer[struct{}] whose handler returns err as-is, surfacing via
	// the Pool's SkimAll path.
	inner.errSink = newInternalSkimmer(psgfn.HandlerFunc[struct{}](func(ctx context.Context, _ struct{}, err error) error {
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
	errSink       Skimmer[struct{}]
	funnelPool    *FunnelPool
	funnelFactory psgfn.FunnelFactory[T]

	// limiter caps how many funnelWorks this Funnel processes
	// concurrently. The zero Limiter (impl == nil) means unlimited.
	// Acquired in funnelWork.Execute and released when Execute
	// completes.
	limiter Limiter

	innerPool           *omnipool.Pool[funnelOp[T]]
	halfBoundFunnelPool *omnipool.Pool[halfBoundFunnel[T]]
	funnelWorkPool      *omnipool.Pool[funnelWork[T]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*halfBoundFunnel[T]]
}

func (c *funnelOp[T]) Init() {
	c.halfBoundFunnelPool = omnipool.For[halfBoundFunnel[T]]()
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

	// Save innerPool before clearing
	innerPool := c.innerPool

	// Clear all fields
	c.errSink = Skimmer[struct{}]{}
	c.funnelPool = nil
	c.funnelFactory = nil
	c.limiter = Limiter{}
	// Keep c.innerPool - it's metadata about where to return this object

	// Return to pool
	innerPool.Put(c)
}

type halfBoundFunnel[T any] struct {
	id funnelInstanceID
	op *funnelOp[T]

	mu            sync.Mutex
	refCount      int
	earliestGroup workq.GroupID
	accumulator   psgfn.Accumulator[T]

	// queued reports whether this instance currently has a Ref held on
	// behalf of an in-flight Schedule on the FunnelPool's flushQ.
	// Mutated only under c.mu.
	queued bool

	// flushHeapPos is the 1-based position of this instance in the
	// FunnelPool's flush deadline queue (0 means not in the queue).
	// Mutated only by the queue.
	flushHeapPos int
}

func (c *halfBoundFunnel[T]) InstanceID() funnelInstanceID {
	return c.id
}

func (c *halfBoundFunnel[T]) InstanceCount() int {
	return int(c.op.instanceCount.Load())
}

// Position implements [delayq.Item]. The heap reads positions under
// delayq.mu so the read is consistent with the heap's own ordering;
// concurrent writers come exclusively through SetPosition, which
// synchronizes with funnel via c.mu.
func (c *halfBoundFunnel[T]) Position() int { return c.flushHeapPos }

// SetPosition implements [delayq.Item]. It is called by the delayq
// heap under delayq.mu when an item is inserted, swapped, or removed.
// We take c.mu so funnel's read of c.queued and c.flushHeapPos
// stays consistent with the heap's view: when delayq's Drain pops c
// (p == 0), the queued flag flips false here, ensuring a concurrent
// funnel that subsequently acquires c.mu correctly observes "no Ref
// outstanding" and Refs for its new Schedule.
//
// Lock ordering: delayq.mu first (held by the heap operation), then
// c.mu (taken here). funnel never takes delayq.mu while holding
// c.mu, so no deadlock.
func (c *halfBoundFunnel[T]) SetPosition(p int) {
	c.mu.Lock()
	c.flushHeapPos = p
	if p == 0 {
		c.queued = false
	}
	c.mu.Unlock()
}

// Must already be holding c.mu lock.
func (c *halfBoundFunnel[T]) Ref() {
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount++
}

// Must not be holding c.mu lock.
func (c *halfBoundFunnel[T]) Unref() {
	c.mu.Lock()
	finalRefDropped := c.unref()
	c.mu.Unlock()
	if finalRefDropped {
		c.free()
	}
}

// Must already be holding c.mu lock.
// Returns true if the final reference was dropped.
func (c *halfBoundFunnel[T]) unref() bool {
	// Must already be holding c.mu lock
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount--
	return c.refCount == 0
}

// A call to unref() must already have returned true
func (c *halfBoundFunnel[T]) free() {
	pool := c.op.halfBoundFunnelPool
	op := c.op
	pool.Put(c)
	op.instanceCount.Add(-1)
	op.unref()
}

func (c *halfBoundFunnel[T]) allocate(
	ctx context.Context,
	newAccumulator psgfn.FunnelFactory[T],
	sender *rdvq.Sender,
) {
	traceRegion := "halfBoundFunnel.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emitErr(ctx, sender, ErrFunnelFactoryPanicked)
		}
	}()
	c.accumulator = newAccumulator()
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
func (c *halfBoundFunnel[T]) emitErr(ctx context.Context, sender *rdvq.Sender, accErr error) {
	traceRegion := "halfBoundFunnel.emitErr"
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

func (c *halfBoundFunnel[T]) funnel(
	ctx context.Context,
	flushQ *delayq.Queue[funnelFlusher],
	sender *rdvq.Sender,
	input T,
	inputErr error,
) {

	traceRegion := "halfBoundFunnel.funnel"
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

	switch {
	case !newFlushDeadline.IsZero() && time.Until(newFlushDeadline) <= 0:
		// Already-past deadline — flush inline.
		if c.queued {
			flushQ.Remove(c)
			c.queued = false
		}
		c.flush(ctx, sender)
	default:
		// Either a future deadline or no deadline (zero). In the
		// no-deadline case the accumulator stays alive until the
		// FunnelPool's job-end flush sweep picks it up; we still
		// place the instance in the flushQ — with a far-future
		// placeholder deadline — so that sweep finds it.
		deadline := newFlushDeadline
		if deadline.IsZero() {
			deadline = time.Now().Add(maxFlushAllSkew)
		}
		if !c.queued {
			c.Ref()
			c.queued = true
		}
		flushQ.Schedule(c, deadline)
	}
}

// Must not already hold c.mu
func (c *halfBoundFunnel[T]) Flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "halfBoundFunnel.Flush"
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
func (c *halfBoundFunnel[T]) flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "halfBoundFunnel.flush"

	accumulator := c.accumulator
	if accumulator == nil {
		// already flushed, ignore
		return
	}
	c.accumulator = nil

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
	Funnel(ctx context.Context, flushQ *delayq.Queue[funnelFlusher], sender *rdvq.Sender)
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

func (w *funnelWork[T]) Funnel(ctx context.Context, flushQ *delayq.Queue[funnelFlusher], sender *rdvq.Sender) {
	var hbc *halfBoundFunnel[T]
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
		hbc = w.op.halfBoundFunnelPool.Get()
		hbc.mu.Lock()
		hbc.refCount = 1
		w.op.ref()
		w.op.instanceCount.Add(1)
		hbc.op = w.op
		hbc.id = funnelInstanceID(funnelInstanceCounter.Add(1))
		hbc.earliestGroup = w.Group()
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
	hbc.funnel(ctx, flushQ, sender, w.input, w.inputErr)
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
