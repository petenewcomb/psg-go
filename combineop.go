// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
)

// CombineOp represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
type CombineOp[I, O any] struct {
	gatherOp    *GatherOp[O]
	pool        *CombinerPool
	newCombiner CombinerFactory[I, O]
	minHoldTime time.Duration // Minimum time since last combine before auto-flushing
	maxHoldTime time.Duration // Maximum time since first combine before auto-flushing

	// avoid closure reallocation
	postResultFn func(
		ctx context.Context,
		j *Job,
		taskWorkerOutboxMap *outboxMap,
		input I,
		inputErr error,
	)
}

// NewCombineOp creates a new CombineOp operation that uses the specified gather function,
// combiner pool, and combiner factory.
//
//nolint:contextcheck // background context used only for tracing
func NewCombineOp[I, O any](
	gatherOp *GatherOp[O],
	pool *CombinerPool,
	combinerFactory CombinerFactory[I, O],
	options ...psgopt.CombineOpOption,
) *CombineOp[I, O] {
	traceRegion := "NewCombineOp"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if gatherOp == nil {
		panic("gather must be non-nil")
	}
	if pool == nil {
		panic("combiner pool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combiner factory must be non-nil")
	}
	c := &CombineOp[I, O]{
		gatherOp:    gatherOp,
		pool:        pool,
		newCombiner: combinerFactory,
		minHoldTime: -1, // Sentinel value: no idle-based flushing
		maxHoldTime: -1, // Sentinel value: no absolute deadline
	}

	c.postResultFn = c.postResult

	trace.Logf(context.Background(), traceRegion, "CombineOp=%p, pool=%p", c, pool)

	// Apply user options
	c.SetOptions(options...)

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
	trace.Logf(ctx, traceRegion, "CombineOp=%p", c)

	ctx, meta := vetScatter(ctx, target, taskFn)
	work := c.newScatterWork(target, taskFn)
	return scatterNow(ctx, meta, target, work)
}

// TryScatter is like [CombineOp.Scatter] but returns instead of blocking if
// the given target is at its concurrency limit.
//
// See [GatherOp.TryScatter] for details about behavior and return values.
func (c *CombineOp[I, O]) TryScatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) (bool, error) {
	traceRegion := "CombineOp.TryScatter"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "CombineOp=%p", c)

	ctx, meta := vetScatter(ctx, target, taskFn)
	work := c.newScatterWork(target, taskFn)
	return tryScatterNow(ctx, meta, target, work)
}

func (c *CombineOp[I, O]) newScatterWork(
	target TaskPoolOrJob,
	taskFn psgfn.Task[I],
) *combineScatterWork {
	traceRegion := "CombineOp.newScatterWork"

	j := target.getJob()
	if j != c.pool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	w := combineScatterWorkPool.Get()
	w.Init(c.pool, target, bindTaskFunc(j, taskFn, c.postResultFn))

	trace.Logf(context.Background(), traceRegion, "CombineOp=%p created %v", c, w)
	return w
}

type combineScatterWork struct {
	jobWork
	pool   *CombinerPool
	target TaskPoolOrJob
	taskPoolScatterWork
	taskFn boundTaskFunc
}

func (w *combineScatterWork) Init(pool *CombinerPool, target TaskPoolOrJob, taskFn boundTaskFunc) {
	w.jobWork.Init(pool.job)
	w.pool = pool
	w.target = target
	w.taskFn = taskFn
}

func (w *combineScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "combineScatterWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	bb := w.pool.job.protoBB

	return w.pool.governor.Execute(ctx, ex, bb,
		func(ctx context.Context, ex workq.Execution) error {
			return w.target.scatter(ctx, ex, &w.taskPoolScatterWork, w.taskFn)
		},
	)
}

func (w *combineScatterWork) Close() {
	traceRegion := "combineScatterWork.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.jobWork.Close(w.pool.job)

	*w = combineScatterWork{} // Clear the work item for garbage collection and reuse
	combineScatterWorkPool.Put(w)
}

var combineScatterWorkPool = omnipool.For[combineScatterWork]()

// combineOpConfigWrapper wraps a CombineOp to implement the combineOpConfig interface for options
type combineOpConfigWrapper[I, O any] struct {
	combineOp *CombineOp[I, O]
}

func (w combineOpConfigWrapper[I, O]) Update(changes opts.CombineOpConfigChanges) {
	// Validate all changes first
	if changes.MinHoldTime != nil {
		if *changes.MinHoldTime < -1 {
			panic(fmt.Sprintf("invalid minHoldTime %v: must be >= -1", *changes.MinHoldTime))
		}
		if w.combineOp.maxHoldTime >= 0 && *changes.MinHoldTime > w.combineOp.maxHoldTime {
			panic(fmt.Sprintf("minHoldTime (%v) cannot be greater than maxHoldTime (%v)",
				*changes.MinHoldTime, w.combineOp.maxHoldTime))
		}
	}
	if changes.MaxHoldTime != nil {
		if *changes.MaxHoldTime < -1 {
			panic(fmt.Sprintf("invalid maxHoldTime %v: must be >= -1", *changes.MaxHoldTime))
		}
		if w.combineOp.minHoldTime >= 0 && *changes.MaxHoldTime < w.combineOp.minHoldTime {
			panic(fmt.Sprintf("maxHoldTime (%v) cannot be less than minHoldTime (%v)",
				*changes.MaxHoldTime, w.combineOp.minHoldTime))
		}
	}

	// Apply changes
	if changes.MinHoldTime != nil {
		w.combineOp.minHoldTime = *changes.MinHoldTime
	}
	if changes.MaxHoldTime != nil {
		w.combineOp.maxHoldTime = *changes.MaxHoldTime
	}
}

// SetOptions applies the given configuration options to the combine operation.
// This method is safe to call at any time. However, the timing of when the new
// values take effect within a running job is undefined.
func (c *CombineOp[I, O]) SetOptions(options ...psgopt.CombineOpOption) {
	opts.ApplyToCombineOp(combineOpConfigWrapper[I, O]{combineOp: c}, options...)
}

func (c *CombineOp[I, O]) postResult(
	ctx context.Context,
	j *Job,
	taskWorkerOutboxMap *outboxMap,
	input I,
	inputErr error,
) {
	traceRegion := "CombineOp.postResult"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Post the combine using the task worker's outbox for the pool's combine queue
	combineOutbox := OutboxFor[workq.Work](taskWorkerOutboxMap, c.pool.combineOutboxKey())
	trace.Logf(ctx, traceRegion, "CombineOp=%p, outbox=%p", c, combineOutbox)

	// Bind the halfBoundCombineFn to the result.
	boundCombineFn := func(
		ctx context.Context,
		cm *combinerMap,
		queueWork workq.QueueWorkFunc,
		emitGatherOutbox *workq.Outbox,
	) {
		halfBoundCombineFn := getCombineFunc(ctx, cm, c.pool, c, queueWork, emitGatherOutbox)
		halfBoundCombineFn(ctx, input, inputErr)
	}

	c.pool.postCombine(ctx, combineOutbox, boundCombineFn)
}
