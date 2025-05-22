// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/basicq"
	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/dynval"
	"github.com/petenewcomb/psg-go/internal/heap"
	"github.com/petenewcomb/psg-go/internal/state"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/waitq"
)

// Empirically determined but not widely validated, YMMV. Subject to change as broader experience is gained.
const DefaultCombinerThroughputMeasurementWindow = 50 * time.Millisecond

// Empirically determined but not widely validated, YMMV. Subject to change as broader experience is gained.
const DefaultCombinerGoroutineIdleTimeout = 10 * time.Millisecond

// CombinerPool manages a pool of goroutines that execute combiners.
// It handles concurrency limits, spawning new goroutines, and reusing existing ones.
type CombinerPool struct {
	job              *Job
	concurrencyLimit dynval.Value[int]
	idleTimeout      time.Duration

	// CombinerPoolState hosts the data and core logic for managing the pool of
	// goroutines to maximize throughput while minimizes the number of
	// outstanding goroutines and therefore duplication of individual combiners.
	state state.CombinerPoolState

	// Scattered tasks first attempt to post their results to primaryChan. If a
	// combiner goroutine is not immediately available, the task will
	// concurrently try posting to both primaryChan and secondaryChan.
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
	combineWaiters  waitq.Queue
}

// NewCombinerPool creates a new CombinerPool with the specified concurrency limit.
func NewCombinerPool(job *Job) *CombinerPool {
	if job == nil {
		panic("job is nil")
	}
	cp := &CombinerPool{
		job:           job,
		idleTimeout:   DefaultCombinerGoroutineIdleTimeout,
		primaryChan:   make(chan boundCombineFunc),
		secondaryChan: make(chan boundCombineFunc),
	}
	cp.concurrencyLimit.Store(-1) // unlimited by default
	cp.state.SetThroughputMeasurementWindow(DefaultCombinerThroughputMeasurementWindow)
	cp.combineWaiters.Init()
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
// A positive value specifies how long a goroutine should wait while idle before
// exiting. Shorter timeouts reduce resource usage but may require more frequent
// spawning of new goroutines and result in more frequent flushing. Longer
// timeouts keep goroutines available for longer but use more resources. A value
// of -1 disables idle timeouts completely, causing all goroutines to remain
// alive until the job completes. This maximizes combining efficiency but uses
// more resources. A value of 0 means goroutines may exit as soon as they become
// idle, though is perhaps useful only when testing edge cases. The default
// value is [DefaultCombinerGoroutineIdleTimeout].
//
// This method is safe to call at any time. However, the timing
// of when the new value takes effect within a running job is undefined.
func (cp *CombinerPool) SetIdleTimeout(timeout time.Duration) {
	if timeout < -1 {
		panic(fmt.Sprintf("invalid idle timeout %v: must be >= -1", timeout))
	}
	cp.idleTimeout = timeout
}

// SetThroughputMeasurementWindow sets the period of time over which combiner
// throughput is measured as input to the algorithm that determines whether or
// not to launch new combiner goroutines (if allowed by the concurrency limit).
// This algorithm works to maximize overall throughput while minimizing the
// number of combiner goroutines.
//
// Each combiner goroutine combines values independently, and more goroutines
// means more independent combiners. This increases parallelism but may result
// in more, smaller batches of combined values.
//
// When a combiner pool begins receiving results from tasks, it will immediately
// start the first goroutine but must then wait for a measurement window period
// to pass before it can launch a second, which it will do only if the first is
// highly utilized. One more measurement window period must pass before the
// algorithm may launch a third. The third will be launched only if both running
// goroutines are highly utilized and the second improved overall throughput. A
// fourth will be started only if the third also improved throughput, and so on.
// Higher values for this setting will therefore slow ramp-up but reduce
// overshoot and fluctuation, conversely, lower values will speed initial
// ramp-up but may cause jitter that can dramatically reduce throughput due to
// both resource contention and excessive gather load due to combiner flushes as
// superfluous goroutines exit.
//
// The default value for this setting is
// [DefaultCombinerThroughputMeasurementWindow].
//
// This method is safe to call at any time and its value will take effect
// immediately. If the value is raised, spawning of new goroutines will be
// delayed as described for ramp-up above until data to cover the new window
// size can be gathered.
func (cp *CombinerPool) SetThroughputMeasurementWindow(d time.Duration) {
	cp.state.SetThroughputMeasurementWindow(d)
}

func (cp *CombinerPool) postCombine(ctx context.Context, combine boundCombineFunc) {
	// Attempt to post the combine to the primary channel.
	select {
	case cp.primaryChan <- combine:
		return
	case <-ctx.Done():
		return
	case <-cp.job.ctx.Done():
		return
	default:
	}

	// Attempt to post the combine to the primary or secondary channels.
	select {
	case cp.primaryChan <- combine:
		return
	case cp.secondaryChan <- combine:
		return
	case <-ctx.Done():
		return
	case <-cp.job.ctx.Done():
		return
	default:
	}

	waiting := false
	defer func() {
		if waiting {
			cp.waitingCombines.Decrement()
			cp.combineWaiters.Notify()
		}
	}()
	for {
		concurrencyLimit, concurrencyLimitChangeCh := cp.concurrencyLimit.Load()

		spawnWaitCh := cp.state.ShouldSpawnGoroutine(concurrencyLimit)
		if spawnWaitCh == nil {
			cp.spawnNewCombiner(combine)
			return
		}

		// If we get here, the primary and secondary channels were busy and we
		// hit the limit of how many combiner tasks we can launch or need to
		// wait before we can spawn another. Increment the waiting task count to
		// signal Scatter to apply backpressure.
		if !waiting {
			cp.waitingCombines.Increment()
			waiting = true
		}

		// Block until we can post or it's time to retry
		select {
		case cp.primaryChan <- combine:
			return
		case cp.secondaryChan <- combine:
			return
		case <-spawnWaitCh:
		case <-concurrencyLimitChangeCh:
		case <-ctx.Done():
			return
		case <-cp.job.ctx.Done():
			return
		}
	}
}

func (cp *CombinerPool) spawnNewCombiner(combine boundCombineFunc) {
	j := cp.job
	nextJobFlushCh, unregisterAsJobFlusher := j.state.RegisterFlusher()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()

		var isSecondary bool

		// Will become nil if this goroutine becomes secondary
		primaryCh := cp.primaryChan

		// Initialized with backpressureProvider below
		var goroutineCtx context.Context

		var cm combinerMap
		type combineWorkFunc func(ctx context.Context)
		var workQueue basicq.Queue[combineWorkFunc]

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

		combineOne := func(ctx context.Context, idleTimerCh <-chan time.Time, waiter waitq.Waiter, limitChangeCh <-chan struct{}) (bool, error) {
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
					flushDeadlineTimer := timerp.Get()
					defer timerp.Put(flushDeadlineTimer)
					flushDeadlineTimer.Reset(deadline.Sub(now))
					flushDeadlineTimerCh = flushDeadlineTimer.C
					break
				}
				nextBCToFlush.FlushFunc(ctx)
			}

			waitStart := time.Now()
			defer func() {
				cp.state.AddWaitTime(time.Since(waitStart))
			}()

			select {
			case combine := <-primaryCh:
				workQueue.PushBack(func(ctx context.Context) {
					executeCombine(ctx, combine)
				})
			case combine := <-cp.secondaryChan:
				workQueue.PushBack(func(ctx context.Context) {
					executeCombine(ctx, combine)
				})
			case <-nextJobFlushCh:
				workQueue.PushBack(flushAll)
			case <-flushDeadlineTimerCh:
				// A combiner has reached its deadline
				workQueue.PushBack(func(ctx context.Context) {
					for {
						bc := cm.NextToFlush()
						if bc == nil || time.Now().Before(bc.FlushDeadline) {
							break
						}
						bc.FlushFunc(ctx)
					}
				})
			case <-idleTimerCh:
				// This goroutine is no longer needed. Notify any potential
				// waiters and exit. Combiners will be flushed via the deferred
				// call to flushAll below.
				return false, errIdleTimeout
			case <-waiter.Done():
				return true, nil
			case <-limitChangeCh:
			case <-j.state.Done():
				return false, ErrJobDone
			case <-ctx.Done():
				return false, ctx.Err()
			case <-goroutineCtx.Done():
				return false, goroutineCtx.Err()
			}
			return false, nil
		}

		bp := combineBackpressureProvider{
			job: j,
			tryCombineOne: func(ctx context.Context) (bool, error) {
				return tryCombineOne(ctx)
			},
			combineOne: func(ctx context.Context, waiter waitq.Waiter, limitChangeCh <-chan struct{}) (bool, error) {
				return combineOne(ctx, nil, waiter, limitChangeCh)
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
			cp.state.IncrementCompleted()
		}

		cp.state.GoroutineStarted()
		defer cp.state.GoroutineExited()

		executeCombine(goroutineCtx, combine)

		var idleTimer *time.Timer
		for {
			for {
				work, ok := workQueue.PopFront()
				if !ok {
					break
				}
				work(goroutineCtx)
			}

			if !isSecondary {
				isSecondary = cp.secondaryElected.CompareAndSwap(false, true)
				if isSecondary {
					primaryCh = nil
					idleTimer = timerp.Get()
					defer timerp.Put(idleTimer)
					defer cp.secondaryElected.Store(false)
				}
			}

			var idleTimerCh <-chan time.Time
			if isSecondary {
				// Capture the current idle timeout value to ensure consistency
				idleTimeout := cp.idleTimeout
				if idleTimeout >= 0 {
					// This is the goroutine that has elected itself to read only
					// the secondary channel. This will keep this goroutine idle
					// unless it's really needed, thus allowing the idle timeout to
					// elapse (if enabled).
					idleTimer.Reset(idleTimeout)
					idleTimerCh = idleTimer.C
				}
			}

			// No need to report errors from combineOne, since they would only
			// be due to canceled contexts. Other errors are posted to be
			// gathered.
			_, err := combineOne(goroutineCtx, idleTimerCh, waitq.Waiter{}, nil)
			if err != nil {
				// This goroutine should exit.
				return
			}
		}
	}()
}

const errIdleTimeout = cerr.Error("idle timeout reached")

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

func getCombineFunc[I, O any](ctx context.Context, cm *combinerMap, j *Job, c *Combine[I, O]) halfBoundCombineFunc[I] {
	k := combinerMapKey{
		Job:     j,
		Combine: c,
	}
	bc := cm.m[k]
	var combineFunc halfBoundCombineFunc[I]
	if bc != nil {
		combineFunc = bc.CombineFunc.(halfBoundCombineFunc[I])
	} else {
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

		combiner := func() Combiner[I, O] {
			panicked := true
			defer func() {
				if panicked {
					emit(ctx, *new(O), ErrCombinerFactoryPanicked)
				}
			}()
			combiner := c.newCombiner()
			panicked = false
			if combiner == nil {
				emit(ctx, *new(O), ErrCombinerFactoryReturnedNil)
				combiner = &errCombiner[I, O]{err: ErrCombinerFactoryReturnedNil}
			}
			return combiner
		}()

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
					emit(ctx, *new(O), ErrCombinePanicked)
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
					emit(ctx, *new(O), ErrCombinerFlushPanicked)
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
