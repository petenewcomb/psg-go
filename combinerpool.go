// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/basicq"
	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/cpstate"
	"github.com/petenewcomb/psg-go/internal/heap"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/waitq"
)

// Empirically determined but not widely validated, YMMV. Subject to change as broader experience is gained.
const DefaultCombinerPoolMeasurementTimeConstant = 50 * time.Millisecond
const DefaultCombinerPoolHistoryRetentionPeriod = 1 * time.Second
const DefaultCombinerPoolIdleTimeout = 100 * time.Microsecond
const DefaultCombinerPoolHighUtilizationThreshold = 0.6
const DefaultCombinerPoolMinimumReturn = 0.01
const DefaultCombinerPoolAggressiveGrowthFactor = 1.5
const DefaultCombinerPoolConservativeGrowthFactor = 1.1

// CombinerPool manages a pool of goroutines that execute combiners.
// It handles concurrency limits, spawning new goroutines, and reusing existing ones.
type CombinerPool struct {
	j           *Job
	idleTimeout time.Duration

	// CombinerPoolState hosts the data and core logic for managing the pool of
	// goroutines to maximize throughput with the minimum number of goroutines
	// and therefore duplication of individual combiners.
	state cpstate.CombinerPoolState

	// Scattered tasks first attempt to post their results to primaryQueue. If a
	// combiner goroutine is not immediately available, the task will
	// concurrently try posting to both primaryQueue and secondaryChan.
	//
	// Only one goroutine at a time can elect itself "secondary". Once elected,
	// the secondary goroutine no longer listens to primaryQueue and will
	// therefore receive task results only if the other goroutines are too busy
	// to immediately receive all results being posted to primaryQueue. This
	// allows the secondary goroutine to detect if its capacity is no longer
	// needed by staying idle until idleTimeout has passed. If this happens, the
	// secondary goroutine resets secondaryElected to false and exits, allowing
	// a different goroutine to elect itself secondary and continue the idle
	// detection process.
	primaryQueue     rdvq.Required[pendingCombine]
	secondaryQueue   rdvq.Optional[pendingCombine]
	secondaryChan    chan pendingCombine
	secondaryElected atomic.Bool

	waitingCombines jobstate.InFlightCounter
	combineWaiters  waitq.Queue
}

// NewCombinerPool creates a new CombinerPool bound to the specified job.
//
// Panics if the job is nil or in the done state.
func NewCombinerPool(job *Job) *CombinerPool {
	// Check if the job is done
	job.panicIfDone()
	cp := &CombinerPool{
		j:             job,
		idleTimeout:   DefaultCombinerPoolIdleTimeout,
		secondaryChan: make(chan pendingCombine),
	}
	cp.state.SetLimits(0, -1) // unlimited by default
	cp.state.SetHighUtilizationThreshold(DefaultCombinerPoolHighUtilizationThreshold)
	cp.state.SetMeasurementTimeConstant(DefaultCombinerPoolMeasurementTimeConstant)
	cp.state.SetHistoryRetentionPeriod(DefaultCombinerPoolHistoryRetentionPeriod)
	cp.state.SetMinimumReturn(DefaultCombinerPoolMinimumReturn)
	cp.state.SetGrowthFactors(DefaultCombinerPoolAggressiveGrowthFactor, DefaultCombinerPoolConservativeGrowthFactor)
	cp.primaryQueue.Init(combineQueuePool)
	cp.secondaryQueue.Init(combineQueuePool)
	cp.combineWaiters.Init()
	return cp
}

// checkInitialized panics if the CombinerPool was not properly initialized via NewCombinerPool
func (cp *CombinerPool) checkInitialized() {
	if cp.j == nil {
		panic("CombinerPool not initialized: must use NewCombinerPool")
	}
}

// SetLimit sets the active concurrency limit for the pool. A negative value means no
// limit (combiners will always be launched regardless of how many are currently
// running). Zero means no new combiners will be launched until SetLimit is called
// with a non-zero value.
//
// This method is safe to call at any time. The new limit takes effect immediately
// for subsequent combiner launches and may unblock existing blocked operations.
func (cp *CombinerPool) SetLimits(minConcurrency, maxConcurrency int) {
	cp.checkInitialized()
	cp.state.SetLimits(minConcurrency, maxConcurrency)
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
	cp.checkInitialized()
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
func (cp *CombinerPool) SetMeasurementTimeConstant(d time.Duration) {
	cp.checkInitialized()
	cp.state.SetMeasurementTimeConstant(d)
}

// SetHighUtilizationThreshold sets the utilization threshold above which a
// combiner goroutine is considered highly utilized. This affects when new
// goroutines are spawned to handle load.
//
// The default value is [DefaultCombinerPoolHighUtilizationThreshold].
func (cp *CombinerPool) SetHighUtilizationThreshold(threshold float64) {
	cp.checkInitialized()
	cp.state.SetHighUtilizationThreshold(threshold)
}

// SetHistoryRetentionPeriod sets how long performance samples are retained
// for analysis. Older samples are discarded to adapt to changing workload
// patterns.
//
// The default value is [DefaultCombinerPoolHistoryRetentionPeriod].
func (cp *CombinerPool) SetHistoryRetentionPeriod(d time.Duration) {
	cp.checkInitialized()
	cp.state.SetHistoryRetentionPeriod(d)
}

// SetMinimumReturn sets the threshold ratio for detecting the throughput knee:
// the point at which the return from each additional unit of capacity has
// diminished to the point of no longer being worth the investment.
//
// The default value is [DefaultCombinerPoolMinimumReturn].
func (cp *CombinerPool) SetMinimumReturn(ratio float64) {
	cp.checkInitialized()
	cp.state.SetMinimumReturn(ratio)
}

// SetGrowthFactors sets the multipliers used when scaling up the number
// of combiner goroutines. The aggressive factor is used when all existing
// goroutines show high utilization with linear scaling. The conservative
// factor is used for more cautious scaling.
//
// The default values are [DefaultCombinerPoolAggressiveGrowthFactor] and
// [DefaultCombinerPoolConservativeGrowthFactor].
func (cp *CombinerPool) SetGrowthFactors(aggressive, conservative float64) {
	cp.checkInitialized()
	cp.state.SetGrowthFactors(aggressive, conservative)
}

var combineQueuePool = &rdvq.Pool[pendingCombine]{}

func (cp *CombinerPool) postCombine(ctx context.Context, combine boundCombineFunc) {
	if cp.state.MaybeSpawnGoroutine() {
		cp.spawnNewCombiner(combine)
		return
	}

	pc := pendingCombine{fn: combine, releaseWaiters: false}
	cp.primaryQueue.PushBackFunc(combineQueuePool, pc, func(primaryCh chan<- pendingCombine, pc pendingCombine) {
		// Attempt to post to the secondary queue, else fall back to the slow path.
		if !cp.secondaryQueue.TryPushBack(combineQueuePool, pc) {
			cp.postCombineSlow(ctx, primaryCh, pc.fn)
		}
	})
}

func (cp *CombinerPool) releaseWaiters() {
	if cp.waitingCombines.Decrement() {
		// No more waiting combines, so release all remaining
		// combine waiters
		cp.combineWaiters.NotifyAll()
	} else {
		// Release just one combine waiter, since there are still
		// other waiting combines.
		cp.combineWaiters.Notify()
	}
}

func (cp *CombinerPool) postCombineSlow(ctx context.Context, primaryCh chan<- pendingCombine, combine boundCombineFunc) {

	// We don't attempt the primary channel alone here since both fast paths
	// failed, meaning both primary and secondary goroutines are likely busy. At
	// this point spillage to secondary is warranted, so we use
	// first-come-first-served among whatever becomes available.

	waitersIncremented := false
	defer func() {
		// If we incremented waiters but didn't successfully send to a channel,
		// we must release the waiters ourselves
		if waitersIncremented {
			cp.releaseWaiters()
		}
	}()

	for {
		spawnWaitCh := cp.state.ShouldSpawnGoroutine()
		if spawnWaitCh == nil {
			cp.spawnNewCombiner(combine)
			return
		}

		// If we get here, the primary and secondary channels were busy and we
		// hit the limit of how many combiner tasks we can launch or need to
		// wait before we can spawn another. Increment the waiting task count to
		// signal Scatter to apply backpressure.
		if !waitersIncremented {
			cp.waitingCombines.Increment()
			waitersIncremented = true
		}

		// Create pendingCombine with releaseWaiters=true since we incremented the counter
		pc := pendingCombine{
			fn:             combine,
			releaseWaiters: true,
		}

		// Block until we can post or it's time to retry
		select {
		case primaryCh <- pc:
			waitersIncremented = false // Successfully sent, don't release in defer
			return
		case cp.secondaryChan <- pc:
			waitersIncremented = false // Successfully sent, don't release in defer
			return
		case <-spawnWaitCh:
		case <-ctx.Done():
			return
		}
	}
}

func (cp *CombinerPool) spawnNewCombiner(combine boundCombineFunc) {
	j := cp.j
	nextJobFlushCh, unregisterAsJobFlusher := j.state.RegisterFlusher()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()

		isSecondary := false

		// Create the base goroutine context
		goroutineCtx, cancelGoroutineCtx := context.WithCancel(j.ctx)
		// deferred call to cancel below

		// Minimize the number of channels on which this goroutine must repeatedly
		// retrieve and select to reduce contention.
		doneCh := make(chan struct{})
		var doneChErr error
		var doneWg sync.WaitGroup
		doneWg.Add(1)
		go func() {
			defer doneWg.Done()
			select {
			case <-j.state.Done():
				doneChErr = ErrJobDone
			case <-goroutineCtx.Done():
				doneChErr = goroutineCtx.Err()
			}
			close(doneCh)
		}()
		defer func() {
			cancelGoroutineCtx()
			doneWg.Wait()
		}()

		var cm combinerMap
		type combineWorkFunc func(ctx context.Context)
		type combineWorkItem struct {
			id     int64
			workFn combineWorkFunc
		}
		var workQueue basicq.Queue[combineWorkItem]
		var workCounter int64

		// Pending scatter queue for backpressure handling
		type pendingScatterFunc func(ctx context.Context) error
		var pendingScatters basicq.Queue[pendingScatterFunc]

		// More forward references
		var executeCombine func(ctx context.Context, combine boundCombineFunc)
		var flushAll func(ctx context.Context)

		queueWork := func(workFn combineWorkFunc) {
			workCounter++
			workQueue.PushBack(combineWorkItem{
				id:     workCounter,
				workFn: workFn,
			})
		}

		flushToNextDeadline := func() time.Duration {
			for {
				nextBCToFlush := cm.NextToFlush()
				if nextBCToFlush == nil {
					break
				}
				deadline := nextBCToFlush.FlushDeadline
				timeLeft := time.Until(deadline)
				if timeLeft > 0 {
					return timeLeft
				}
				// Remove from heap immediately to prevent infinite loop
				cm.deadlines.Remove(nextBCToFlush)
				queueWork(nextBCToFlush.FlushFunc)
			}
			return 0
		}

		queueCombine := func(pc pendingCombine) {
			if pc.releaseWaiters {
				cp.releaseWaiters()
			}

			queueWork(func(ctx context.Context) {
				executeCombine(ctx, pc.fn)
			})
		}

		tryCombineOne := func(ctx context.Context) bool {

			if isSecondary {
				gotCombine := false
				cp.secondaryQueue.TryPopFront(ctx, combineQueuePool, func(pc pendingCombine) {
					queueCombine(pc)
					gotCombine = true
				})
				if gotCombine {
					return true
				}

				select {
				case pc := <-cp.secondaryChan:
					queueCombine(pc)
					return true
				default:
				}
				return false
			}

			// Not secondary
			ok := false
			// Overrides queueCombine defined above to include setting ok flag
			queueCombine := func(pc pendingCombine) {
				ok = true
				// References previously defined version above
				queueCombine(pc)
			}
			cp.primaryQueue.PopFrontFunc(combineQueuePool, queueCombine,
				func(dedicatedPrimaryCh, sharedPrimaryCh <-chan pendingCombine) <-chan pendingCombine {
					select {
					case pc := <-dedicatedPrimaryCh:
						queueCombine(pc)
						return dedicatedPrimaryCh
					case pc := <-sharedPrimaryCh:
						queueCombine(pc)
					case pc := <-cp.secondaryChan:
						// Primary may steal from secondary, but not vice-versa
						queueCombine(pc)
					default:
					}
					return nil
				},
			)
			return ok
		}

		combineOne := func(ctx context.Context, idleTimerCh <-chan time.Time, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error) {

			// Check if any combiners need to be flushed due to deadlines and
			// set up flush deadline timer if needed
			var flushDeadlineTimerCh <-chan time.Time
			if timeUntilNextDeadline := flushToNextDeadline(); timeUntilNextDeadline > 0 {
				flushDeadlineTimer := timerp.Get()
				defer timerp.Put(flushDeadlineTimer)
				flushDeadlineTimer.Reset(timeUntilNextDeadline)
				flushDeadlineTimerCh = flushDeadlineTimer.C
			}

			if isSecondary {
				waiterNotified := false
				var err error
				cp.secondaryQueue.PopFrontFunc(combineQueuePool, queueCombine,
					func(dedicatedCh <-chan pendingCombine) bool {
						// Secondary goroutines only listen to secondaryChan and other events
						waitStartTime := time.Now()
						cp.state.SecondaryWaitStarted(waitStartTime)
						defer cp.state.SecondaryWaitEnded(waitStartTime)
						dedicatedChWasSource := false
						waiterNotified = waiter.Wait(func(waitCh <-chan struct{}) bool {
							select {
							case pc := <-dedicatedCh:
								queueCombine(pc)
								dedicatedChWasSource = true
							case pc := <-cp.secondaryChan:
								queueCombine(pc)
							case <-nextJobFlushCh:
								queueWork(flushAll)
							case <-flushDeadlineTimerCh:
								// At least one combiner has reached its flush deadline
								flushToNextDeadline()
							case <-idleTimerCh:
								// This goroutine may no longer be needed. Notify any
								// potential waiters and exit. Combiners will be flushed via
								// the deferred call to flushAll below.
								if cp.state.ShouldExitGoroutine() {
									err = errIdleTimeout
								}
							case <-waitCh:
								return true
							case <-changeCh:
							case <-doneCh:
								err = doneChErr
							case <-ctx.Done():
								err = ctx.Err()
							}
							return false
						})
						return dedicatedChWasSource
					},
				)
				if err == errIdleTimeout && workQueue.Len() > 0 {
					// Don't exit if we still have work to do.
					err = nil
				}
				return waiterNotified, err
			}

			// Not secondary
			waiterNotified := false
			var err error
			cp.primaryQueue.PopFrontFunc(combineQueuePool, queueCombine,
				func(dedicatedCh, sharedCh <-chan pendingCombine) (sourceCh <-chan pendingCombine) {
					waiterNotified = waiter.Wait(func(waitCh <-chan struct{}) bool {
						select {
						case pc := <-dedicatedCh:
							queueCombine(pc)
							sourceCh = dedicatedCh
						case pc := <-sharedCh:
							queueCombine(pc)
						case pc := <-cp.secondaryChan:
							// Primary may steal from secondary, but not vice-versa
							queueCombine(pc)
						case <-nextJobFlushCh:
							queueWork(flushAll)
						case <-flushDeadlineTimerCh:
							// At least one combiner has reached its deadline
							flushToNextDeadline()
						case <-waitCh:
							return true
						case <-changeCh:
						case <-doneCh:
							err = doneChErr
						case <-ctx.Done():
							err = ctx.Err()
						}
						return false
					})
					return sourceCh
				},
			)
			return waiterNotified, err
		}

		var combineOneAndDoTheWork func(ctx context.Context, topLevel bool, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error)

		bp := combineBackpressureProvider{
			job: j,
			tryCombineOne: func(ctx context.Context) (bool, error) {
				return tryCombineOne(ctx), nil
			},
			combineOne: func(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error) {
				return combineOneAndDoTheWork(ctx, false, waiter, changeCh)
			},
			queueWork: func(workFunc func(context.Context) error) {
				// Queue scatter functions to be executed later rather than immediately
				pendingScatters.PushBack(workFunc)
			},
		}

		backpressureCtx := withBackpressureProvider(goroutineCtx, bp)

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
		defer flushAll(backpressureCtx)

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

		var idleTimer *time.Timer

		combineOneAndDoTheWork = func(ctx context.Context, topLevel bool, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error) {
			lastIDToProcess := workCounter
			for {
				work, ok := workQueue.PopFront()
				if !ok {
					break
				}
				work.workFn(ctx)
				if work.id >= lastIDToProcess {
					break
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

			// Execute a pending scatter if this is a top-level call
			if topLevel {
				scatterFunc, ok := pendingScatters.PopFront()
				if ok {
					if err := scatterFunc(ctx); err != nil {
						// Analysis: All pending scatter functions originate from scatters called within
						// combiner execution context. Even if it's a gather.Scatter() call, it uses the
						// combiner's backpressure provider (not the job's default one).
						//
						// The combiner backpressure provider only returns errors from:
						// - bp.Yield() -> tryCombineOne() -> always returns (bool, nil)
						// - bp.Block() -> combineOneAndDoTheWork() -> can return context/job cancellation errors
						//
						// Therefore, the only possible errors are shutdown-related:
						// - context.Canceled/DeadlineExceeded (context cancellation)
						// - ErrJobDone (job completion)
						// - errIdleTimeout (combiner idle timeout)
						//
						// No user gather function errors occur here since the gather is only queued,
						// not executed. Gather execution happens later in the job's work queue.
						//
						// These are all expected shutdown conditions, so no special handling needed.
						switch err {
						case context.Canceled, context.DeadlineExceeded, ErrJobDone, errIdleTimeout:
						default:
							panic(fmt.Sprintf("unexpected error from deferred scatter: %v", err))
						}
					}
				}
			}

			// Add any pending flushes to the work queue before deciding whether
			// we need to block.
			flushToNextDeadline()

			// No need to report errors from combineOne, since they would only
			// be due to canceled contexts. Other errors are posted to be
			// gathered.
			var ok bool
			var err error
			if workCounter > lastIDToProcess || (topLevel && pendingScatters.Len() > 0) {
				// There's still work in the queue OR pending scatters, use tryCombineOne to avoid blocking
				ok = tryCombineOne(ctx)
			} else {
				ok, err = combineOne(ctx, idleTimerCh, waiter, changeCh)
			}
			return ok, err
		}

		executeCombine(backpressureCtx, combine)

		for {
			if !isSecondary {
				if cp.secondaryElected.CompareAndSwap(false, true) {
					isSecondary = true
					idleTimer = timerp.Get()
					defer timerp.Put(idleTimer)
					defer cp.secondaryElected.Store(false)
				}
			}

			if _, err := combineOneAndDoTheWork(backpressureCtx, true, waitq.Waiter{}, nil); err != nil {
				switch err {
				case ErrJobDone, errIdleTimeout, context.Canceled:
				default:
					panic(fmt.Sprintf("combiner goroutine exiting with unexpected error: %v", err))
				}
				if workQueue.Len() != 0 {
					panic("exiting with work in queue")
				}
				break
			}
		}
	}()
}

const errIdleTimeout = cerr.Error("idle timeout reached")

type boundCombineFunc func(ctx context.Context, cm *combinerMap)

type pendingCombine struct {
	fn             boundCombineFunc
	releaseWaiters bool
}

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

func getCombineFunc[I, O any](ctx context.Context, cm *combinerMap, cp *CombinerPool, c *Combine[I, O]) halfBoundCombineFunc[I] {
	j := cp.j
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

			// The job's in-flight work counter will be decremented by
			// Job.executeGather, so we must increment it to keep the job alive
			// until the gather happens.
			j.state.IncrementWork()

			// Post the bound gather to the job's gather queue.
			j.postGather(ctx, gather)
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

				// The job's in-flight work counter must be decremented here
				// just as it is in Job.executeGather.
				j.state.DecrementWork()
			}()

			combiner.Combine(ctx, input, inputErr, emit)
			didNotPanic = true
		}

		// Store the combineFunc in the boundCombiner
		bc.CombineFunc = combineFunc

		// Define the FlushFunc with access to bc
		bc.FlushFunc = func(ctx context.Context) {
			// Reset FirstCombine for next batch, but keep combiner in map for reuse
			bc.FirstCombine = time.Time{}

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
