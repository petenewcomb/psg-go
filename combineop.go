// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// CombineOp represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
type CombineOp[I, O any] struct {
	id                    combineOpID
	gatherFn              psgfn.Gather[O]
	pool                  *CombinerPool
	newCombiner           psgfn.CombinerFactory[I, O]
	instancePool          *omnipool.Pool[pooledCombine[I, O]]
	halfBoundCombinerPool *omnipool.Pool[halfBoundCombiner[I, O]]
	boundGatherPool       *omnipool.Pool[boundGather[O]]
}

type combineOpID int64

var combineOpCounter atomic.Int64

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
		id:                    combineOpID(combineOpCounter.Add(1)),
		gatherFn:              gatherOp.gatherFn,
		pool:                  pool,
		newCombiner:           combinerFactory,
		instancePool:          omnipool.For[pooledCombine[I, O]](),
		halfBoundCombinerPool: omnipool.For[halfBoundCombiner[I, O]](),
		boundGatherPool:       omnipool.For[boundGather[O]](),
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
func (c CombineOp[I, O]) Scatter(
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
func (c CombineOp[I, O]) TryScatter(
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

func (c CombineOp[I, O]) newScatterWork(
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

	pc := c.instancePool.Get()
	pc.instancePool = c.instancePool
	pc.halfBoundCombinerPool = c.halfBoundCombinerPool
	pc.boundGatherPool = c.boundGatherPool
	pc.opID = c.id
	pc.gatherFn = c.gatherFn
	pc.combinerPool = c.pool
	pc.newCombiner = c.newCombiner

	w := combineScatterWorkPool.Get()
	w.Init(group, c.pool, deadline, target, bindTaskFunc(group, j, taskFn, pc.postResultFn))

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "CombineOp#%d created %v, pooledCombine=%p", c.id, w, pc)
	}
	return w
}

type pooledCombine[I, O any] struct {
	instancePool          *omnipool.Pool[pooledCombine[I, O]]
	halfBoundCombinerPool *omnipool.Pool[halfBoundCombiner[I, O]]
	boundGatherPool       *omnipool.Pool[boundGather[O]]
	opID                  combineOpID
	gatherFn              psgfn.Gather[O]
	combinerPool          *CombinerPool
	newCombiner           psgfn.CombinerFactory[I, O]

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
	combineOutbox := OutboxFor[workq.Work](taskWorkerOutboxMap, pc.combinerPool.combineOutboxKey())
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "CombineOp#%d, outbox=%p", pc.opID, combineOutbox)
	}

	pc.group = group
	pc.input = input
	pc.inputErr = inputErr
	pc.combinerPool.postCombine(ctx, group, combineOutbox, pc.boundCombineFn)
}

func (pc *pooledCombine[I, O]) execute(ctx context.Context, cm *combinerMap, emitGatherOutbox *workq.Outbox) {

	halfBoundCombineFn := getCombineFunc(
		ctx,
		pc.halfBoundCombinerPool,
		pc.boundGatherPool,
		pc.group,
		cm,
		pc.combinerPool,
		pc.opID,
		pc.newCombiner,
		pc.gatherFn,
		emitGatherOutbox,
	)
	halfBoundCombineFn(ctx, pc.input, pc.inputErr)

	pc.instancePool.Put(pc)
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
