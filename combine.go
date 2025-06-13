// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/waitq"
)

// Combine represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
type Combine[I, O any] struct {
	gather       *Gather[O]
	combinerPool *CombinerPool
	newCombiner  CombinerFactory[I, O]
	minHoldTime  time.Duration // Minimum time since last combine before auto-flushing
	maxHoldTime  time.Duration // Maximum time since first combine before auto-flushing
}

// NewCombine creates a new Combine operation that uses the specified gather function,
// combiner pool, and combiner factory.
func NewCombine[I, O any](
	gather *Gather[O],
	combinerPool *CombinerPool,
	combinerFactory CombinerFactory[I, O],
) *Combine[I, O] {
	if gather == nil {
		panic("gather must be non-nil")
	}
	if combinerPool == nil {
		panic("combiner pool must be non-nil")
	}
	if combinerFactory == nil {
		panic("combiner factory must be non-nil")
	}
	c := &Combine[I, O]{
		gather:       gather,
		combinerPool: combinerPool,
		newCombiner:  combinerFactory,
		minHoldTime:  -1, // Sentinel value: no idle-based flushing
		maxHoldTime:  -1, // Sentinel value: no absolute deadline
	}
	return c
}

// SetMinHoldTime sets the minimum time a combiner will hold inputs after the last
// combine operation before flushing. This is useful for batching inputs that arrive
// close together in time.
//
// A value of -1 (the default) means no idle-based flushing will occur.
// A value of 0 means flush immediately after each combine.
// A positive value means wait at least that duration after the last combine before flushing.
//
// This method is safe to call at any time. However, the timing
// of when the new value takes effect within a running job is undefined.
//
// Panics if argument is less than -1 or greater than maxHoldTime (when
// maxHoldTime >= 0).
func (c *Combine[I, O]) SetMinHoldTime(d time.Duration) {
	if d < -1 {
		panic(fmt.Sprintf("invalid minHoldTime %v: must be >= -1", d))
	}
	if c.maxHoldTime >= 0 && d > c.maxHoldTime {
		panic(fmt.Sprintf("minHoldTime (%v) cannot be greater than maxHoldTime (%v)", d, c.maxHoldTime))
	}
	c.minHoldTime = d
}

// SetMaxHoldTime sets the maximum time a combiner will hold any inputs before
// flushing, measured from when the first unflushed input was received. This creates
// an upper bound on result latency.
//
// A value of -1 (the default) means no absolute deadline for flushing.
// A value of 0 means flush immediately (equivalent to no combining).
// A positive value means wait at most that duration since the first combine before flushing.
//
// This method is safe to call at any time. However, the timing
// of when the new value takes effect within a running job is undefined.
//
// Panics if argument is less than -1 or less than minHoldTime (when minHoldTime
// >= 0).
func (c *Combine[I, O]) SetMaxHoldTime(d time.Duration) {
	if d < -1 {
		panic(fmt.Sprintf("invalid maxHoldTime %v: must be >= -1", d))
	}
	if c.minHoldTime >= 0 && d < c.minHoldTime {
		panic(fmt.Sprintf("maxHoldTime (%v) cannot be less than minHoldTime (%v)", d, c.minHoldTime))
	}
	c.maxHoldTime = d
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// combined using this Combine's combiner and eventually passed to the associated
// Gather.
//
// See [Gather.Scatter] for details about backpressure, concurrency limits,
// context handling, and error behavior.
func (c *Combine[I, O]) Scatter(
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
		j.state.IncrementTasks()
		bp.QueueWork(func(ctx context.Context) error {
			defer j.state.DecrementTasks()
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

// TryScatter is like [Combine.Scatter] but returns instead of blocking if
// the given target is at its concurrency limit.
//
// See [Gather.TryScatter] for details about behavior and return values.
func (c *Combine[I, O]) TryScatter(
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

func (c *Combine[I, O]) scatter(
	vettedCtx vettedContext,
	j *Job,
	target TaskPoolOrJob,
	block bool,
	taskFunc TaskFunc[I],
) (bool, error) {
	if j != c.combinerPool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	bp := getBackpressureProvider(vettedCtx.ctx, j)

	if err := yieldBeforeScatter(vettedCtx, bp); err != nil {
		return false, err
	}

	if !c.combinerPool.waitingCombines.IsZero() {
		for {
			proceed := false
			var err error
			c.combinerPool.combineWaiters.Wait(func(waiter waitq.Waiter) bool {
				// Check again _after_ registering as a waiter, so we don't
				// potentially miss a notification.
				if c.combinerPool.waitingCombines.IsZero() {
					proceed = true
					return false // waiter was not notified
				}

				// bp.Block will return true only if we got a notification from the
				// waiterQueue, so we can pass that along to break out of the loop
				// and proceed without rechecking waitingCombines.
				var waiterNotified bool
				waiterNotified, err = bp.Block(vettedCtx.ctx, waiter, nil)
				if waiterNotified {
					proceed = true
				}
				return waiterNotified
			})
			if err != nil {
				return false, err
			}
			if proceed {
				break
			}
		}
	}

	var bpf backpressureFunc
	if block {
		bpf = bp.Block
	}

	return scatter(vettedCtx, target, taskFunc, bpf, func(ctx context.Context, input I, inputErr error) {
		postStartTime := time.Now()
		c.combinerPool.postCombine(ctx, func(ctx context.Context, cm *combinerMap) time.Duration {
			// Create an emit callback to handle output from the combiner
			combineFn := getCombineFunc(ctx, cm, c.combinerPool, c)
			latency := time.Since(postStartTime)
			combineFn(ctx, input, inputErr)
			return latency
		})
	})
}

// combineBackpressureProvider is used to integrate the combiner pool with the job's
// backpressure system, allowing tasks to be gathered while waiting for resources
type combineBackpressureProvider struct {
	job           *Job
	tryCombineOne func(ctx context.Context) (bool, error)
	combineOne    func(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error)
	queueWork     func(workFunc func(context.Context) error)
	key           backpressureProviderKeyField
}

func (bp combineBackpressureProvider) ForJob(j *Job) bool {
	return bp.job == j
}

func (bp combineBackpressureProvider) Key() backpressureProviderKey {
	return &bp.key
}

func (bp combineBackpressureProvider) Yield(vetted vettedContext) (bool, error) {
	return bp.tryCombineOne(vetted.ctx)
}

func (bp combineBackpressureProvider) Block(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error) {
	select {
	case <-waiter.Done():
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
	//return bp.combineOne(ctx, waiter, changeCh)
}

func (bp combineBackpressureProvider) QueueWork(workFunc func(context.Context) error) {
	bp.queueWork(workFunc)
}

func isCombinerBackpressureProvider(bp backpressureProvider) bool {
	_, ok := bp.(combineBackpressureProvider)
	return ok
}
