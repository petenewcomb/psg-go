// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/heap"
	"github.com/petenewcomb/psg-go/internal/state"
)

// CombinerPool manages a pool of goroutines that execute combiners.
// It handles concurrency limits, spawning new goroutines, and reusing existing ones.
type CombinerPool struct {
	job              *Job
	concurrencyLimit state.DynamicValue[int]
	spawnDelay       time.Duration
	idleTimeout      time.Duration

	liveGoroutineCount atomic.Int64

	// Scattered tasks first attempt to post their results to primaryChan. If a
	// combiner goroutine is not immediately available, the task will
	// concurrently try posting to both primaryChan and secondaryChan for the
	// period of time defined by spawnDelay before attempting to launch a new
	// goroutine. If concurrencyLimit disallows launch, the task will continue
	// to try posting to both channels indefinitely.
	//
	// Only one goroutine at a time can elect itself "secondary". Once elected,
	// the secondary goroutine no longer listens to primaryChan and will
	// therefore receive task results only if the other goroutines are too busy
	// to immediately receive all results being posted to primaryChan. This
	// allows the secondary goroutine to detect if its capacity is no longer
	// needed by staying idle until idleTimeout has passed. If this happens, the
	// secondary goroutine resets secondaryElected to false and exits, allowing
	// a different goroutine to elect itself secondary and continue the idle
	// detection process.
	primaryChan      chan boundCombineFunc
	secondaryChan    chan boundCombineFunc
	secondaryElected atomic.Bool

	waitingCombines state.InFlightCounter
	waiterQueue     state.WaiterQueue
}

// NewCombinerPool creates a new CombinerPool with the specified concurrency limit.
func NewCombinerPool(job *Job, concurrencyLimit int) *CombinerPool {
	if job == nil {
		panic("job is nil")
	}
	cp := &CombinerPool{
		job:           job,
		spawnDelay:    0,  // Default: no delay in spawning new goroutines
		idleTimeout:   -1, // Sentinel value: no idle-based exit
		primaryChan:   make(chan boundCombineFunc),
		secondaryChan: make(chan boundCombineFunc),
	}
	cp.concurrencyLimit.Store(concurrencyLimit)
	return cp
}

// SetLimit sets the active concurrency limit for the pool. A negative value means no
// limit (combiners will always be launched regardless of how many are currently
// running). Zero means no new combiners will be launched until SetLimit is called
// with a non-zero value.
//
// This method is safe to call at any time. The new limit takes effect immediately
// for subsequent combiner launches and may unblock existing blocked operations.
func (cp *CombinerPool) SetLimit(limit int) {
	cp.concurrencyLimit.Store(limit)
}

// SetIdleTimeout sets how long excess combiner goroutines can remain idle
// before exiting. This is used to optimize resource usage by allowing unneeded
// goroutines to terminate when combiner activity is low.
//
// The pool ensures that only one goroutine at a time is subject to the idle timeout,
// which prevents excessive thrashing when the workload fluctuates. The reciprocal
// of the idle timeout is the maximum frequency at which goroutines will exit due
// to idleness (outside of job termination).
//
// Note that when any combiner goroutine exits, all combiners it is managing will
// be flushed regardless of their min/max hold time settings. This ensures no
// data is lost, but may result in smaller batches than expected if goroutines
// frequently exit due to idleness.
//
// A value of -1 (the default) disables idle timeouts completely, causing all goroutines
// to remain alive until the job completes. This maximizes combining efficiency but
// uses more resources.
//
// A value of 0 means goroutines may exit as soon as they become idle.
//
// A positive value specifies how long a goroutine should wait while idle before exiting.
// Shorter timeouts reduce resource usage but may require more frequent spawning of new
// goroutines and result in more frequent flushing. Longer timeouts keep goroutines
// available for longer but use more resources.
//
// This method is safe to call at any time. However, the timing
// of when the new value takes effect within a running job is undefined.
func (cp *CombinerPool) SetIdleTimeout(timeout time.Duration) {
	if timeout < -1 {
		panic(fmt.Sprintf("invalid idle timeout %v: must be >= -1", timeout))
	}
	cp.idleTimeout = timeout
}

// SetSpawnDelay sets the delay before spawning new combiner goroutines when
// existing goroutines are busy. This controls how quickly the pool responds to
// increased load by creating new combiners.
//
// Each combiner goroutine combines values independently, and more goroutines
// means more independent combiners. This increases parallelism but may result
// in more, smaller batches of combined values.
//
// A value of 0 (the default) means new goroutines are created immediately
// when needed, up to the concurrency limit. This maximizes responsiveness to
// sudden increases in load.
//
// A positive value introduces a delay before spawning each new goroutine,
// allowing existing goroutines a chance to catch up. This can lead to better
// batching efficiency at the cost of higher latency during load spikes.
//
// During the delay period, the pool will continue trying to send the combine
// operation to existing goroutines. Only if no goroutine becomes available
// during the delay will a new one be created.
//
// This method is safe to call at any time. However, the timing
// of when the new value takes effect within a running job is undefined.
func (cp *CombinerPool) SetSpawnDelay(delay time.Duration) {
	if delay < 0 {
		panic(fmt.Sprintf("invalid spawn delay %v: must be >= 0", delay))
	}
	cp.spawnDelay = delay
}

func (cp *CombinerPool) launch(ctx context.Context, combine boundCombineFunc) {

	j := cp.job

	// Loop in case the concurrency limit changes
	for {
		concurrencyLimit, concurrencyLimitChangeCh := cp.concurrencyLimit.Load()

		if cp.liveGoroutineCount.Load() == 0 {
			// This is the first combine, go ahead and try to launch a task
			// without waiting for the spawn delay.
			if cp.launchNewCombiner(j, concurrencyLimit, combine) {
				return
			}
			// Unable to launch a task (limit is zero or another goroutine beat
			// us to it), continue to blocking as usual.
		}

		// Attempt to post the combine to the primary channel.
		select {
		case cp.primaryChan <- combine:
			return
		default:
		}

		// Primary channel is busy. Try the secondary one too and worst
		// case attempt to launch a new task.
		spawnDelay := cp.spawnDelay
		if cp.spawnDelay > 0 {
			maybeSpawn := func() bool {
				spawnDelayTimer := j.timerPool.Get()
				defer j.timerPool.Put(spawnDelayTimer)
				spawnDelayTimer.Reset(spawnDelay)
				select {
				case cp.primaryChan <- combine:
					return true
				case cp.secondaryChan <- combine:
					return true
				case <-spawnDelayTimer.C:
					return cp.launchNewCombiner(j, concurrencyLimit, combine)
				}
			}
			if maybeSpawn() {
				return
			}
		} else {
			select {
			case cp.primaryChan <- combine:
				return
			case cp.secondaryChan <- combine:
				return
			default:
				if cp.launchNewCombiner(j, concurrencyLimit, combine) {
					return
				}
			}
		}

		// Return true if we're done, false if we should loop and retry.
		wait := func() bool {
			// If we get here, the primary and secondary channels were busy and we
			// hit the limit of how many combiner tasks we can launch. Increment the
			// waiting task count to signal Scatter to apply backpressure.
			cp.waitingCombines.Increment()
			defer func() {
				cp.waitingCombines.Decrement()
				cp.waiterQueue.Notify()
			}()

			// Then block until we can post or a context gets canceled.
			select {
			case cp.primaryChan <- combine:
			case cp.secondaryChan <- combine:
			case <-concurrencyLimitChangeCh:
				// The concurrency limit changed, so loop and retry
				return false
			case <-ctx.Done():
			case <-j.ctx.Done():
			}
			return true
		}
		if wait() {
			break
		}
	}
}

func (cp *CombinerPool) launchNewCombiner(j *Job, concurrencyLimit int, combine boundCombineFunc) bool {
	if cp.liveGoroutineCount.Add(1) > int64(concurrencyLimit) {
		cp.liveGoroutineCount.Add(-1)
		return false
	}
	nextJobFlushCh, unregisterAsJobFlusher := j.state.RegisterFlusher()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer cp.liveGoroutineCount.Add(-1)

		var isSecondary bool

		// Will become nil if this goroutine becomes secondary
		primaryCh := cp.primaryChan

		// Initialized with backpressureProvider below
		var goroutineCtx context.Context

		var cm combinerMap

		// More forward references
		var executeCombine func(ctx context.Context, combine boundCombineFunc)
		var flushAll func(ctx context.Context)

		tryCombineOne := func(ctx context.Context) (bool, error) {
			now := time.Now()
			for {
				nextBCToFlush := cm.NextToFlush()
				if nextBCToFlush == nil {
					break
				}
				deadline := nextBCToFlush.FlushDeadline
				if now.Before(deadline) {
					break
				}
				nextBCToFlush.FlushFunc(ctx)
			}

			select {
			case combine := <-primaryCh:
				executeCombine(ctx, combine)
			case combine := <-cp.secondaryChan:
				executeCombine(ctx, combine)
			case <-j.state.Done():
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			case <-goroutineCtx.Done():
				return false, goroutineCtx.Err()
			default:
			}
			return true, nil
		}

		type combineOneResult struct {
			GatheredOne    bool
			WaiterNotified bool
			TimedOut       bool
			JobDone        bool
			Work           func(context.Context)
		}
		combineOne := func(ctx context.Context, timerCh <-chan time.Time, waiter state.Waiter, limitChangeCh <-chan struct{}) (combineOneResult, error) {
			var res combineOneResult
			var err error

			// Check if any combiners need to be flushed due to deadlines and
			// set up flush deadline timer if needed
			now := time.Now()
			var flushDeadlineTimerCh <-chan time.Time
			for {
				nextBCToFlush := cm.NextToFlush()
				if nextBCToFlush == nil {
					break
				}
				deadline := nextBCToFlush.FlushDeadline
				if now.Before(deadline) {
					flushDeadlineTimer := j.timerPool.Get()
					defer j.timerPool.Put(flushDeadlineTimer)
					flushDeadlineTimer.Reset(deadline.Sub(now))
					flushDeadlineTimerCh = flushDeadlineTimer.C
					break
				}
				nextBCToFlush.FlushFunc(ctx)
			}

			select {
			case combine := <-primaryCh:
				res.GatheredOne = true
				res.Work = func(ctx context.Context) {
					executeCombine(ctx, combine)
				}
			case combine := <-cp.secondaryChan:
				res.GatheredOne = true
				res.Work = func(ctx context.Context) {
					executeCombine(ctx, combine)
				}
			case <-nextJobFlushCh:
				res.Work = flushAll
			case <-flushDeadlineTimerCh:
				// A combiner has reached its deadline - flush it
				bc := cm.NextToFlush()
				if bc != nil && !time.Now().Before(bc.FlushDeadline) {
					res.Work = bc.FlushFunc
				}
			case <-timerCh:
				// This goroutine is no longer needed.
				res.TimedOut = true
			case <-waiter.Done():
				// Retry per backpressureProvider.Block
				res.WaiterNotified = true
			case <-limitChangeCh:
				// Retry per backpressureProvider.Block
			case <-j.state.Done():
				res.JobDone = true
			case <-ctx.Done():
				err = ctx.Err()
			case <-goroutineCtx.Done():
				err = goroutineCtx.Err()
			}
			return res, err
		}

		bp := combineBackpressureProvider{
			job: j,
			tryCombineOne: func(ctx context.Context) (bool, error) {
				return tryCombineOne(ctx)
			},
			combineOne: func(ctx context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (blockResult, error) {
				res, err := combineOne(ctx, nil, waiter, limitChangeCh)
				var work workFunc
				if res.Work != nil {
					work = func(ctx context.Context) error {
						res.Work(ctx)
						return nil
					}
				}
				return blockResult{
					WaiterNotified: res.WaiterNotified,
					Work:           work,
				}, err
			},
		}
		goroutineCtx = withBackpressureProvider(j.ctx, bp)

		// Flush sends any pending results from the combiner if needed. It also
		// decrements the combiner counter to ensure that the job is not kept
		// alive if there's nothing left to flush.
		flushAll = func(ctx context.Context) {
			if nextJobFlushCh != nil {
				// Call the combiner's Flush method
				cm.FlushAll(ctx)
				nextJobFlushCh = nil
				unregisterAsJobFlusher()
			}
		}

		// Ensure combiner is flushed as needed when this goroutine terminates.
		defer flushAll(goroutineCtx)

		executeCombine = func(ctx context.Context, combine boundCombineFunc) {
			if nextJobFlushCh == nil {
				// Make sure the job won't terminate before the combiner is flushed
				nextJobFlushCh, unregisterAsJobFlusher = j.state.RegisterFlusher()
			}
			combine(ctx, &cm)
		}
		executeCombine(goroutineCtx, combine)

		var timer *time.Timer
		for {
			if isSecondary {
				isSecondary = cp.secondaryElected.CompareAndSwap(false, true)
				if isSecondary {
					primaryCh = nil
					timer = j.timerPool.Get()
					defer j.timerPool.Put(timer)
					defer cp.secondaryElected.Store(false)
				}
			}

			var timerCh <-chan time.Time
			if isSecondary {
				// Capture the current idle timeout value to ensure consistency
				idleTimeout := cp.idleTimeout

				if idleTimeout >= 0 {
					// This is the goroutine that has elected itself to read only
					// the secondary channel. This will keep this goroutine idle
					// unless it's really needed, thus allowing the idle timeout to
					// elapse (if enabled).
					timer.Reset(idleTimeout)
					timerCh = timer.C
				}
			}

			// Ignore errors from combineOne, since they would only be due to
			// canceled contexts
			res, err := combineOne(goroutineCtx, timerCh, state.Waiter{}, nil)
			if res.Work != nil {
				res.Work(goroutineCtx)
			}
			if res.TimedOut || res.JobDone || err != nil {
				// This goroutine should exit.
				return
			}
		}
	}()
	return true
}

type boundCombineFunc func(ctx context.Context, cm *combinerMap)

type halfBoundCombineFunc[I any] func(ctx context.Context, input I, inputErr error)

type boundCombiner struct {
	CombineFunc   any
	FlushFunc     func(ctx context.Context)
	FirstCombine  time.Time // When first unflushed input was received (for maxHoldTime)
	FlushDeadline time.Time // The earliest time this combiner should be flushed
	heapPosition  int       // Position in the deadline heap, 0 if not in heap
}

// Less implements heap.Item interface
func (bc *boundCombiner) Less(other *boundCombiner) bool {
	return bc.FlushDeadline.Before(other.FlushDeadline)
}

// SetPosition implements heap.Item interface
func (bc *boundCombiner) SetPosition(position int) {
	bc.heapPosition = position
}

// Position implements heap.Item interface
func (bc *boundCombiner) Position() int {
	return bc.heapPosition
}

type combinerMap struct {
	m         map[combinerMapKey]*boundCombiner
	deadlines heap.Heap[*boundCombiner]
}

type combinerMapKey struct {
	Job     *Job
	Combine any
}

func getCombineFunc[I any, O any](cm *combinerMap, j *Job, c *Combine[I, O]) halfBoundCombineFunc[I] {
	k := combinerMapKey{
		Job:     j,
		Combine: c,
	}
	bc := cm.m[k]
	var combineFunc halfBoundCombineFunc[I]
	if bc != nil {
		combineFunc = bc.CombineFunc.(halfBoundCombineFunc[I])
	} else {
		combiner := c.combinerFactory()
		emit := func(ctx context.Context, output O, outputErr error) {
			// Bind the gatherFunc to the combiner output
			gather := func(ctx context.Context) error {
				return c.gather.gatherFunc(ctx, output, outputErr)
			}

			// The job's in-flight task counter will be decremented by
			// Job.executeGather, so we must increment it to keep the job alive
			// until the gather happens.
			j.state.IncrementTasks()

			// Post the bound gather to the job's gather channel.
			select {
			case j.gatherChan <- gather:
			case <-ctx.Done():
			case <-j.ctx.Done():
			}
		}

		// Initialize the map if needed
		if cm.m == nil {
			cm.m = make(map[combinerMapKey]*boundCombiner)
		}

		// Create the boundCombiner first
		bc = &boundCombiner{}

		// Define the combineFunc with access to bc
		combineFunc = func(ctx context.Context, input I, inputErr error) {
			now := time.Now()

			// If this is the first combine since last flush, record the time
			if bc.FirstCombine.IsZero() {
				bc.FirstCombine = now
			}

			// Calculate flush deadline based on min/max hold times
			var deadline time.Time

			// Calculate minHoldTime deadline (time since this combine operation)
			if c.minHoldTime >= 0 {
				deadline = now.Add(c.minHoldTime)
			}

			// Calculate maxHoldTime deadline (time since first combine)
			if c.maxHoldTime >= 0 {
				maxDeadline := bc.FirstCombine.Add(c.maxHoldTime)
				// Use maxDeadline if it's earlier or if no min deadline yet
				if deadline.IsZero() || maxDeadline.Before(deadline) {
					deadline = maxDeadline
				}
			}

			cm.UpdateFlushDeadline(bc, deadline)

			didNotPanic := false
			defer func() {
				if !didNotPanic {
					// Just in case the panic is otherwise suppressed
					emit(ctx, *new(O), ErrCombinePanic)
				}

				// The job's in-flight task counter must be decremented here
				// just as it is in Job.executeGather.
				j.state.DecrementTasks()
			}()

			combiner.Combine(ctx, input, inputErr, emit)
			didNotPanic = true
		}

		// Store the combineFunc in the boundCombiner
		bc.CombineFunc = combineFunc

		// Define the FlushFunc with access to bc
		bc.FlushFunc = func(ctx context.Context) {
			cm.Remove(k, bc) // Remove from both map and heap

			didNotPanic := false
			defer func() {
				if !didNotPanic {
					// Just in case the panic is otherwise suppressed
					emit(ctx, *new(O), ErrCombinerFlushPanic)
				}
			}()

			combiner.Flush(ctx, emit)
			didNotPanic = true
		}

		// Add the boundCombiner to the map
		cm.m[k] = bc
	}
	return combineFunc
}

func (cm *combinerMap) UpdateFlushDeadline(bc *boundCombiner, deadline time.Time) {
	bc.FlushDeadline = deadline
	if deadline.IsZero() {
		cm.deadlines.Remove(bc)
	} else {
		cm.deadlines.Push(bc)
	}
}

func (cm *combinerMap) NextToFlush() *boundCombiner {
	if cm.deadlines.Len() == 0 {
		return nil
	}
	// Peek at the earliest deadline
	return cm.deadlines.Peek()
}

// Remove removes a combiner from both the map and the deadline heap
func (cm *combinerMap) Remove(k combinerMapKey, bc *boundCombiner) {
	// Remove from the heap if it's there
	_ = cm.deadlines.Remove(bc)
	// Remove from the map
	delete(cm.m, k)
}

func (cm *combinerMap) FlushAll(ctx context.Context) {
	for _, bc := range cm.m {
		bc.FlushFunc(ctx)
	}
	cm.m = nil
	cm.deadlines = heap.Heap[*boundCombiner]{} // Reset to zero value
}
