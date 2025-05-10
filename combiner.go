// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/state"
)

type Combiner[I any, O any] struct {
	ctx              context.Context
	concurrencyLimit state.DynamicValue[int]
	newCombinerFunc  CombinerFactory[I, O]
	gatherFunc       GatherFunc[O]
	spawnDelay       time.Duration
	linger           time.Duration
	inFlight         state.InFlightCounter
	liveCount        atomic.Int64
	primaryChan      chan boundCombinerFunc[I, O]
	secondaryChan    chan boundCombinerFunc[I, O]
	secondaryElected atomic.Bool
	waitingCombines  state.InFlightCounter
	waiterQueue      state.WaiterQueue
}

type CombinerFunc[I any, O any] func(ctx context.Context, flush bool, input I, inputErr error) (emit bool, output O, err error)

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
		ctx:             ctx,
		newCombinerFunc: combinerFactory,
		gatherFunc:      gatherFunc,
		spawnDelay:      10 * time.Millisecond,
		linger:          100 * time.Millisecond,
		primaryChan:     make(chan boundCombinerFunc[I, O]),
		secondaryChan:   make(chan boundCombinerFunc[I, O]),
	}
	c.concurrencyLimit.Store(concurrencyLimit)
	c.inFlight.Name = fmt.Sprintf("Combiner(%p).inFlight", c)
	c.waitingCombines.Name = fmt.Sprintf("Combiner(%p).waitingCombines", c)
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
	launched, err := c.scatter(ctx, pool, true, taskFunc)
	if !launched && err == nil {
		panic("task function was not launched, but no error was returned")
	}
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
	return c.scatter(ctx, pool, false, taskFunc)
}

func (c *Combiner[I, O]) scatter(
	ctx context.Context,
	pool *Pool,
	block bool,
	taskFunc TaskFunc[I],
) (bool, error) {
	vetScatter(ctx, pool, taskFunc)

	ctx = withPoolBackpressureProvider(ctx, pool)
	bp := getBackpressureProvider(ctx, pool.job)

	if err := yieldBeforeScatter(ctx, c.ctx, bp); err != nil {
		return false, err
	}

	if !c.waitingCombines.IsZero() {
		wait := func() (blockResult, error) {
			waiter := c.waiterQueue.Add()
			defer waiter.Close()

			// Check again _after_ registering with the queue, so we don't
			// potentially miss a notification.
			if c.waitingCombines.IsZero() {
				return blockResult{WaiterNotified: true}, nil
			}

			// bp.Block will return true only if we got a notification from the
			// waiterQueue, so we can pass that along to break out of the loop
			// and proceed without rechecking waitingCombines. Also pass along
			// the work function to be executed only after the waiter has been
			// closed.
			return bp.Block(ctx, c.ctx, waiter, nil)
		}
		for {
			res, err := wait()
			if err != nil {
				return false, err
			}
			if res.Work != nil {
				if err := res.Work(ctx); err != nil {
					return false, err
				}
			}
			if res.WaiterNotified {
				break
			}
		}
	}

	var bpf backpressureFunc
	if block {
		bpf = func(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (workFunc, error) {
			res, err := bp.Block(ctx, ctx2, waiter, limitChangeCh)
			return res.Work, err
		}
	}

	return scatter(ctx, pool, taskFunc, bpf, func(input I, err error) {
		// Build the combine function, binding the supplied combineFunc to the
		// result.
		combine := func(ctx context.Context, combinerFunc CombinerFunc[I, O]) (bool, O, error) {
			return combinerFunc(ctx, false, input, err)
		}

		// Loop in case the concurrency limit changes
		j := pool.job
		for {
			concurrencyLimit, concurrencyLimitChangeCh := c.concurrencyLimit.Load()

			if c.inFlight.Increment() && c.liveCount.Load() == 0 {
				// This is the first combine, go ahead and try to launch a task
				// without waiting for the spawn delay.
				if c.launchNewCombinerTask(j, concurrencyLimit, combine) {
					return
				}
				// Unable to launch a task (limit is zero or another goroutine beat
				// us to it), continue to blocking as usual.
			}

			// Attempt to post the combine to the primary channel.
			select {
			case c.primaryChan <- combine:
				return
			default:
			}

			// Primary channel is busy. Try the secondary one too and worst
			// case attempt to launch a new task.
			select {
			case c.primaryChan <- combine:
				return
			case c.secondaryChan <- combine:
				return
			default:
				if c.launchNewCombinerTask(j, concurrencyLimit, combine) {
					return
				}
			}

			// Return true if we're done, false if we should loop and retry.
			wait := func() bool {
				// If we get here, the primary and secondary channels were busy and we
				// hit the limit of how many combiner tasks we can launch. Increment the
				// waiting task count to signal Scatter to apply backpressure.
				c.waitingCombines.Increment()
				defer func() {
					c.waitingCombines.Decrement()
					c.waiterQueue.Notify()
				}()

				// Then block until we can post or a context gets canceled.
				select {
				case c.primaryChan <- combine:
				case c.secondaryChan <- combine:
				case <-concurrencyLimitChangeCh:
					// The concurrency limit changed, so loop and retry
					return false
				case <-ctx.Done():
				case <-c.ctx.Done():
				case <-j.ctx.Done():
				}
				return true
			}
			if wait() {
				break
			}
		}
	})
}

func (c *Combiner[I, O]) launchNewCombinerTask(j *Job, concurrencyLimit int, combine boundCombinerFunc[I, O]) bool {
	if c.liveCount.Add(1) > int64(concurrencyLimit) {
		c.liveCount.Add(-1)
		return false
	}
	nextFlushCh, unregisterAsFlusher := j.state.RegisterFlusher()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer c.liveCount.Add(-1)

		var isSecondary bool

		// Will become nil if this goroutine becomes secondary
		primaryCh := c.primaryChan

		// Initialized with backpressureProvider below
		var goroutineCtx context.Context

		// More forward references
		var executeCombine func(combine boundCombinerFunc[I, O])
		var flush func()

		tryCombineOne := func(ctx, ctx2 context.Context) (bool, error) {
			select {
			case combine := <-primaryCh:
				executeCombine(combine)
			case combine := <-c.secondaryChan:
				executeCombine(combine)
			case <-j.state.Done():
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			case <-ctx2.Done():
				return false, ctx.Err()
			case <-goroutineCtx.Done():
				return false, goroutineCtx.Err()
			case <-j.ctx.Done():
				return false, j.ctx.Err()
			default:
			}
			return true, nil
		}

		// Returns 1 if a combine, flush, or limit change was received, 0 if the
		// goroutine should exit, -1 if the waiter was notified
		type combineOneResult struct {
			GatheredOne    bool
			WaiterNotified bool
			TimedOut       bool
			JobDone        bool
			Work           func()
		}
		combineOne := func(ctx, ctx2 context.Context, timerCh <-chan time.Time, waiter state.Waiter, limitChangeCh <-chan struct{}) (combineOneResult, error) {
			var res combineOneResult
			var err error
			select {
			case combine := <-primaryCh:
				res.GatheredOne = true
				res.Work = func() {
					executeCombine(combine)
				}
			case combine := <-c.secondaryChan:
				res.GatheredOne = true
				res.Work = func() {
					executeCombine(combine)
				}
			case <-nextFlushCh:
				res.Work = flush
			case <-timerCh:
				// This goroutine is no longer needed.
				res.TimedOut = true
			case <-waiter.Done():
				// Retry per backpressureProvider.Block
				res.WaiterNotified = true
			case <-limitChangeCh:
				// Retry per backpressureProvider.Block
			case <-ctx.Done():
				err = ctx.Err()
			case <-ctx2.Done():
				err = ctx2.Err()
			case <-goroutineCtx.Done():
				err = goroutineCtx.Err()
			case <-j.ctx.Done():
				err = j.ctx.Err()
			case <-j.state.Done():
				res.JobDone = true
			}
			return res, err
		}

		bp := combineBackpressureProvider{
			job: j,
			tryCombineOne: func(ctx, ctx2 context.Context) (bool, error) {
				return tryCombineOne(ctx, ctx2)
			},
			combineOne: func(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (blockResult, error) {
				res, err := combineOne(ctx, ctx2, nil, waiter, limitChangeCh)
				var work workFunc
				if res.Work != nil {
					work = func(context.Context) error {
						res.Work()
						return nil
					}
				}
				return blockResult{
					WaiterNotified: res.WaiterNotified,
					Work:           work,
				}, err
			},
		}
		goroutineCtx = withBackpressureProvider(c.ctx, bp)

		combinerFunc := c.newCombinerFunc()

		// Flush sends any pending results from the combiner if needed. It also
		// decrements the combiner counter to ensure that the job is not kept
		// alive if there's nothing left to flush.
		flush = func() {
			if nextFlushCh != nil {
				var dummyInput I
				emit, output, err := combinerFunc(goroutineCtx, true, dummyInput, nil)
				if emit || err != nil {
					// There was no corresponding task in flight, but the
					// in-flight counter will be decremented by the job's
					// gather. Incrementing the in-flight counter will also keep
					// the job alive until the gather happens, even though we're
					// about to decrement the combiner count.
					j.state.IncrementTasks()
					c.postGather(j, output, err)
				}
				nextFlushCh = nil
				unregisterAsFlusher()
			}
		}

		// Ensure combiner is flushed as needed when this goroutine terminates.
		defer flush()

		executeCombine = func(combine boundCombinerFunc[I, O]) {
			if nextFlushCh == nil {
				// Make sure the job won't terminate before the combiner is flushed
				nextFlushCh, unregisterAsFlusher = j.state.RegisterFlusher()
			}
			emit, output, err := combine(goroutineCtx, combinerFunc)
			if emit || err != nil {
				c.postGather(j, output, err)
			} else {
				// Need to decrement the in-flight counter here because we're
				// not posting a corresponding gather.
				j.state.DecrementTasks()
			}
			c.inFlight.Decrement()
		}
		executeCombine(combine)

		var timer *time.Timer
		for {
			if isSecondary {
				isSecondary = c.secondaryElected.CompareAndSwap(false, true)
				if isSecondary {
					primaryCh = nil
					timer = j.timerPool.Get()
					defer j.timerPool.Put(timer)
					defer c.secondaryElected.Store(false)
				}
			}

			var timerCh <-chan time.Time
			if isSecondary {
				// This is the goroutine that has elected itself to read only
				// the secondary channel. This will keep this goroutine idle
				// unless it's really needed, thus allowing the linger time to
				// elapse.
				timer.Reset(c.linger)
				timerCh = timer.C
			}

			// Ignore errors from combineOne, since they would only be due to
			// canceled contexts
			res, err := combineOne(goroutineCtx, goroutineCtx, timerCh, state.Waiter{}, nil)
			if res.Work != nil {
				res.Work()

			}
			if res.TimedOut || res.JobDone || err != nil {
				// This goroutine should exit.
				return
			}
		}
	}()
	return true
}

type combineBackpressureProvider struct {
	job           *Job
	tryCombineOne func(ctx, ctx2 context.Context) (bool, error)
	combineOne    func(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (blockResult, error)
}

func (bp combineBackpressureProvider) ForJob(j *Job) bool {
	return bp.job == j
}

func (bp combineBackpressureProvider) Yield(ctx, ctx2 context.Context) (bool, error) {
	return bp.tryCombineOne(ctx, ctx2)
}

func (bp combineBackpressureProvider) Block(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (blockResult, error) {
	return bp.combineOne(ctx, ctx2, waiter, limitChangeCh)
}

func (c *Combiner[I, O]) postGather(j *Job, value O, err error) {
	// Build the gather function, binding the gatherFunc to the
	// combiner output.
	gather := func(ctx context.Context) error {
		return c.gatherFunc(ctx, value, err)
	}

	// Post the gather of the combiner's output to the job's gather channel.
	select {
	case j.gatherChan <- gather:
	case <-c.ctx.Done():
	case <-j.ctx.Done():
	}
}

type boundCombinerFunc[I any, O any] = func(context.Context, CombinerFunc[I, O]) (emit bool, output O, err error)
