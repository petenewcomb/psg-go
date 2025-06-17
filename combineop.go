// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/waitq"
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
}

// NewCombineOp creates a new CombineOp operation that uses the specified gather function,
// combiner pool, and combiner factory.
func NewCombineOp[I, O any](
	gatherOp *GatherOp[O],
	pool *CombinerPool,
	combinerFactory CombinerFactory[I, O],
	options ...psgopt.CombineOpOption,
) *CombineOp[I, O] {
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
	taskFunc TaskFunc[I],
) error {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFunc)

	doScatter := func(vettedCtx vettedContext) error {
		launched, err := c.scatter(vettedCtx, j, target, true, taskFunc)
		if !launched && err == nil {
			panic("task function was not launched, but no error was returned")
		}
		return err
	}

	bp := getBackpressureProvider(vettedCtx.ctx, j)

	// Queue work if we're in a gather context or if we have a combiner backpressure provider
	if vettedCtx.inGather || isCombinerBackpressureProvider(bp) {
		// Make sure the job doesn't shut down until this scatter has been done.
		j.state.IncrementWork()
		bp.QueueWork(func(ctx context.Context) error {
			defer j.state.DecrementWork()
			vettedCtx := j.vettedContext(ctx)
			return doScatter(vettedCtx)
		})
		return nil
	}

	ctx = j.gatherContext(vettedCtx)

	if err := j.processOutstandingWork(ctx); err != nil {
		return err
	}

	return doScatter(vettedCtx)
}

// TryScatter is like [CombineOp.Scatter] but returns instead of blocking if
// the given target is at its concurrency limit.
//
// See [GatherOp.TryScatter] for details about behavior and return values.
func (c *CombineOp[I, O]) TryScatter(
	ctx context.Context,
	target TaskPoolOrJob,
	taskFunc TaskFunc[I],
) (bool, error) {
	j := target.job()
	vettedCtx := j.vettedContext(ctx)
	vetScatter(vettedCtx, target, taskFunc)

	if !vettedCtx.inGather {
		if err := j.processOutstandingWork(ctx); err != nil {
			return false, err
		}
	}

	return c.scatter(vettedCtx, j, target, false, taskFunc)
}

func (c *CombineOp[I, O]) scatter(
	vettedCtx vettedContext,
	j *Job,
	target TaskPoolOrJob,
	block bool,
	taskFunc TaskFunc[I],
) (bool, error) {
	if j != c.pool.j {
		panic("target and combiner pools are associated with different jobs")
	}

	bp := getBackpressureProvider(vettedCtx.ctx, j)

	if err := yieldBeforeScatter(vettedCtx, bp); err != nil {
		return false, err
	}

	for !c.pool.waitingCombines.IsZero() {
		waiter := c.pool.combineWaiters.NewWaiter(func() bool {
			// Check again _after_ registering as a waiter, so we don't
			// potentially miss a notification.
			return !c.pool.waitingCombines.IsZero()
		})

		// bp.Block will return true only if we got a notification from the
		// waiterQueue, so we can pass that along to break out of the loop
		// and proceed without rechecking waitingCombines.
		waiterNotified, err := bp.Block(vettedCtx.ctx, waiter, nil)
		if err != nil {
			return false, err
		}
		if waiterNotified {
			break
		}
	}

	var bpf backpressureFunc
	if block {
		bpf = bp.Block
	}

	return scatter(vettedCtx, target, taskFunc, bpf, func(ctx context.Context, input I, inputErr error) {
		c.pool.postCombine(ctx, func(ctx context.Context, cm *combinerMap) {
			combineFn := getCombineFunc(ctx, cm, c.pool, c)
			combineFn(ctx, input, inputErr)
		})
	})
}

// combineBackpressureProvider is used to integrate the combiner pool with the job's
// backpressure system, allowing tasks to be gathered while waiting for resources
type combineBackpressureProvider struct {
	job          *Job
	tryCombineFn func(ctx context.Context) (bool, error)
	combineFn    func(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error)
	queueWorkFn  func(workFn func(context.Context) error)
	key          backpressureProviderKeyField
}

func (bp combineBackpressureProvider) ForJob(j *Job) bool {
	return bp.job == j
}

func (bp combineBackpressureProvider) Key() backpressureProviderKey {
	return &bp.key
}

func (bp combineBackpressureProvider) Yield(vetted vettedContext) (bool, error) {
	return bp.tryCombineFn(vetted.ctx)
}

func (bp combineBackpressureProvider) Block(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error) {
	return bp.combineFn(ctx, waiter, changeCh)
}

func (bp combineBackpressureProvider) QueueWork(workFn func(context.Context) error) {
	bp.queueWorkFn(workFn)
}

func isCombinerBackpressureProvider(bp backpressureProvider) bool {
	_, ok := bp.(combineBackpressureProvider)
	return ok
}

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
			panic(fmt.Sprintf("minHoldTime (%v) cannot be greater than maxHoldTime (%v)", *changes.MinHoldTime, w.combineOp.maxHoldTime))
		}
	}
	if changes.MaxHoldTime != nil {
		if *changes.MaxHoldTime < -1 {
			panic(fmt.Sprintf("invalid maxHoldTime %v: must be >= -1", *changes.MaxHoldTime))
		}
		if w.combineOp.minHoldTime >= 0 && *changes.MaxHoldTime < w.combineOp.minHoldTime {
			panic(fmt.Sprintf("maxHoldTime (%v) cannot be less than minHoldTime (%v)", *changes.MaxHoldTime, w.combineOp.minHoldTime))
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
