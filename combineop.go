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

// combineOpHandleTrait implements leakguard.DupTrait for combineOp resources.
// It manages the lifecycle of combineOp instances through reference counting.
type combineOpHandleTrait[T any] struct{}

func (combineOpHandleTrait[T]) Close(c *combineOp[T]) {
	c.unref()
}

func (combineOpHandleTrait[T]) Dup(c *combineOp[T]) (*combineOp[T], error) {
	c.ref()
	return c, nil
}

func (combineOpHandleTrait[T]) String(c *combineOp[T]) string {
	return fmt.Sprintf("Combiner(%p)", c)
}

// Combiner represents a stateful aggregation op. Inputs flow in through
// [Combiner.Submit] (or via [Combiner.Start] for value-producing tasks);
// the user-supplied [psgfn.Accumulator] processes them inside a
// CombinerPool worker. Downstream emission is the Accumulator body's
// responsibility — it calls Submit on whatever downstream sinks it has
// captured. There is no framework-mediated output type; Accumulator
// errors are surfaced via the Pool's GatherAll path.
//
// Thread-safety and copying: a Combiner value is designed to be copied.
// While a single Combiner value does not support concurrent calls to
// Start or TryStart, copies of a Combiner can be used concurrently. All
// copies share the same combiner identity and will route work to the
// same Accumulator instances.
//
// Resource management: each Combiner must be explicitly closed via
// Close(). Dup() creates independent handles that share the same
// underlying state. The combineOp resource is cleaned up when the last
// handle is closed and all internal references (from tasks and work
// items) are released.
type Combiner[T any] struct {
	h leakguard.Handle[combineOp[T], combineOpHandleTrait[T]]
}

// NewCombiner creates a new Combiner operation. Pass [WithLimits] in
// opts to bind a [Limiter] (e.g. via [NewSemaphore]) that caps the
// number of concurrent combine-work executions for this Combiner.
//
// The framework manages an internal error sink that surfaces
// Accumulator errors through the Pool's GatherAll path; the user's
// Accumulator body is responsible for routing successful results via
// Submit on whatever downstream sinks it captures.
//
//nolint:contextcheck // background context used only for tracing
func NewCombiner[T any](
	combinerPool *CombinerPool,
	combinerFactory psgfn.CombinerFactory[T],
	opts ...OpOption,
) Combiner[T] {
	traceRegion := "NewCombiner"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if combinerPool == nil {
		panic("combinerPool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combinerFactory must be non-nil")
	}

	cfg := resolveOpConfig(opts)

	innerPool := omnipool.For[combineOp[T]]()
	inner := innerPool.Get()

	if inner.refCount.Load() != 0 {
		panic("unexpected nonzero inner.refCount")
	}
	if inner.combinerPool != nil {
		panic("unexpected non-nil inner.combinerPool")
	}
	if inner.combinerFactory != nil {
		panic("unexpected non-nil inner.combinerFactory")
	}
	if inner.instanceCount.Load() != 0 {
		panic("unexpected nonzero inner.instanceCount")
	}

	inner.refCount.Store(1)
	// Framework-owned error sink: Accumulator errors flow through this
	// Gatherer[struct{}] whose handler returns err as-is, surfacing via
	// the Pool's GatherAll path.
	inner.errSink = NewGatherer(func(ctx context.Context, _ struct{}, err error) error {
		return err
	})
	inner.combinerPool = combinerPool
	inner.combinerFactory = combinerFactory
	inner.limiter = cfg.singleLimiter()
	inner.innerPool = innerPool

	h := leakguard.New[combineOp[T], combineOpHandleTrait[T]](inner)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Combiner(%p), handleID=%d, pool=%p",
			inner, h.HandleID(), combinerPool)
	}

	return Combiner[T]{h: h}
}

// Submit posts a value to the Combiner. Sugar for SubmitErr with a
// nil error.
func (c *Combiner[T]) Submit(
	ctx context.Context,
	value T,
) error {
	return c.SubmitErr(ctx, value, nil)
}

// SubmitErr posts a (value, err) pair to the Combiner. err is
// delivered to the Accumulator alongside value; use nil when reporting
// a successful result.
func (c *Combiner[T]) SubmitErr(
	ctx context.Context,
	value T,
	err error,
) error {
	traceRegion := "Combiner.SubmitErr"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.refInner()
	defer inner.unref()
	trace.Logf(ctx, traceRegion, "Combiner(%p)", inner)

	ctx, meta := inner.combinerPool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.submit(ctx, meta, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline. See
// [Combiner.Submit].
func (c *Combiner[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value T,
) (bool, error) {
	return c.TrySubmitErr(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past deadline.
// See [Combiner.SubmitErr].
func (c *Combiner[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Combiner.TrySubmitErr"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.refInner()
	defer inner.unref()
	trace.Logf(ctx, traceRegion, "Combiner(%p)", inner)

	ctx, meta := inner.combinerPool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.trySubmit(ctx, meta, group, value, err, deadline)
}

// Dup creates a duplicate handle to the same underlying Combiner.
// Like file descriptor duplication, this creates a new handle that shares
// the same underlying combiner state but requires its own Close() call.
// This is useful for passing Combiner handles to different goroutines
// or async operations that need their own lifecycle management.
func (c *Combiner[T]) Dup() Combiner[T] {
	h, err := leakguard.Dup(c.h)
	if err != nil {
		panic(fmt.Sprintf("Dup() failed: %v", err))
	}
	return Combiner[T]{h: h}
}

// Close releases this handle to the Combiner. Each handle (including dups)
// must be closed exactly once. The underlying combiner state is cleaned up
// when the last handle is closed.
func (c *Combiner[T]) Close() {
	c.h.Close()
}

// refInner gets the inner combineOp, checks if closed, and adds a reference.
// Panics if the Combiner has been closed.
// The caller must ensure a matching unref() is called.
func (c *Combiner[T]) refInner() *combineOp[T] {
	inner := c.h.Get()
	if inner == nil {
		panic("Combiner has been closed")
	}
	inner.ref()
	return inner
}

type combinerInstanceID int64

var combinerInstanceCounter atomic.Int64

type combineOp[T any] struct {
	refCount atomic.Int64

	// errSink is framework-owned. Accumulator errors are routed through
	// it; its handler returns err as-is so it surfaces via GatherAll.
	errSink         Gatherer[struct{}]
	combinerPool    *CombinerPool
	combinerFactory psgfn.CombinerFactory[T]

	// limiter caps how many combineWorks this Combiner processes
	// concurrently. The zero Limiter (impl == nil) means unlimited.
	// Acquired in combineWork.Execute and released when Execute
	// completes.
	limiter Limiter

	innerPool             *omnipool.Pool[combineOp[T]]
	halfBoundCombinerPool *omnipool.Pool[halfBoundCombiner[T]]
	combineWorkPool       *omnipool.Pool[combineWork[T]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*halfBoundCombiner[T]]
}

func (c *combineOp[T]) Init() {
	c.halfBoundCombinerPool = omnipool.For[halfBoundCombiner[T]]()
	c.combineWorkPool = omnipool.For[combineWork[T]]()
	c.instanceQueue.Init()
}

func (c *combineOp[T]) Reset() {
	// Reset logic is now handled in unref() when refCount hits zero.
	// We keep this empty method to satisfy the Resetter interface - if we didn't,
	// omnipool would zero the entire struct including pool pointers set by Init().
}

// ref increments the refCount to track handle ownership and internal references.
// It is called by leakguard when a handle is created via Dup(), and also used
// for internal refs (tasks, work items).
func (c *combineOp[T]) ref() {
	newCount := c.refCount.Add(1)
	if newCount <= 1 {
		panic("ref() called with no existing references")
	}
}

// unref is called by leakguard when a handle is closed.
// It decrements refCount and cleans up if this was the last reference.
func (c *combineOp[T]) unref() {
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
	c.errSink = Gatherer[struct{}]{}
	c.combinerPool = nil
	c.combinerFactory = nil
	c.limiter = Limiter{}
	// Keep c.innerPool - it's metadata about where to return this object

	// Return to pool
	innerPool.Put(c)
}

type halfBoundCombiner[T any] struct {
	id combinerInstanceID
	op *combineOp[T]

	mu            sync.Mutex
	refCount      int
	earliestGroup workq.GroupID
	accumulator   psgfn.Accumulator[T]

	// queued reports whether this instance currently has a Ref held on
	// behalf of an in-flight Schedule on the CombinerPool's flushQ.
	// Mutated only under c.mu.
	queued bool

	// flushHeapPos is the 1-based position of this instance in the
	// CombinerPool's flush deadline queue (0 means not in the queue).
	// Mutated only by the queue.
	flushHeapPos int
}

func (c *halfBoundCombiner[T]) InstanceID() combinerInstanceID {
	return c.id
}

func (c *halfBoundCombiner[T]) InstanceCount() int {
	return int(c.op.instanceCount.Load())
}

// Position implements [delayq.Item]. The heap reads positions under
// delayq.mu so the read is consistent with the heap's own ordering;
// concurrent writers come exclusively through SetPosition, which
// synchronizes with combine via c.mu.
func (c *halfBoundCombiner[T]) Position() int { return c.flushHeapPos }

// SetPosition implements [delayq.Item]. It is called by the delayq
// heap under delayq.mu when an item is inserted, swapped, or removed.
// We take c.mu so combine's read of c.queued and c.flushHeapPos
// stays consistent with the heap's view: when delayq's Drain pops c
// (p == 0), the queued flag flips false here, ensuring a concurrent
// combine that subsequently acquires c.mu correctly observes "no Ref
// outstanding" and Refs for its new Schedule.
//
// Lock ordering: delayq.mu first (held by the heap operation), then
// c.mu (taken here). combine never takes delayq.mu while holding
// c.mu, so no deadlock.
func (c *halfBoundCombiner[T]) SetPosition(p int) {
	c.mu.Lock()
	c.flushHeapPos = p
	if p == 0 {
		c.queued = false
	}
	c.mu.Unlock()
}

// Must already be holding c.mu lock.
func (c *halfBoundCombiner[T]) Ref() {
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount++
}

// Must not be holding c.mu lock.
func (c *halfBoundCombiner[T]) Unref() {
	c.mu.Lock()
	finalRefDropped := c.unref()
	c.mu.Unlock()
	if finalRefDropped {
		c.free()
	}
}

// Must already be holding c.mu lock.
// Returns true if the final reference was dropped.
func (c *halfBoundCombiner[T]) unref() bool {
	// Must already be holding c.mu lock
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount--
	return c.refCount == 0
}

// A call to unref() must already have returned true
func (c *halfBoundCombiner[T]) free() {
	pool := c.op.halfBoundCombinerPool
	op := c.op
	pool.Put(c)
	op.instanceCount.Add(-1)
	op.unref()
}

func (c *halfBoundCombiner[T]) allocate(
	ctx context.Context,
	newAccumulator psgfn.CombinerFactory[T],
	sender *rdvq.Sender,
) {
	traceRegion := "halfBoundCombiner.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emitErr(ctx, sender, ErrCombinerFactoryPanicked)
		}
	}()
	c.accumulator = newAccumulator()
	panicked = false
	if c.accumulator == nil {
		c.emitErr(ctx, sender, ErrCombinerFactoryReturnedNil)
		c.accumulator = &errAccumulator[T]{err: ErrCombinerFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Combiner(%p) returning new accumulator=%v", c.op, c.accumulator)
	}
}

// emitErr surfaces an Accumulator error through the framework-owned error
// sink. The errSink's handler returns the error to the caller of
// Pool.GatherAll. Successful results are not surfaced this way — the
// Accumulator body is expected to Submit those to user-owned downstream
// sinks directly.
func (c *halfBoundCombiner[T]) emitErr(ctx context.Context, sender *rdvq.Sender, accErr error) {
	traceRegion := "halfBoundCombiner.emitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	if accErr == nil {
		return
	}
	ctx, meta := c.op.combinerPool.job.ctxMeta(ctx)
	err := c.op.errSink.submit(
		ctx, meta, c.op.combinerPool.job, c.earliestGroup, struct{}{}, accErr)
	if err != nil && ctx.Err() == nil {
		panic(fmt.Sprintf("unexpected non-cancelation error: %v", err))
	}
	_ = sender
}

func (c *halfBoundCombiner[T]) combine(
	ctx context.Context,
	flushQ *delayq.Queue[combinerFlusher],
	sender *rdvq.Sender,
	input T,
	inputErr error,
) {

	traceRegion := "halfBoundCombiner.combine"
	defer trace.StartRegion(ctx, traceRegion).End()

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, sender, ErrCombinePanicked)
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
		// CombinerPool's job-end flush sweep picks it up; we still
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
func (c *halfBoundCombiner[T]) Flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "halfBoundCombiner.Flush"
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
func (c *halfBoundCombiner[T]) flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "halfBoundCombiner.flush"

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
			c.emitErr(ctx, sender, ErrCombinerFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on accumulator=%v", accumulator)
	err := accumulator.Flush(ctx)
	panicked = false
	if err != nil {
		c.emitErr(ctx, sender, err)
	}
}

func (c *combineOp[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	combineWork := c.newCombineWork(group, value, err)
	postWork := c.combinerPool.newCombinePostWork(group, combineWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

func (c *combineOp[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	// Create combine work directly with values
	combineWork := c.newCombineWork(group, value, err)
	postWork := c.combinerPool.newCombinePostWork(group, combineWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}

// boundCombineWork interface allows type erasure for combineWork instances
type boundCombineWork interface {
	workq.Work
	Combine(ctx context.Context, flushQ *delayq.Queue[combinerFlusher], sender *rdvq.Sender)
	Waiting(*workq.Governor)
}

type combineWork[T any] struct {
	poolWork
	workq.DownstreamWork
	op       *combineOp[T]
	input    T
	inputErr error
}

func (c *combineOp[T]) newCombineWork(group workq.GroupID, value T, err error) *combineWork[T] {
	w := c.combineWorkPool.Get()
	w.Init(group, c, value, err)
	return w
}

func (w *combineWork[T]) Init(group workq.GroupID, op *combineOp[T], input T, inputErr error) {
	w.poolWork.Init(group, op.combinerPool.job)
	w.op = op
	w.input = input
	w.inputErr = inputErr
	op.combinerPool.inFlight.Increment()
	op.ref() // Add reference for the combine work
}

func (w *combineWork[T]) Combine(ctx context.Context, flushQ *delayq.Queue[combinerFlusher], sender *rdvq.Sender) {
	var hbc *halfBoundCombiner[T]
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
		hbc = w.op.halfBoundCombinerPool.Get()
		hbc.mu.Lock()
		hbc.refCount = 1
		w.op.ref()
		w.op.instanceCount.Add(1)
		hbc.op = w.op
		hbc.id = combinerInstanceID(combinerInstanceCounter.Add(1))
		hbc.earliestGroup = w.Group()
		hbc.allocate(ctx, w.op.combinerFactory, sender)
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
	hbc.combine(ctx, flushQ, sender, w.input, w.inputErr)
}

func (w *combineWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "combineWork(%p), %v", w, w)

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
		BlockBehavior: w.op.combinerPool.job.protoBB,
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

func (w *combineWork[T]) executeInner(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	workerCtx, meta := w.op.combinerPool.job.ctxMeta(ctx)
	cw := meta.executionEnvironment.(*cpWorker)
	cw.PushGroup(w.Group())
	defer cw.PopGroup()
	cw.executeCombine(workerCtx, w)
	return nil
}

func (w *combineWork[T]) Free() {
	traceRegion := "combineWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "combineWork(%p), %v", w, w)

	w.op.combinerPool.inFlight.Decrement()

	w.DownstreamWork.Close()
	w.poolWork.Close(w.op.combinerPool.job)

	pool := w.op.combineWorkPool
	w.op.unref()
	pool.Put(w)
}
