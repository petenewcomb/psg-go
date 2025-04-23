// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/state"
)

type Combiner[I any, O any] struct {
	ctx              context.Context
	concurrencyLimit atomic.Int64
	newCombinerFunc  CombinerFactory[I, O]
	gatherFunc       GatherFunc[O]
	spawnDelay       time.Duration
	linger           time.Duration
	inFlight         state.InFlightCounter
	liveCount        atomic.Int64
	primaryChannel   chan boundCombinerFunc[I, O]
	secondaryChannel chan boundCombinerFunc[I, O]
}

type CombinerFunc[I any, O any] func(ctx context.Context, done bool, input I, inputErr error) (emit bool, output O, err error)

type CombinerFactory[I any, O any] = func() CombinerFunc[I, O]

func NewCombiner[I any, O any](ctx context.Context, concurrencyLimit int, combinerFactory CombinerFactory[I, O], gatherFunc GatherFunc[O]) *Combiner[I, O] {
	if ctx == nil {
		panic("context must be non-nil")
	}
	if combinerFactory == nil {
		panic("combiner factory must be non-nil")
	}
	if gatherFunc == nil {
		panic("gather function must be non-nil")
	}
	c := &Combiner[I, O]{
		ctx:              ctx,
		newCombinerFunc:  combinerFactory,
		gatherFunc:       gatherFunc,
		spawnDelay:       10 * time.Millisecond,
		linger:           100 * time.Millisecond,
		primaryChannel:   make(chan boundCombinerFunc[I, O]),
		secondaryChannel: make(chan boundCombinerFunc[I, O]),
	}
	c.concurrencyLimit.Store(int64(concurrencyLimit))
	return c
}

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// passed to the Gather within a subsequent call to Scatter or any of the
// gathering methods of [Job] (i.e., [Job.GatherOne], [Job.TryGatherOne],
// [Job.GatherAll], or [Job.TryGatherAll]).
//
// Scatter blocks to delay launch as needed to ensure compliance with the
// concurrency limit for the given pool. This backpressure is applied by
// gathering other tasks in the job until the a slot becomes available. The
// context passed to Scatter may be used to cancel (e.g., with a timeout) both
// gathering and launch, but only the context associated with the pool's job
// will be passed to the task.
//
// WARNING: Scatter must not be called from within a TaskFunc launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated GatherFunc after the TaskFunc
// completes.
//
// Scatter will panic if the given pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the Gather's gather function.
//
// See [TaskFunc] and [GatherFunc] for important caveats and additional detail.
func (c *Combiner[I, O]) Scatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[I],
) error {
	_, err := c.scatter(ctx, pool, taskFunc, backpressureWaiter)
	return err
}

// TryScatter attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Scatter]. Unlike Scatter, TryScatter will
// return instead of blocking if the given pool is already at its concurrency
// limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// the pool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Scatter for more detail about how scattering works.
func (c *Combiner[I, O]) TryScatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[I],
) (bool, error) {
	return c.scatter(ctx, pool, taskFunc, backpressureDecline)
}

func (c *Combiner[I, O]) scatter(
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[I],
	mode backpressureMode,
) (bool, error) {
	j := pool.job
	return scatter(ctx, pool, taskFunc, mode, func(input I, err error) {
		// Build the combine function, binding the supplied combineFunc to the
		// result.
		combine := func(ctx context.Context, combinerFunc CombinerFunc[I, O]) (bool, O, error) {
			return combinerFunc(ctx, false, input, err)
		}

		if c.inFlight.Increment() && c.liveCount.Load() == 0 {
			// This is the first combine, go ahead and try to launch a task
			// without waiting for the spawn delay.
			if c.launchNewCombinerTask(j, combine) {
				return
			}
			// Unable to launch a task (limit is zero or another goroutine beat
			// us to it), continue to blocking as usual.
		}

		// Loop to deal with the case in which we try to launch a task but
		// cannot.
		for {
			// Post the combine, preferring the primary channel.
			select {
			case c.primaryChannel <- combine:
				return
			case <-time.After(c.spawnDelay):
				select {
				case c.secondaryChannel <- combine:
					return
				default:
					if c.launchNewCombinerTask(j, combine) {
						return
					}
					// Loop and retry
				}
			case <-ctx.Done():
				return
			case <-c.ctx.Done():
				return
			case <-j.ctx.Done():
				return
			}
		}
	})
}

func (c *Combiner[I, O]) launchNewCombinerTask(j *Job, combine boundCombinerFunc[I, O]) bool {
	id := c.liveCount.Add(1)
	if id > c.concurrencyLimit.Load() {
		c.liveCount.Add(-1)
		return false
	}
	j.combiners.Increment()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer c.liveCount.Add(-1)
		defer j.decrementCombiners()

		var combinerFunc CombinerFunc[I, O]
		defer func() {
			if combinerFunc != nil {
				var dummyInput I
				emit, output, err := combinerFunc(c.ctx, true, dummyInput, nil)
				if emit || err != nil {
					// There was no corresponding task in flight, but the
					// in-flight counter will be decremented by the job's
					// gather. Incrementing the in-flight counter will also keep
					// the job alive until the gather happens, even though we're
					// about to decrement the combiner count. (See defer
					// j.decrementCombiners() statement just above.)
					j.inFlight.Increment()
					c.postGather(j, output, err)
				}

			}
		}()

		executeCombine := func(combine boundCombinerFunc[I, O]) {
			if combinerFunc == nil {
				combinerFunc = c.newCombinerFunc()
			}
			emit, output, err := combine(c.ctx, combinerFunc)
			if emit || err != nil {
				c.postGather(j, output, err)
			} else {
				// Need to decrement the in-flight counter here because we're
				// not posting a corresponding gather.
				j.decrementInFlight()
			}
			c.inFlight.Decrement()
		}
		if combine != nil {
			executeCombine(combine)
		}

		for {
			if id == c.liveCount.Load() {
				// This is the most recently added live goroutine (and not the
				// only live goroutine), so read only the secondary channel.
				// This will keep this goroutine idle unless it's really needed,
				// thus allowing the linger time to elapse.
				select {
				case combine := <-c.secondaryChannel:
					executeCombine(combine)
				case <-time.After(c.linger):
					// This goroutine is no longer needed.
					return
				case <-c.ctx.Done():
					return
				case <-j.ctx.Done():
					return
				case <-j.flush:
					return
				}
			} else {
				// Same select as above, but also reads the primary channel
				select {
				case combine := <-c.primaryChannel:
					executeCombine(combine)
				case combine := <-c.secondaryChannel:
					executeCombine(combine)
				case <-time.After(c.linger):
					// This goroutine is no longer needed.
					return
				case <-c.ctx.Done():
					return
				case <-j.ctx.Done():
					return
				case <-j.flush:
					return
				}
			}
		}
	}()
	return true
}

func (c *Combiner[I, O]) postGather(j *Job, value O, err error) {
	// Build the gather function, binding the gatherFunc to the
	// combiner output.
	gather := func(ctx context.Context) error {
		return c.gatherFunc(ctx, value, err)
	}

	// Post the gather of the combiner's output to the job's gather channel.
	select {
	case j.gatherChannel <- gather:
	case <-c.ctx.Done():
	case <-j.ctx.Done():
	}
}

type boundCombinerFunc[I any, O any] = func(context.Context, CombinerFunc[I, O]) (emit bool, output O, err error)
