// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/state"
)

// Combine represents an operation that combines inputs and produces outputs.
// It binds a gather function with a combiner factory and a combiner pool.
type Combine[I any, O any] struct {
	gather          *Gather[O]
	combinerPool    *CombinerPool
	combinerFactory CombinerFactory[I, O]
	minHoldTime     time.Duration // Minimum time since last combine before auto-flushing
	maxHoldTime     time.Duration // Maximum time since first combine before auto-flushing
}

// NewCombine creates a new Combine operation that uses the specified gather function,
// combiner pool, and combiner factory.
func NewCombine[I any, O any](
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
		gather:          gather,
		combinerPool:    combinerPool,
		combinerFactory: combinerFactory,
		minHoldTime:     -1, // Sentinel value: no idle-based flushing
		maxHoldTime:     -1, // Sentinel value: no absolute deadline
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
// Panics if min is < -1 or if min > maxHoldTime (when maxHoldTime >= 0).
func (c *Combine[I, O]) SetMinHoldTime(min time.Duration) {
	if min < -1 {
		panic(fmt.Sprintf("invalid minHoldTime %v: must be >= -1", min))
	}
	if c.maxHoldTime >= 0 && min > c.maxHoldTime {
		panic(fmt.Sprintf("minHoldTime (%v) cannot be greater than maxHoldTime (%v)", min, c.maxHoldTime))
	}
	c.minHoldTime = min
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
// Panics if max is < -1 or if max < minHoldTime (when minHoldTime >= 0).
func (c *Combine[I, O]) SetMaxHoldTime(max time.Duration) {
	if max < -1 {
		panic(fmt.Sprintf("invalid maxHoldTime %v: must be >= -1", max))
	}
	if c.minHoldTime >= 0 && max < c.minHoldTime {
		panic(fmt.Sprintf("maxHoldTime (%v) cannot be less than minHoldTime (%v)", max, c.minHoldTime))
	}
	c.maxHoldTime = max
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// combined using this Combine's combiner and eventually passed to the associated
// Gather.
//
// Scatter blocks to delay launch as needed to ensure compliance with the
// concurrency limit for the given task pool. This backpressure is applied by
// gathering other tasks in the job until a slot becomes available. The
// context passed to Scatter may be used to cancel (e.g., with a timeout) both
// gathering and launch, but only the context associated with the task pool's job
// will be passed to the task.
//
// WARNING: Scatter must not be called from within a TaskFunc launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated GatherFunc after the TaskFunc
// completes.
//
// Scatter will panic if the given task pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the Gather's gather function.
//
// See [TaskFunc] and [GatherFunc] for important caveats and additional detail.
func (c *Combine[I, O]) Scatter(
	ctx context.Context,
	taskPool *TaskPool,
	taskFunc TaskFunc[I],
) error {
	launched, err := c.scatter(ctx, taskPool, true, taskFunc)
	if !launched && err == nil {
		panic("task function was not launched, but no error was returned")
	}
	return err
}

// TryScatter attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Scatter]. Unlike Scatter, TryScatter will
// return instead of blocking if the given task pool is already at its concurrency
// limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// the task pool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Scatter for more detail about how scattering works.
func (c *Combine[I, O]) TryScatter(
	ctx context.Context,
	taskPool *TaskPool,
	taskFunc TaskFunc[I],
) (bool, error) {
	return c.scatter(ctx, taskPool, false, taskFunc)
}

func (c *Combine[I, O]) scatter(
	ctx context.Context,
	taskPool *TaskPool,
	block bool,
	taskFunc TaskFunc[I],
) (bool, error) {
	vetScatter(ctx, taskPool, taskFunc)

	j := taskPool.job
	if j != c.combinerPool.job {
		panic("task and combiner pools are associated with different jobs")
	}

	ctx = withTaskPoolBackpressureProvider(ctx, taskPool)
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
		bpf = func(ctx context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) error {
			_, err := bp.Block(ctx, waiter, limitChangeCh)
			return err
		}
	}

	return scatter(ctx, taskPool, taskFunc, bpf, func(ctx context.Context, input I, inputErr error) {
		c.combinerPool.launch(ctx, func(ctx context.Context, cm *combinerMap) {
			// Create an emit callback to handle output from the combiner
			getCombineFunc(cm, j, c)(ctx, input, inputErr)
		})
	})
}

// combineBackpressureProvider is used to integrate the combiner pool with the job's
// backpressure system, allowing tasks to be gathered while waiting for resources
type combineBackpressureProvider struct {
	job           *Job
	tryCombineOne func(ctx context.Context) (bool, error)
	combineOne    func(ctx context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (bool, error)
}

func (bp combineBackpressureProvider) ForJob(j *Job) bool {
	return bp.job == j
}

func (bp combineBackpressureProvider) Yield(ctx context.Context) (bool, error) {
	return bp.tryCombineOne(ctx)
}

func (bp combineBackpressureProvider) Block(ctx context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (bool, error) {
	return bp.combineOne(ctx, waiter, limitChangeCh)
}
