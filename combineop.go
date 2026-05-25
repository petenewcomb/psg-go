// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
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
	"github.com/petenewcomb/psg-go/psgfn"
)

// combineOpHandleTrait implements leakguard.DupTrait for combineOp resources.
// It manages the lifecycle of combineOp instances through reference counting.
type combineOpHandleTrait[I, O any] struct{}

func (combineOpHandleTrait[I, O]) Close(c *combineOp[I, O]) {
	c.unref()
}

func (combineOpHandleTrait[I, O]) Dup(c *combineOp[I, O]) (*combineOp[I, O], error) {
	c.ref()
	return c, nil
}

func (combineOpHandleTrait[I, O]) String(c *combineOp[I, O]) string {
	return fmt.Sprintf("Combiner(%p)", c)
}

// Combiner represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
// Combiner extends the capabilities of Gatherer by aggregating task results
// through combiners before gathering.
//
// Thread-safety and copying: Like Gatherer, a Combiner value is designed to be
// copied. While a single Combiner value does not support concurrent calls to
// Start or TryStart, copies of a Combiner can be used concurrently. All
// copies share the same combiner identity and will route work to the same
// combiner instances. This allows Combiner values to be safely passed by value
// to goroutines or stored in structures without losing their binding to the
// underlying combiner pool and operation identity.
//
// Resource management: Combiner uses leakguard for safe handle management.
// Each Combiner must be explicitly closed via Close(). Dup() creates independent
// handles that share the same underlying state. The combineOp resource is
// cleaned up when the last handle is closed and all internal references
// (from tasks and work items) are released.
type Combiner[I, O any] struct {
	h leakguard.Handle[combineOp[I, O], combineOpHandleTrait[I, O]]
}

// NewCombiner creates a new Combiner operation that uses the specified gather function,
// combiner pool, and combiner factory.
//
//nolint:contextcheck // background context used only for tracing
func NewCombiner[I any, O any](
	gatherer Gatherer[O],
	combinerPool *CombinerPool,
	combinerFactory psgfn.CombinerFactory[I, O],
) Combiner[I, O] {
	traceRegion := "NewCombiner"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if gatherer.gatherFn == nil {
		panic("gatherer is uninitialized")
	}
	if combinerPool == nil {
		panic("combinerPool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combinerFactory must be non-nil")
	}

	innerPool := omnipool.For[combineOp[I, O]]()
	inner := innerPool.Get()

	if inner.refCount.Load() != 0 {
		panic("unexpected nonzero inner.refCount")
	}
	if inner.gatherer.gatherFn != nil {
		panic("unexpected non-nil inner.gatherer.gatherFn")
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
	inner.gatherer = gatherer
	inner.combinerPool = combinerPool
	inner.combinerFactory = combinerFactory
	inner.innerPool = innerPool

	h := leakguard.New[combineOp[I, O], combineOpHandleTrait[I, O]](inner)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Combiner(%p), handleID=%d, pool=%p",
			inner, h.HandleID(), combinerPool)
	}

	return Combiner[I, O]{h: h}
}

// Start initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// combined using this Combine's combiner and eventually passed to the associated
// Gather.
//
// See [Gatherer.Start] for details about backpressure, concurrency limits,
// context handling, and error behavior.
func (c *Combiner[I, O]) Start(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) error {
	traceRegion := "Combiner.Start"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.h.Get()
	if inner == nil {
		panic("Combiner has been closed")
	}
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Combiner(%p)", inner)
	}

	ctx, meta := vetScatter(ctx, target, taskFn)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := c.newScatterWork(group, time.Time{}, target, taskFn)
	return meta.ExecuteNowOrQueue(ctx, work)
}

// TryStart is like [Combiner.Start] but returns instead of blocking if
// the given target is at its concurrency limit.
//
// See [Gatherer.TryStart] for details about behavior and return values.
func (c *Combiner[I, O]) TryStart(
	ctx context.Context,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) (bool, error) {
	traceRegion := "Combiner.TryStart"
	defer trace.StartRegion(ctx, traceRegion).End()
	inner := c.h.Get()
	if inner == nil {
		panic("Combiner has been closed")
	}
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Combiner(%p)", inner)
	}

	ctx, meta := vetScatter(ctx, target, taskFn)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	work := c.newScatterWork(group, deadline, target, taskFn)
	ok, err := meta.TryExecuteNow(ctx, deadline, work)
	if !ok {
		work.Free()
	}
	return ok, err
}

// Submit posts values to be combined by the combine queue.
// This follows the same pattern as Start but for posting combine work instead
// of launching tasks.
func (c *Combiner[I, O]) Submit(
	ctx context.Context,
	value I,
	err error,
) error {
	traceRegion := "Combiner.Submit"
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

// TrySubmit attempts to post values to be combined by the combine queue.
// Like Submit, but returns instead of blocking if queuing would be required.
func (c *Combiner[I, O]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value I,
	err error,
) (bool, error) {
	traceRegion := "Combiner.TrySubmit"
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

func (c *Combiner[I, O]) newScatterWork(
	group workq.GroupID,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) *combineScatterWork {
	traceRegion := "Combiner.newScatterWork"

	inner := c.refInner()
	defer inner.unref()

	j := target.getJob()
	if j != inner.combinerPool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	targetScatterWork := target.newScatterWork(group, deadline, inner.newTask(group, taskFn))
	w := newCombineScatterWork(inner.combinerPool, group, deadline, targetScatterWork)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"Combiner(%p) created %v, instanceQueue=%p",
			inner, w, &inner.instanceQueue)
	}
	return w
}

// Dup creates a duplicate handle to the same underlying Combiner.
// Like file descriptor duplication, this creates a new handle that shares
// the same underlying combiner state but requires its own Close() call.
// This is useful for passing Combiner handles to different goroutines
// or async operations that need their own lifecycle management.
func (c *Combiner[I, O]) Dup() Combiner[I, O] {
	h, err := leakguard.Dup(c.h)
	if err != nil {
		panic(fmt.Sprintf("Dup() failed: %v", err))
	}
	return Combiner[I, O]{h: h}
}

// Close releases this handle to the Combiner. Each handle (including dups)
// must be closed exactly once. The underlying combiner state is cleaned up
// when the last handle is closed.
func (c *Combiner[I, O]) Close() {
	c.h.Close()
}

// refInner gets the inner combineOp, checks if closed, and adds a reference.
// Panics if the Combiner has been closed.
// The caller must ensure a matching unref() is called.
func (c *Combiner[I, O]) refInner() *combineOp[I, O] {
	inner := c.h.Get()
	if inner == nil {
		panic("Combiner has been closed")
	}
	inner.ref()
	return inner
}

type combinerInstanceID int64

var combinerInstanceCounter atomic.Int64

type combineOp[I, O any] struct {
	refCount atomic.Int64

	gatherer        Gatherer[O]
	combinerPool    *CombinerPool
	combinerFactory psgfn.CombinerFactory[I, O]

	innerPool             *omnipool.Pool[combineOp[I, O]]
	halfBoundCombinerPool *omnipool.Pool[halfBoundCombiner[I, O]]
	taskPool              *omnipool.Pool[combineTask[I, O]]
	combineWorkPool       *omnipool.Pool[combineWork[I, O]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*halfBoundCombiner[I, O]]
}

func (c *combineOp[I, O]) Init() {
	c.halfBoundCombinerPool = omnipool.For[halfBoundCombiner[I, O]]()
	c.taskPool = omnipool.For[combineTask[I, O]]()
	c.combineWorkPool = omnipool.For[combineWork[I, O]]()
	c.instanceQueue.Init()
}

func (c *combineOp[I, O]) Reset() {
	// Reset logic is now handled in unref() when refCount hits zero.
	// We keep this empty method to satisfy the Resetter interface - if we didn't,
	// omnipool would zero the entire struct including pool pointers set by Init().
}

// ref increments the refCount to track handle ownership and internal references.
// It is called by leakguard when a handle is created via Dup(), and also used
// for internal refs (tasks, work items).
func (c *combineOp[I, O]) ref() {
	newCount := c.refCount.Add(1)
	if newCount <= 1 {
		panic("ref() called with no existing references")
	}
}

// unref is called by leakguard when a handle is closed.
// It decrements refCount and cleans up if this was the last reference.
func (c *combineOp[I, O]) unref() {
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
	c.gatherer = Gatherer[O]{}
	c.combinerPool = nil
	c.combinerFactory = nil
	// Keep c.innerPool - it's metadata about where to return this object

	// Return to pool
	innerPool.Put(c)
}

type halfBoundCombiner[I, O any] struct {
	id combinerInstanceID
	op *combineOp[I, O]

	mu            sync.Mutex
	refCount      int
	earliestGroup workq.GroupID
	combiner      psgfn.Combiner[I, O]
}

func (c *halfBoundCombiner[I, O]) InstanceID() combinerInstanceID {
	return c.id
}

func (c *halfBoundCombiner[I, O]) InstanceCount() int {
	return int(c.op.instanceCount.Load())
}

// Must already be holding c.mu lock.
func (c *halfBoundCombiner[I, O]) Ref() {
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount++
}

// Must not be holding c.mu lock.
func (c *halfBoundCombiner[I, O]) Unref() {
	c.mu.Lock()
	finalRefDropped := c.unref()
	c.mu.Unlock()
	if finalRefDropped {
		c.free()
	}
}

// Must already be holding c.mu lock.
// Returns true if the final reference was dropped.
func (c *halfBoundCombiner[I, O]) unref() bool {
	// Must already be holding c.mu lock
	if c.refCount < 1 {
		panic("reference count underflow")
	}
	c.refCount--
	return c.refCount == 0
}

// A call to unref() must already have returned true
func (c *halfBoundCombiner[I, O]) free() {
	pool := c.op.halfBoundCombinerPool
	op := c.op
	pool.Put(c)
	op.instanceCount.Add(-1)
	op.unref()
}

func (c *halfBoundCombiner[I, O]) allocate(
	ctx context.Context,
	newCombiner psgfn.CombinerFactory[I, O],
	sender *rdvq.Sender,
) {
	traceRegion := "halfBoundCombiner.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emit(ctx, sender, *new(O), ErrCombinerFactoryPanicked)
		}
	}()
	c.combiner = newCombiner()
	panicked = false
	if c.combiner == nil {
		c.emit(ctx, sender, *new(O), ErrCombinerFactoryReturnedNil)
		c.combiner = &errCombiner[I, O]{err: ErrCombinerFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Combiner(%p) returning new combiner=%v", c.op, c.combiner)
	}
}

func (c *halfBoundCombiner[I, O]) emit(ctx context.Context, sender *rdvq.Sender, output O, outputErr error) {
	traceRegion := "halfBoundCombiner.emit"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := c.op.combinerPool.job.ctxMeta(ctx)
	err := c.op.gatherer.submit(
		ctx, meta, c.op.combinerPool.job, c.earliestGroup, output, outputErr)
	if err != nil && ctx.Err() == nil {
		panic(fmt.Sprintf("unexpected non-cancelation error: %v", err))
	}
}

func (c *halfBoundCombiner[I, O]) combine(
	ctx context.Context,
	cm *activeCombinerMap,
	sender *rdvq.Sender,
	input I,
	inputErr error,
) {

	traceRegion := "halfBoundCombiner.combine"
	defer trace.StartRegion(ctx, traceRegion).End()

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emit(ctx, sender, *new(O), ErrCombinePanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Combine on combiner=%v", c.combiner)
	newFlushDeadline, err := c.combiner.Combine(ctx, input, inputErr)
	didNotPanic = true

	if err != nil {
		c.emit(ctx, sender, *new(O), err)
	}

	if !newFlushDeadline.IsZero() && time.Until(newFlushDeadline) <= 0 {
		cm.Remove(c)
		c.flush(ctx, sender)
	} else {
		cm.Push(c, newFlushDeadline)
	}
}

// Must not already hold c.mu
func (c *halfBoundCombiner[I, O]) Flush(ctx context.Context, sender *rdvq.Sender) {
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
func (c *halfBoundCombiner[I, O]) flush(ctx context.Context, sender *rdvq.Sender) {
	traceRegion := "halfBoundCombiner.flush"

	combiner := c.combiner
	if combiner == nil {
		// already flushed, ignore
		return
	}
	c.combiner = nil

	panicked := true // Assume the worst
	defer func() {
		if panicked {
			// Just in case the panic is otherwise suppressed
			c.emit(ctx, sender, *new(O), ErrCombinerFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on combiner=%v", combiner)
	v, err := combiner.Flush(ctx)
	panicked = false
	if !errors.Is(err, psgfn.ErrDoNotGather) {
		c.emit(ctx, sender, v, err)
	}
}

type combineScatterWork struct {
	workq.Work
	pool     *CombinerPool
	deadline time.Time
}

func newCombineScatterWork(
	pool *CombinerPool,
	group workq.GroupID,
	deadline time.Time,
	targetScatterWork workq.Work,
) *combineScatterWork {
	w := combineScatterWorkPool.Get()
	w.Work = targetScatterWork
	w.pool = pool
	w.deadline = deadline
	return w
}

func (w *combineScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	workFn := w.Work.Execute
	bb := w.pool.job.protoBB
	if bb.ShouldBlock(ctx) != nil {
		poolGovernedWorkFn := func(ctx context.Context, ex workq.Execution) error {
			return w.pool.job.governor.Execute(ctx, ex, w.deadline, bb, workFn)
		}
		return w.pool.governor.Execute(ctx, ex, w.deadline, bb, poolGovernedWorkFn)
	}
	return workFn(ctx, ex)
}

//nolint:contextcheck // background context used only for tracing
func (w *combineScatterWork) Free() {
	traceRegion := "combineScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Work.Free()
	combineScatterWorkPool.Put(w)
}

var combineScatterWorkPool = omnipool.For[combineScatterWork]()

type combineTask[I, O any] struct {
	group  workq.GroupID
	taskFn psgfn.Task[I]
	op     *combineOp[I, O]
	pool   *omnipool.Pool[combineTask[I, O]]
}

func (c *combineOp[I, O]) newTask(group workq.GroupID, taskFn psgfn.Task[I]) boundTask {
	ct := c.taskPool.Get()
	ct.pool = c.taskPool
	ct.group = group
	ct.taskFn = taskFn
	ct.op = c
	c.ref() // Add reference for the task
	return ct
}

func (ct *combineTask[I, O]) Execute(
	ctx context.Context,
	group workq.GroupID,
	completedFn func(),
	taskWorkerSender *rdvq.Sender,
) {
	traceRegion := "combineTask.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var value I
	var err error = ErrTaskPanicked
	defer func() {
		traceRegion := traceRegion + ".defer"
		defer trace.StartRegion(ctx, traceRegion).End()
		if completedFn != nil {
			completedFn()
		}
		if err != nil {
			trace.Logf(ctx, traceRegion, "posting task err=%v", err)
		}
		ctx, meta := ct.op.combinerPool.job.ctxMeta(ctx)
		intErr := ct.op.submit(ctx, meta, ct.group, value, err)
		if intErr != nil && ctx.Err() == nil {
			panic(fmt.Sprintf("unexpected non-cancelation error: %v", intErr))
		}
	}()

	trace.WithRegion(ctx, traceRegion+".taskFn", func() {
		value, err = ct.taskFn(ctx)
	})
}

func (ct *combineTask[I, O]) Free() {
	traceRegion := "combineTask.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	ct.op.unref() // Release reference from the task
	ct.pool.Put(ct)
}

func (c *combineOp[I, O]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value I,
	err error,
) error {
	combineWork := c.newCombineWork(group, value, err)
	postWork := c.combinerPool.newCombinePostWork(group, combineWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

func (c *combineOp[I, O]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value I,
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
	Combine(ctx context.Context, cm *activeCombinerMap, sender *rdvq.Sender)
	Waiting(*workq.Governor)
}

type combineWork[I, O any] struct {
	poolWork
	workq.DownstreamWork
	op       *combineOp[I, O]
	input    I
	inputErr error
}

func (c *combineOp[I, O]) newCombineWork(group workq.GroupID, value I, err error) *combineWork[I, O] {
	w := c.combineWorkPool.Get()
	w.Init(group, c, value, err)
	return w
}

func (w *combineWork[I, O]) Init(group workq.GroupID, op *combineOp[I, O], input I, inputErr error) {
	w.poolWork.Init(group, op.combinerPool.job)
	w.op = op
	w.input = input
	w.inputErr = inputErr
	op.combinerPool.inFlight.Increment()
	op.ref() // Add reference for the combine work
}

func (w *combineWork[I, O]) Combine(ctx context.Context, cm *activeCombinerMap, sender *rdvq.Sender) {
	var hbc *halfBoundCombiner[I, O]
	for {
		hbc, _ = w.op.instanceQueue.TryPopFront()
		if hbc == nil {
			break
		}
		hbc.mu.Lock()
		if hbc.combiner != nil {
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
		flushed := hbc.combiner == nil
		finalRefDropped := flushed && hbc.unref()
		hbc.mu.Unlock()
		if !flushed {
			w.op.instanceQueue.PushBack(hbc)
		} else if finalRefDropped {
			hbc.free()
		}
	}()
	hbc.combine(ctx, cm, sender, w.input, w.inputErr)
}

func (w *combineWork[I, O]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "combineWork(%p), %v", w, w)

	ex.Starting()
	workerCtx, meta := w.op.combinerPool.job.ctxMeta(ctx)
	cw := meta.executionEnvironment.(*cpWorker)
	cw.PushGroup(w.Group())
	defer cw.PopGroup()
	cw.executeCombine(workerCtx, w)
	return nil
}

func (w *combineWork[I, O]) Free() {
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
