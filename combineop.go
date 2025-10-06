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

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// CombineOp represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
// CombineOp extends the capabilities of GatherOp by aggregating task results
// through combiners before gathering.
//
// Thread-safety and copying: Like GatherOp, a CombineOp value is designed to be
// copied. While a single CombineOp value does not support concurrent calls to
// Scatter or TryScatter, copies of a CombineOp can be used concurrently. All
// copies share the same combiner identity and will route work to the same
// combiner instances. This allows CombineOp values to be safely passed by value
// to goroutines or stored in structures without losing their binding to the
// underlying combiner pool and operation identity.
type CombineOp[I, O any] struct {
	handleID combineOpHandleID
	inner    *combineOp[I, O]
}

// NewCombineOp creates a new CombineOp operation that uses the specified gather function,
// combiner pool, and combiner factory.
//
//nolint:contextcheck // background context used only for tracing
func NewCombineOp[I any, O any](
	gatherOp GatherOp[O],
	combinerPool *CombinerPool,
	combinerFactory psgfn.CombinerFactory[I, O],
) CombineOp[I, O] {
	traceRegion := "NewCombineOp"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if gatherOp.gatherFn == nil {
		panic("gatherOp is uninitialized")
	}
	if combinerPool == nil {
		panic("combinerPool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combinerFactory must be non-nil")
	}

	innerPool := omnipool.For[combineOp[I, O]]()
	id := combineOpID(combineOpCounter.Add(1))
	var inner *combineOp[I, O]
	for {
		inner = innerPool.Get()
		inner.mu.Lock()
		if inner.id == 0 {
			break
		}
		inner.mu.Unlock()
	}

	defer inner.mu.Unlock()

	inner.id = id

	// Register first handle
	handleID := combineOpHandleID(combineOpHandleCounter.Add(1))
	inner.handleIDs[handleID] = struct{}{}

	if inner.refCount != 0 {
		panic("unexpected nonzero inner.refCount")
	}
	if inner.gatherOp.gatherFn != nil {
		panic("unexpected non-nil inner.gatherOp.gatherFn")
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

	inner.refCount = 1
	inner.gatherOp = gatherOp
	inner.combinerPool = combinerPool
	inner.combinerFactory = combinerFactory
	inner.innerPool = innerPool

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "CombineOp#%d, handleID=%d, combineOp=%p, pool=%p",
			id, handleID, inner, combinerPool)
	}

	return CombineOp[I, O]{handleID: handleID, inner: inner}
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// combined using this Combine's combiner and eventually passed to the associated
// Gather.
//
// See [GatherOp.Scatter] for details about backpressure, concurrency limits,
// context handling, and error behavior.
func (c *CombineOp[I, O]) Scatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) error {
	traceRegion := "CombineOp.Scatter"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "CombineOp#%d", c.inner.id)
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

// TryScatter is like [CombineOp.Scatter] but returns instead of blocking if
// the given target is at its concurrency limit.
//
// See [GatherOp.TryScatter] for details about behavior and return values.
func (c *CombineOp[I, O]) TryScatter(
	ctx context.Context,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) (bool, error) {
	traceRegion := "CombineOp.TryScatter"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "CombineOp#%d", c.inner.id)
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

// Integrate posts values to be combined by the combine queue.
// This follows the same pattern as Scatter but for posting combine work instead
// of launching tasks.
func (c *CombineOp[I, O]) Integrate(
	ctx context.Context,
	value I,
	err error,
) error {
	traceRegion := "CombineOp.Integrate"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "CombineOp#%d", c.inner.id)

	inner := c.refInner()
	defer inner.unref(0)

	ctx, meta := inner.combinerPool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.integrate(ctx, meta, group, value, err)
}

// TryIntegrate attempts to post values to be combined by the combine queue.
// Like Integrate, but returns instead of blocking if queuing would be required.
func (c *CombineOp[I, O]) TryIntegrate(
	ctx context.Context,
	deadline time.Time,
	value I,
	err error,
) (bool, error) {
	traceRegion := "CombineOp.TryIntegrate"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "CombineOp#%d", c.inner.id)

	inner := c.refInner()
	defer inner.unref(0)

	ctx, meta := inner.combinerPool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return inner.tryIntegrate(ctx, meta, group, value, err, deadline)
}

func (c *CombineOp[I, O]) newScatterWork(
	group workq.GroupID,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) *combineScatterWork {
	traceRegion := "CombineOp.newScatterWork"

	inner := c.refInner()
	defer inner.unref(0)

	j := target.getJob()
	if j != inner.combinerPool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	w := combineScatterWorkPool.Get()
	w.Init(group, inner.combinerPool, deadline, target, inner.newTask(group, taskFn))

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"CombineOp#%d created %v, inner=%p, instanceQueue=%p",
			inner.id, w, inner, &inner.instanceQueue)
	}
	return w
}

// Dup creates a duplicate handle to the same underlying CombineOp.
// Like file descriptor duplication, this creates a new handle that shares
// the same underlying combiner state but requires its own Close() call.
// This is useful for passing CombineOp handles to different goroutines
// or async operations that need their own lifecycle management.
func (c *CombineOp[I, O]) Dup() CombineOp[I, O] {
	c.inner.mu.Lock()
	defer c.inner.mu.Unlock()

	// Check if this handle is still valid
	if _, ok := c.inner.handleIDs[c.handleID]; !ok {
		panic(fmt.Sprintf("Dup() called on closed handle %d", c.handleID))
	}

	// Create new handle
	handleID := combineOpHandleID(combineOpHandleCounter.Add(1))
	c.inner.handleIDs[handleID] = struct{}{}
	c.inner.refCount++

	return CombineOp[I, O]{
		handleID: handleID,
		inner:    c.inner,
	}
}

// Close releases this handle to the CombineOp. Each handle (including dups)
// must be closed exactly once. The underlying combiner state is cleaned up
// when the last handle is closed.
func (c *CombineOp[I, O]) Close() {
	if c.inner == nil {
		panic("operation not initialized")
	}
	c.inner.unref(c.handleID)
}

func (c *CombineOp[I, O]) refInner() *combineOp[I, O] {
	inner := c.inner
	if inner == nil {
		panic("operation not initialized")
	}

	inner.mu.Lock()
	defer inner.mu.Unlock()

	// Check if all handles have been closed
	if len(inner.handleIDs) == 0 {
		panic("operation closed")
	}

	// Check if this specific handle is still valid
	if _, ok := inner.handleIDs[c.handleID]; !ok {
		panic(fmt.Sprintf("Handle %d already closed", c.handleID))
	}

	inner.refCount++
	return inner
}

type combineOpID int64

var combineOpCounter atomic.Int64

type combineOpHandleID int64

var combineOpHandleCounter atomic.Int64

type combinerInstanceID int64

var combinerInstanceCounter atomic.Int64

type combineOp[I, O any] struct {
	mu       sync.Mutex
	id       combineOpID
	refCount int

	// Handle tracking for Dup/Close
	handleIDs map[combineOpHandleID]struct{}

	gatherOp        GatherOp[O]
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
	c.handleIDs = make(map[combineOpHandleID]struct{})
}

func (c *combineOp[I, O]) Reset() {
	// Reset logic is now handled in unref() while holding the mutex to prevent races.
	// We keep this empty method to satisfy the Resetter interface - if we didn't,
	// omnipool would zero the entire struct including the mutex, which would be bad.
}

func (c *combineOp[I, O]) ref() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refCount <= 0 {
		panic("expected existing references")
	}
	c.refCount++
}

// unref closes the handle if non-zero and always decrements the reference
// count. If this was the last reference, it also clears fields and returns to
// pool.
func (c *combineOp[I, O]) unref(handleID combineOpHandleID) {
	c.mu.Lock()
	needUnlock := true
	defer func() {
		if needUnlock {
			c.mu.Unlock()
		}
	}()

	// If handleID is non-zero, this is a Close() call
	if handleID != 0 {
		// Check if this handle was already closed
		if _, ok := c.handleIDs[handleID]; !ok {
			panic(fmt.Sprintf("Double close or value copy close on handle %d", handleID))
		}

		// Remove this handle
		delete(c.handleIDs, handleID)
	}

	if c.refCount <= 0 {
		panic("reference count underflow")
	}
	c.refCount--

	if c.refCount != 0 {
		return
	}

	// Last reference - clean up and return to pool

	// Validate cleanup invariants
	if c.instanceCount.Load() != 0 {
		panic("instance count is not zero")
	}
	if _, ok := c.instanceQueue.TryPopFront(); ok {
		panic("instance queue was not empty")
	}

	// Save innerPool before clearing
	innerPool := c.innerPool

	// Clear all fields while holding the mutex to prevent races
	c.id = 0 // Must clear to mark as available for reuse (checked in NewCombineOp)
	c.gatherOp = GatherOp[O]{}
	c.combinerPool = nil
	c.combinerFactory = nil
	// Keep c.innerPool - it's metadata about where to return this object
	// Clear handle map for reuse
	clear(c.handleIDs)

	needUnlock = false
	c.mu.Unlock()

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
	op.unref(0)
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
		trace.Logf(ctx, traceRegion, "CombineOp#%d returning new combiner=%v", c.op.id, c.combiner)
	}
}

func (c *halfBoundCombiner[I, O]) emit(ctx context.Context, sender *rdvq.Sender, output O, outputErr error) {
	traceRegion := "halfBoundCombiner.emit"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := c.op.combinerPool.job.ctxMeta(ctx)
	err := c.op.gatherOp.integrate(
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
	jobWork
	pool     *CombinerPool
	deadline time.Time
	target   TaskPoolOrJob
	taskPoolScatterWork
	task boundTask
}

func (w *combineScatterWork) Init(
	group workq.GroupID,
	pool *CombinerPool,
	deadline time.Time,
	target TaskPoolOrJob,
	task boundTask,
) {
	w.jobWork.Init(group, pool.job)
	w.pool = pool
	w.deadline = deadline
	w.target = target
	w.task = task
}

func (w *combineScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	workFn := func(ctx context.Context, ex workq.Execution) error {
		return w.target.scatter(ctx, w.Group(), ex, w.deadline, &w.taskPoolScatterWork, w.task)
	}

	defer func() {
		if ex.Started() {
			w.task = nil // we no longer own the task
		}
	}()

	bb := w.pool.job.protoBB
	if bb.ShouldBlock(ctx) != nil {
		jobGovernedWorkFn := func(ctx context.Context, ex workq.Execution) error {
			return w.pool.job.governor.Execute(ctx, ex, w.deadline, bb, workFn)
		}
		return w.pool.governor.Execute(ctx, ex, w.deadline, bb, jobGovernedWorkFn)
	} else {
		return workFn(ctx, ex)
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *combineScatterWork) Free() {
	traceRegion := "combineScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	if w.task != nil {
		w.task.Free()
	}
	w.Close(w.pool.job)
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
		intErr := ct.op.integrate(ctx, meta, ct.group, value, err)
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
	ct.op.unref(0) // Release reference from the task
	ct.pool.Put(ct)
}

func (c *combineOp[I, O]) integrate(
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

func (c *combineOp[I, O]) tryIntegrate(
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
	jobWork
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
	w.jobWork.Init(group, op.combinerPool.job)
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
	w.jobWork.Close(w.op.combinerPool.job)

	pool := w.op.combineWorkPool
	w.op.unref(0)
	pool.Put(w)
}
