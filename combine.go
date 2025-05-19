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
	launched, err := c.scatter(ctx, target, true, taskFunc)
	if !launched && err == nil {
		panic("task function was not launched, but no error was returned")
	}
	return err
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
	return c.scatter(ctx, target, false, taskFunc)
}

func (c *Combine[I, O]) scatter(
	ctx context.Context,
	target TaskPoolOrJob,
	block bool,
	taskFunc TaskFunc[I],
) (bool, error) {
	vetScatter(ctx, target, taskFunc)

	j := target.job()
	if j != c.combinerPool.job {
		panic("target and combiner pools are associated with different jobs")
	}

	ctx = target.withBackpressureProvider(ctx)
	bp := getBackpressureProvider(ctx, j)

	if err := yieldBeforeScatter(ctx, bp); err != nil {
		return false, err
	}

	if !c.combinerPool.waitingCombines.IsZero() {
		wait := func() (bool, error) {
			waiter := c.combinerPool.waiterQueue.Add()
			defer waiter.Close()

			// Check again _after_ registering with the queue, so we don't
			// potentially miss a notification.
			if c.combinerPool.waitingCombines.IsZero() {
				return true, nil
			}

			// bp.Block will return true only if we got a notification from the
			// waiterQueue, so we can pass that along to break out of the loop
			// and proceed without rechecking waitingCombines. Also pass along
			// the work function to be executed only after the waiter has been
			// closed.
			return bp.Block(ctx, waiter, nil)
		}
		for {
			waiterNotified, err := wait()
			if err != nil {
				return false, err
			}
			if waiterNotified {
				break
			}
		}
	}

	var bpf backpressureFunc
	if block {
		bpf = func(ctx context.Context, waiter waitq.Waiter, limitChangeCh <-chan struct{}) error {
			_, err := bp.Block(ctx, waiter, limitChangeCh)
			return err
		}
	}

	return scatter(ctx, target, taskFunc, bpf, func(ctx context.Context, input I, inputErr error) {
		c.combinerPool.launch(ctx, func(ctx context.Context, cm *combinerMap) {
			// Create an emit callback to handle output from the combiner
			getCombineFunc(ctx, cm, j, c)(ctx, input, inputErr)
		})
	})
}

// combineBackpressureProvider is used to integrate the combiner pool with the job's
// backpressure system, allowing tasks to be gathered while waiting for resources
type combineBackpressureProvider struct {
	job           *Job
	tryCombineOne func(ctx context.Context) (bool, error)
	combineOne    func(ctx context.Context, waiter waitq.Waiter, limitChangeCh <-chan struct{}) (bool, error)
}

func (bp combineBackpressureProvider) ForJob(j *Job) bool {
	return bp.job == j
}

func (bp combineBackpressureProvider) Yield(ctx context.Context) (bool, error) {
	return bp.tryCombineOne(ctx)
}

func (bp combineBackpressureProvider) Block(ctx context.Context, waiter waitq.Waiter, limitChangeCh <-chan struct{}) (bool, error) {
	return bp.combineOne(ctx, waiter, limitChangeCh)
}
