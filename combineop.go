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
	id              combineOpID
	gatherOp        GatherOp[O]
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
		gatherOp:        gatherOp,
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
		trace.Logf(ctx, traceRegion, "CombineOp#%d", c.id)
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
	trace.Logf(ctx, traceRegion, "CombineOp#%d", c.id)

	ctx, meta := c.pool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	inner := c.refInner()
	defer inner.unref()

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
	trace.Logf(ctx, traceRegion, "CombineOp#%d", c.id)

	ctx, meta := c.pool.job.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	inner := c.refInner()
	defer inner.unref()

	return inner.tryIntegrate(ctx, meta, group, value, err, deadline)
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
	defer inner.unref()

	w := combineScatterWorkPool.Get()
	w.Init(group, c.pool, deadline, target, inner.newTask(group, taskFn))

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"CombineOp#%d created %v, inner=%p, instanceQueue=%p",
			c.id, w, inner, &inner.instanceQueue)
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
	inner.gatherOp = c.gatherOp
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
}

func (c *combineOp[I, O]) Reset() {
	if c.refCount != 0 {
		panic("reference count is not zero")
	}
	if c.instanceCount.Load() != 0 {
		panic("instance count is not zero")
	}
	if _, ok := c.instanceQueue.TryPopFront(); ok {
		panic("instance queue was not empty")
	}
	c.id = 0
	c.gatherOp = GatherOp[O]{}
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
	ct.op.unref() // Release reference from the task
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
	w.op.unref()
	pool.Put(w)
}
