// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/nbcq"
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
	id              combineOpID
	gatherFn        psgfn.Gather[O]
	pool            *CombinerPool
	combinerFactory psgfn.CombinerFactory[I, O]

	innerPool *omnipool.Pool[combineOp[I, O]]
	inner     *combineOp[I, O]
}

// NewCombineOp creates a new CombineOp operation that uses the specified gather function,
// combiner pool, and combiner factory.
//
//nolint:contextcheck // background context used only for tracing
func NewCombineOp[I, O any](
	gatherOp GatherOp[O],
	pool *CombinerPool,
	combinerFactory psgfn.CombinerFactory[I, O],
) CombineOp[I, O] {
	traceRegion := "NewCombineOp"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if gatherOp.gatherFn == nil {
		panic("gatherOp is uninitialized")
	}
	if pool == nil {
		panic("combiner pool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combiner factory must be non-nil")
	}
	c := CombineOp[I, O]{
		id:              combineOpID(combineOpCounter.Add(1)),
		gatherFn:        gatherOp.gatherFn,
		pool:            pool,
		combinerFactory: combinerFactory,
	}

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "CombineOp#%d, pool=%p", c.id, pool)
	}

	return c
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
		trace.Logf(ctx, traceRegion, "CombineOp#%d", c.id)
	}

	ctx, meta := vetScatter(ctx, target, taskFn)
	group := meta.CurrentGroup()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}
	work := c.newScatterWork(group, time.Time{}, target, taskFn)
	return scatterNow(ctx, meta, target, work)
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
	trace.Logf(ctx, traceRegion, "CombineOp#%d", c.id)

	ctx, meta := vetScatter(ctx, target, taskFn)
	group := meta.CurrentGroup()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}
	work := c.newScatterWork(group, deadline, target, taskFn)
	return tryScatterNow(ctx, meta, deadline, target, work)
}

func (c *CombineOp[I, O]) newScatterWork(
	group workq.GroupID,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) *combineScatterWork {
	traceRegion := "CombineOp.newScatterWork"

	j := target.getJob()
	if j != c.pool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	inner := c.refInner()

	pc := inner.instancePool.Get()
	pc.op = inner

	w := combineScatterWorkPool.Get()
	w.Init(group, c.pool, deadline, target, bindTaskFunc(group, j, taskFn, pc.postResultFn))

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"CombineOp#%d created %v, pooledCombine=%p, inner=%p, instanceQueue=%p",
			c.id, w, pc, inner, &inner.instanceQueue)
	}
	return w
}

func (c *CombineOp[I, O]) refInner() *combineOp[I, O] {
	inner := c.inner
	if inner != nil && inner.tryRefAs(c.id) {
		return inner
	}

	c.pool.combineOpMapMu.Lock()
	innerAny := c.pool.combineOpMap[c.id]
	c.pool.combineOpMapMu.Unlock()
	if innerAny != nil {
		inner = innerAny.(*combineOp[I, O])
		if inner.tryRefAs(c.id) {
			return inner
		}
	}

	innerPool := c.innerPool
	if innerPool == nil {
		innerPool = omnipool.For[combineOp[I, O]]()
		c.innerPool = innerPool
	}

	inner = innerPool.Get()
	// inner might still be referenced by a different CombineOp, but the above
	// inner.id == c.id block will ensure that it is not used again by that
	// CombineOp because unref() below set the id to zero before putting it back
	// in the pool.
	inner.mu.Lock()
	inner.id = c.id
	inner.refCount = 1
	inner.gatherFn = c.gatherFn
	inner.combinerPool = c.pool
	inner.combinerFactory = c.combinerFactory
	inner.innerPool = innerPool
	inner.mu.Unlock()

	c.pool.combineOpMapMu.Lock()
	innerAny = c.pool.combineOpMap[c.id]
	if innerAny != nil {
		existingInner := innerAny.(*combineOp[I, O])
		if existingInner.tryRefAs(c.id) {
			c.pool.combineOpMapMu.Unlock()
			inner.unref()
			return existingInner
		}
	}
	if c.pool.combineOpMap == nil {
		c.pool.combineOpMap = make(map[combineOpID]any)
	}
	c.pool.combineOpMap[c.id] = inner
	c.pool.combineOpMapMu.Unlock()
	return inner
}

type combineOpID int64

var combineOpCounter atomic.Int64

type combinerInstanceID int64

var combinerInstanceCounter atomic.Int64

type combineOp[I, O any] struct {
	mu       sync.Mutex
	id       combineOpID
	refCount int

	gatherFn        psgfn.Gather[O]
	combinerPool    *CombinerPool
	combinerFactory psgfn.CombinerFactory[I, O]

	innerPool             *omnipool.Pool[combineOp[I, O]]
	instancePool          *omnipool.Pool[pooledCombine[I, O]]
	halfBoundCombinerPool *omnipool.Pool[halfBoundCombiner[I, O]]
	boundGatherPool       *omnipool.Pool[boundGather[O]]

	instanceCount atomic.Int32
	instanceQueue nbcq.Queue[*halfBoundCombiner[I, O]]
}

func (c *combineOp[I, O]) Init() {
	c.instancePool = omnipool.For[pooledCombine[I, O]]()
	c.halfBoundCombinerPool = omnipool.For[halfBoundCombiner[I, O]]()
	c.boundGatherPool = omnipool.For[boundGather[O]]()
	c.instanceQueue.Init()
}

func (c *combineOp[I, O]) Reset() {
	if c.refCount != 0 {
		panic("reference count is not zero")
	}
	if c.instanceCount.Load() != 0 {
		panic("instance count is not zero")
	}
	if _, ok := c.instanceQueue.PopFront(); ok {
		panic("instance queue was not empty")
	}
	c.id = 0
	c.gatherFn = nil
	c.combinerPool = nil
	c.combinerFactory = nil
	c.innerPool = nil
}

func (c *combineOp[I, O]) tryRefAs(id combineOpID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id != id {
		return false
	}
	if c.refCount <= 0 {
		panic("expected existing references")
	}
	c.refCount++
	return true
}

func (c *combineOp[I, O]) ref() {
	c.mu.Lock()
	if c.refCount <= 0 {
		panic("expected existing references")
	}
	c.refCount++
	c.mu.Unlock()
}

func (c *combineOp[I, O]) unref() {
	c.mu.Lock()
	if c.refCount <= 0 {
		panic("reference count underflow")
	}
	c.refCount--
	if c.refCount != 0 {
		c.mu.Unlock()
		return
	}

	id := c.id
	combinerPool := c.combinerPool
	c.id = 0
	innerPool := c.innerPool
	c.mu.Unlock()

	combinerPool.combineOpMapMu.Lock()
	delete(combinerPool.combineOpMap, id)
	combinerPool.combineOpMapMu.Unlock()

	innerPool.Put(c)
}

type pooledCombine[I, O any] struct {
	op *combineOp[I, O]

	group    workq.GroupID
	input    I
	inputErr error

	// avoid closure reallocation
	postResultFn func(
		ctx context.Context,
		group workq.GroupID,
		j *Job,
		taskWorkerOutboxMap *outboxMap,
		input I,
		inputErr error,
	)
	boundCombineFn boundCombineFunc
}

func (pc *pooledCombine[I, O]) Init() {
	pc.postResultFn = pc.postResult
	pc.boundCombineFn = pc.execute
}

func (pc *pooledCombine[I, O]) Reset() {
	if pc.op != nil {
		pc.op.unref()
	}
	*pc = pooledCombine[I, O]{
		postResultFn:   pc.postResultFn,
		boundCombineFn: pc.boundCombineFn,
	}
}

func (pc *pooledCombine[I, O]) postResult(
	ctx context.Context,
	group workq.GroupID,
	j *Job,
	taskWorkerOutboxMap *outboxMap,
	input I,
	inputErr error,
) {
	traceRegion := "pooledCombine.postResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Post the combine using the task worker's outbox for the pool's combine queue
	combineOutbox := OutboxFor[workq.Work](taskWorkerOutboxMap, pc.op.combinerPool.combineOutboxKey())
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "CombineOp#%d, outbox=%p", pc.op.id, combineOutbox)
	}

	pc.group = group
	pc.input = input
	pc.inputErr = inputErr
	pc.op.combinerPool.postCombine(ctx, group, combineOutbox, pc.boundCombineFn)
}

func (pc *pooledCombine[I, O]) execute(ctx context.Context, cm *activeCombinerMap, emitOutbox *workq.Outbox) {
	var hbc *halfBoundCombiner[I, O]
	for {
		hbc, _ = pc.op.instanceQueue.PopFront()
		if hbc == nil {
			break
		}
		hbc.mu.Lock()
		if hbc.combiner != nil {
			if pc.group < hbc.earliestGroup {
				hbc.earliestGroup = pc.group
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
		hbc = pc.op.halfBoundCombinerPool.Get()
		hbc.mu.Lock()
		hbc.refCount = 1
		pc.op.ref()
		pc.op.instanceCount.Add(1)
		hbc.op = pc.op
		hbc.id = combinerInstanceID(combinerInstanceCounter.Add(1))
		hbc.earliestGroup = pc.group
		hbc.allocate(ctx, pc.op.combinerFactory, emitOutbox)
	}
	defer func() {
		flushed := hbc.combiner == nil
		finalRefDropped := flushed && hbc.unref()
		hbc.mu.Unlock()
		if !flushed {
			pc.op.instanceQueue.PushBack(hbc)
		} else if finalRefDropped {
			hbc.free()
		}
		pc.op.instancePool.Put(pc)
	}()
	hbc.combine(ctx, cm, emitOutbox, pc.input, pc.inputErr)
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
	emitOutbox *workq.Outbox,
) {
	traceRegion := "halfBoundCombiner.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emit(ctx, emitOutbox, *new(O), ErrCombinerFactoryPanicked)
		}
	}()
	c.combiner = newCombiner()
	panicked = false
	if c.combiner == nil {
		c.emit(ctx, emitOutbox, *new(O), ErrCombinerFactoryReturnedNil)
		c.combiner = &errCombiner[I, O]{err: ErrCombinerFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "CombineOp#%d returning new combiner=%v", c.op.id, c.combiner)
	}
}

func (c *halfBoundCombiner[I, O]) emit(ctx context.Context, emitOutbox *workq.Outbox, output O, outputErr error) {
	traceRegion := "halfBoundCombiner.emit"
	defer trace.StartRegion(ctx, traceRegion).End()

	g := c.op.boundGatherPool.Get()
	g.pool = c.op.boundGatherPool
	g.gatherFn = c.op.gatherFn
	g.value = output
	g.err = outputErr

	c.op.combinerPool.job.postGather(ctx, c.earliestGroup, emitOutbox, g.boundGatherFn)
}

func (c *halfBoundCombiner[I, O]) combine(
	ctx context.Context,
	cm *activeCombinerMap,
	emitOutbox *workq.Outbox,
	input I,
	inputErr error,
) {

	traceRegion := "halfBoundCombiner.combine"
	defer trace.StartRegion(ctx, traceRegion).End()

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emit(ctx, emitOutbox, *new(O), ErrCombinePanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Combine on combiner=%v", c.combiner)
	newFlushDeadline, err := c.combiner.Combine(ctx, input, inputErr)
	didNotPanic = true

	if err != nil {
		c.emit(ctx, emitOutbox, *new(O), err)
	}

	if !newFlushDeadline.IsZero() && time.Until(newFlushDeadline) <= 0 {
		cm.Remove(c)
		c.flush(ctx, emitOutbox)
	} else {
		cm.Push(c, newFlushDeadline)
	}
}

// Must not already hold c.mu
func (c *halfBoundCombiner[I, O]) Flush(ctx context.Context, emitOutbox *workq.Outbox) {
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
	c.flush(ctx, emitOutbox)
}

// Must already hold c.mu
func (c *halfBoundCombiner[I, O]) flush(ctx context.Context, emitOutbox *workq.Outbox) {
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
			c.emit(ctx, emitOutbox, *new(O), ErrCombinerFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on combiner=%v", combiner)
	v, err := combiner.Flush(ctx)
	panicked = false
	if !errors.Is(err, psgfn.ErrDoNotGather) {
		c.emit(ctx, emitOutbox, v, err)
	}
}

type combineScatterWork struct {
	jobWork
	pool     *CombinerPool
	deadline time.Time
	target   TaskPoolOrJob
	taskPoolScatterWork
	taskFn boundTaskFunc
}

func (w *combineScatterWork) Init(
	group workq.GroupID,
	pool *CombinerPool,
	deadline time.Time,
	target TaskPoolOrJob,
	taskFn boundTaskFunc,
) {
	w.jobWork.Init(group, pool.job)
	w.pool = pool
	w.deadline = deadline
	w.target = target
	w.taskFn = taskFn
}

func (w *combineScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	workFn := func(ctx context.Context, ex workq.Execution) error {
		return w.target.scatter(ctx, w.Group(), ex, w.deadline, &w.taskPoolScatterWork, w.taskFn)
	}

	bb := w.pool.job.protoBB
	if bb.ShouldBlock(ctx) != nil {
		return w.pool.governor.Execute(ctx, ex, w.deadline, bb, workFn)
	} else {
		return workFn(ctx, ex)
	}
}

func (w *combineScatterWork) Free() {
	traceRegion := "combineScatterWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Close(w.pool.job)
	combineScatterWorkPool.Put(w)
}

var combineScatterWorkPool = omnipool.For[combineScatterWork]()
