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
	"github.com/petenewcomb/psg-go/psgopt"
)

// CombinerPool manages a pool of goroutines that execute combiners.
// It handles concurrency limits, spawning new goroutines, and reusing existing ones.
type CombinerPool struct {
	j *Job

	// CombinerPoolState hosts the data and core logic for managing the pool of
	// goroutines to maximize throughput with the minimum number of goroutines
	// and therefore duplication of individual combiners.
	state cpstate.CombinerPoolState

	// Work posting uses three-tier delivery via combineQueue:
	// 1. Try immediate delivery to waiting receivers (fastest)
	// 2. If no waiting receiver, use sender's outbox (non-blocking)
	// 3. If outbox full, block on shared channel (backpressure)
	//
	// Primary goroutines register for immediate delivery, while the spare goroutine
	// uses PopFrontExcessFunc to process outboxes and shared channel without
	// registering for immediate delivery.
	//
	// Only one goroutine at a time can elect itself "spare" for scaling.
	combineQueue rdvq.Required[pendingCombine]
	spareElected atomic.Bool

	waitingCombines jobstate.InFlightCounter
	combineWaiters  rdvq.Waiters
}

// NewCombinerPool creates a new CombinerPool bound to the specified job.
//
// Panics if the job is nil or in the done state.
func NewCombinerPool(job *Job, options ...psgopt.CombinerPoolOption) *CombinerPool {
	// Check if the job is done
	job.panicIfDone()
	cp := &CombinerPool{
		j: job,
	}

	// Apply default configuration
	cp.state.SetOptions(
		psgopt.WithConcurrencyBounds(0, -1), // unlimited by default
		psgopt.WithHighUtilizationThreshold(psgopt.DefaultCombinerPoolHighUtilizationThreshold),
		psgopt.WithMeasurementTimeConstant(psgopt.DefaultCombinerPoolMeasurementTimeConstant),
		psgopt.WithHistoryRetentionPeriod(psgopt.DefaultCombinerPoolHistoryRetentionPeriod),
		psgopt.WithMinThroughputROI(psgopt.DefaultCombinerPoolMinThroughputROI),
		psgopt.WithGrowthFactors(psgopt.DefaultCombinerPoolAggressiveGrowthFactor, psgopt.DefaultCombinerPoolConservativeGrowthFactor),
		psgopt.WithIdleTimeout(psgopt.DefaultCombinerPoolIdleTimeout),
	)

	// Apply user options
	cp.state.SetOptions(options...)

	cp.combineQueue.Init(combineQueuePool)
	cp.combineWaiters.Init()

	return cp
}

// checkInitialized panics if the CombinerPool was not properly initialized via NewCombinerPool
func (cp *CombinerPool) checkInitialized() {
	if cp.j == nil {
		panic("CombinerPool not initialized: must use NewCombinerPool")
	}
}

// combineOutboxKey returns a unique identifier for this CombinerPool for combine outbox mapping.
func (cp *CombinerPool) combineOutboxKey() outboxKey[pendingCombine] {
	return cp
}

// SetOptions applies the given configuration options to the pool.
func (cp *CombinerPool) SetOptions(options ...psgopt.CombinerPoolOption) {
	cp.checkInitialized()
	cp.state.SetOptions(options...)
}

var combineQueuePool = &rdvq.Pool[pendingCombine]{}

func (cp *CombinerPool) postCombine(ctx context.Context, outbox *rdvq.Outbox[pendingCombine], combineFn boundCombine) {
	if cp.state.MaybeSpawnGoroutine() {
		cp.spawnNewCombiner(combineFn)
		return
	}
	pc := pendingCombine{fn: combineFn, releaseWaiters: false}
	tookSlowPath := false
	cp.combineQueue.PushBackFunc(combineQueuePool, outbox, pc, func(outboxCh chan<- pendingCombine) rdvq.SelectResult {
		// Fallback to slow path when outbox would block
		tookSlowPath = true
		return cp.postCombineSlow(ctx, outboxCh, pc.fn)
	})

	if !tookSlowPath && cp.state.ShouldStartFirstGoroutine() {
		cp.spawnNewCombiner(nil)
	}
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

func (cp *CombinerPool) postCombineSlow(ctx context.Context, outboxCh chan<- pendingCombine, combineFn boundCombine) rdvq.SelectResult {

	// We don't attempt the primary channel alone here since both fast paths
	// failed, meaning both primary and spare goroutines are likely busy. At
	// this point spillage to spare is warranted, so we use
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
			cp.spawnNewCombiner(combineFn)
			return rdvq.SelectAborted
		}

		// If we get here, the primary and spare channels were busy and we
		// hit the limit of how many combiner tasks we can launch or need to
		// wait before we can spawn another. Increment the waiting task count to
		// signal Scatter to apply backpressure.
		if !waitersIncremented {
			cp.waitingCombines.Increment()
			waitersIncremented = true
		}

		// Create pendingCombine with releaseWaiters=true since we incremented the counter
		pc := pendingCombine{
			fn:             combineFn,
			releaseWaiters: true,
		}

		// Block until we can post or it's time to retry
		select {
		case outboxCh <- pc:
			waitersIncremented = false // Successfully sent, don't release in defer
			return rdvq.SelectOutboxFilled
		case <-spawnWaitCh:
		case <-ctx.Done():
			return rdvq.SelectAborted
		}
	}
}

func (cp *CombinerPool) spawnNewCombiner(combineFn boundCombine) {
	j := cp.j
	nextJobFlushCh, unregisterAsJobFlusher := j.state.RegisterFlusher()
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()

		isSpare := false

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
		type combineWorkItem struct {
			id     int64
			workFn combineWork
		}
		var workQueue basicq.Queue[combineWorkItem]
		var workCounter int64

		// Pending scatter queue for backpressure handling
		type pendingScatter func(ctx context.Context) error
		var pendingScatters basicq.Queue[pendingScatter]

		// More forward references
		var executeCombineFn func(ctx context.Context, combineFn boundCombine)
		var flushAll func(ctx context.Context)

		queueWork := func(workFn combineWork) int64 {
			workCounter++
			workQueue.PushBack(combineWorkItem{
				id:     workCounter,
				workFn: workFn,
			})
			return workCounter
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
				queueWork(nextBCToFlush.FlushFn)
			}
			return 0
		}

		var backpressureCtx context.Context

		queueCombine := func(pc pendingCombine) {
			if pc.releaseWaiters {
				cp.releaseWaiters()
			}

			queueWork(func(ctx context.Context) {
				executeCombineFn(ctx, pc.fn)
			})
		}

		tryCombine := func(ctx context.Context) bool {

			if isSpare {
				// Spare goroutine tries non-blocking excess work processing
				if pc, ok := cp.combineQueue.TryPopFront(combineQueuePool); ok {
					queueCombine(pc)
					return true
				}
				return false
			}

			// Primary goroutine
			ok := false
			// Overrides queueCombine defined above to include setting ok flag
			queueCombine := func(pc pendingCombine) {
				ok = true
				// References previously defined version above
				queueCombine(pc)
			}
			cp.combineQueue.PopFrontFunc(combineQueuePool, queueCombine,
				func(inboxCh <-chan pendingCombine, waitCh <-chan struct{}) rdvq.SelectResult {
					select {
					case pc := <-inboxCh:
						queueCombine(pc)
						return rdvq.SelectInboxEmptied
					case <-waitCh:
						return rdvq.SelectWaitSignaled
					default:
						return rdvq.SelectAborted
					}
				},
			)
			return ok
		}

		combine := func(ctx context.Context, idleTimerCh <-chan time.Time, waiter rdvq.Waiter, changeCh <-chan struct{}) (bool, error) {

			// Check if any combiners need to be flushed due to deadlines and
			// set up flush deadline timer if needed
			var flushDeadlineTimerCh <-chan time.Time
			if timeUntilNextDeadline := flushToNextDeadline(); timeUntilNextDeadline > 0 {
				flushDeadlineTimer := timerp.Get()
				defer timerp.Put(flushDeadlineTimer)
				flushDeadlineTimer.Reset(timeUntilNextDeadline)
				flushDeadlineTimerCh = flushDeadlineTimer.C
			}

			if isSpare {
				// Spare goroutine processes excess work (outboxes + shared channel)
				// without registering for immediate delivery
				waiterNotified := false
				var err error

				pc, ok := cp.combineQueue.PopFrontExcessFunc(combineQueuePool,
					func(popWaitCh <-chan struct{}) rdvq.SelectResult {
						popResult := rdvq.SelectAborted
						waiter.Wait(func(waiterWaitCh <-chan struct{}) rdvq.SelectResult {
							waitStartTime := time.Now()
							cp.state.SpareWaitStarted(waitStartTime)
							defer cp.state.SpareWaitEnded(waitStartTime)
							select {
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
							case <-popWaitCh:
								popResult = rdvq.SelectWaitSignaled
							case <-waiterWaitCh:
								waiterNotified = true
								return rdvq.SelectWaitSignaled
							case <-changeCh:
							case <-doneCh:
								err = doneChErr
							case <-ctx.Done():
								err = ctx.Err()
							}
							return rdvq.SelectAborted
						})
						return popResult
					},
				)
				if ok {
					queueCombine(pc)
				}
				if err == errIdleTimeout && workQueue.Len() > 0 {
					// Don't exit if we still have work to do.
					err = nil
				}
				return waiterNotified, err
			}

			// Primary goroutine - registers for immediate delivery
			waiterNotified := false
			var err error
			cp.combineQueue.PopFrontFunc(combineQueuePool, queueCombine,
				func(inboxCh <-chan pendingCombine, popWaitCh <-chan struct{}) rdvq.SelectResult {
					popResult := rdvq.SelectAborted
					waiter.Wait(func(waiterWaitCh <-chan struct{}) rdvq.SelectResult {
						select {
						case pc := <-inboxCh:
							queueCombine(pc)
							popResult = rdvq.SelectInboxEmptied
						case <-nextJobFlushCh:
							queueWork(flushAll)
						case <-flushDeadlineTimerCh:
							// At least one combiner has reached its deadline
							flushToNextDeadline()
						case <-popWaitCh:
							popResult = rdvq.SelectWaitSignaled
						case <-waiterWaitCh:
							waiterNotified = true
							return rdvq.SelectWaitSignaled
						case <-changeCh:
						case <-doneCh:
							err = doneChErr
						case <-ctx.Done():
							err = ctx.Err()
						}
						return rdvq.SelectAborted
					})
					return popResult
				},
			)
			return waiterNotified, err
		}

		var processWorkAndCombine func(ctx context.Context, topLevel bool, waiter rdvq.Waiter, changeCh <-chan struct{}) (bool, error)

		bp := &combineBackpressureProvider{
			baseBackpressureProvider: baseBackpressureProvider{j: j},
			tryCombineFn: func(ctx context.Context) (bool, error) {
				return tryCombine(ctx), nil
			},
			combineFn: func(ctx context.Context, waiter rdvq.Waiter, changeCh <-chan struct{}) (bool, error) {
				return combine(ctx, nil, waiter, changeCh)
			},
			queueWorkFn: func(workFn func(context.Context) error) {
				// Queue scatters for deferred execution when capacity becomes available
				pendingScatters.PushBack(workFn)
			},
		}

		backpressureCtx = withBackpressureProvider(goroutineCtx, bp)

		// Create the dedicated outbox for this combiner goroutine to emit gathers to the job
		emitGatherOutbox := OutboxFor[boundGather](bp.OutboxMap(), j.gatherOutboxKey())

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

		executeCombineFn = func(ctx context.Context, combineFn boundCombine) {
			if nextJobFlushCh == nil {
				// Make sure the job won't terminate before the combiner is flushed
				nextJobFlushCh, unregisterAsJobFlusher = j.state.RegisterFlusher()
			}
			combineFn(ctx, &cm, queueWork, emitGatherOutbox)
			cp.state.IncrementCompleted()
		}

		cp.state.GoroutineStarted()
		defer cp.state.GoroutineExited()

		var idleTimer *time.Timer

		processPendingScatters := func(ctx context.Context, topLevel bool) {
			if !topLevel {
				return
			}

			for {
				scatterFn, ok := pendingScatters.PopFront()
				if !ok {
					break
				}
				if err := scatterFn(ctx); err != nil {
					// Analysis: All pending scatter functions originate from scatters called within
					// combiner execution context. Even if it's a gather.Scatter() call, it uses the
					// combiner's backpressure provider (not the job's default one).
					//
					// The combiner backpressure provider only returns errors from:
					// - bp.Yield() -> tryCombine() -> always returns (bool, nil)
					// - bp.Block() -> processWorkAndCombine() -> can return context/job cancellation errors
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

		processWork := func(ctx context.Context, topLevel bool) int64 {
			// Execute pending scatters before processing work
			processPendingScatters(ctx, topLevel)

			lastIDToProcess := workCounter
			workItemsProcessed := 0
			lastIDProcessed := workCounter
			for {
				work, ok := workQueue.PopFront()
				if !ok {
					break
				}
				workItemsProcessed++
				work.workFn(ctx)
				lastIDProcessed = work.id

				// Execute pending scatters before continuing to process work
				processPendingScatters(ctx, topLevel)

				if work.id >= lastIDToProcess {
					break
				}
			}
			return lastIDProcessed
		}

		processWorkAndCombine = func(ctx context.Context, topLevel bool, waiter rdvq.Waiter, changeCh <-chan struct{}) (bool, error) {

			lastIDProcessed := processWork(ctx, topLevel)

			var idleTimerCh <-chan time.Time
			if topLevel && isSpare {
				// Capture the current idle timeout value to ensure consistency
				idleTimeout := cp.state.IdleTimeout()
				if idleTimeout >= 0 {
					// This is the goroutine that has elected itself to read only
					// the spare channel. This will keep this goroutine idle
					// unless it's really needed, thus allowing the idle timeout to
					// elapse (if enabled).
					idleTimer.Reset(idleTimeout)
					idleTimerCh = idleTimer.C
				}
			}

			// Add any pending flushes to the work queue before deciding whether
			// we need to block.
			flushToNextDeadline()

			// No need to report errors from combine, since they would only
			// be due to canceled contexts. Other errors are posted to be
			// gathered.
			var ok bool
			var err error
			if workCounter > lastIDProcessed {
				// There's still work in the queue, use tryCombine to avoid blocking
				ok = tryCombine(ctx)
			} else {
				ok, err = combine(ctx, idleTimerCh, waiter, changeCh)
			}
			return ok, err
		}

		if combineFn != nil {
			executeCombineFn(backpressureCtx, combineFn)
		}

		for {
			if !isSpare {
				if cp.spareElected.CompareAndSwap(false, true) {
					isSpare = true
					idleTimer = timerp.Get()
					defer timerp.Put(idleTimer)
					defer cp.spareElected.Store(false)
				}
			}

			_, err := processWorkAndCombine(backpressureCtx, true, rdvq.Waiter{}, nil)

			if err != nil {
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

		// Flush combiners as needed when this goroutine terminates.
		flushAll(backpressureCtx)

		// Process any work items that were created during the flush operation
		processWork(backpressureCtx, true)
	}()
}

const errIdleTimeout = cerr.Error("idle timeout reached")

type combineWork func(ctx context.Context)
type boundCombine func(ctx context.Context, cm *combinerMap, queueWork func(combineWork) int64, gatherOutbox *rdvq.Outbox[boundGather])

type pendingCombine struct {
	fn             boundCombine
	releaseWaiters bool
}

type halfBoundCombine[I any] func(ctx context.Context, input I, inputErr error)

type boundCombiner struct {
	CombineFn     any
	FlushFn       func(ctx context.Context)
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

func getCombineFunc[I, O any](ctx context.Context, cm *combinerMap, cp *CombinerPool, c *CombineOp[I, O], queueWork func(combineWork) int64, emitGatherOutbox *rdvq.Outbox[boundGather]) halfBoundCombine[I] {
	j := cp.j
	k := combinerMapKey{
		Job:     j,
		Combine: c,
	}
	bc := cm.m[k]
	var combineFn halfBoundCombine[I]
	if bc != nil {
		combineFn = bc.CombineFn.(halfBoundCombine[I])
	} else {
		emit := func(ctx context.Context, output O, outputErr error) {
			j.state.IncrementWork()

			// Bind the gatherFn to the combiner output
			gatherFn := func(ctx context.Context) error {
				defer func() {
					j.state.DecrementWork()
				}()
				return c.gatherOp.gatherFn(ctx, output, outputErr)
			}

			// Queue the gather as work for this combiner goroutine to avoid blocking
			queueWork(func(ctx context.Context) {
				j.postGather(ctx, emitGatherOutbox, gatherFn)
			})
		}

		combiner := func() Combiner[I, O] {
			panicked := true
			defer func() {
				if panicked {
					emit(ctx, *new(O), ErrCombinerFactoryPanicked)
				}
			}()
			psgfnCombiner := c.newCombiner()
			panicked = false
			if psgfnCombiner.CombineFn == nil && psgfnCombiner.FlushFn == nil {
				emit(ctx, *new(O), ErrCombinerFactoryReturnedNil)
				return &errCombiner[I, O]{err: ErrCombinerFactoryReturnedNil}
			}
			return psgfnCombiner
		}()

		// Initialize the map if needed
		if cm.m == nil {
			cm.m = make(map[combinerMapKey]*boundCombiner)
		}

		// Create the boundCombiner first
		bc = &boundCombiner{}

		// Define the combineFn with access to bc
		combineFn = func(ctx context.Context, input I, inputErr error) {
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
			}()

			combiner.Combine(ctx, input, inputErr, emit)
			didNotPanic = true
		}

		// Store the combineFn in the boundCombiner
		bc.CombineFn = combineFn

		// Define the FlushFn with access to bc
		bc.FlushFn = func(ctx context.Context) {
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
	return combineFn
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
		bc.FlushFn(ctx)
	}
	cm.m = nil
	cm.deadlines = heap.Heap[*boundCombiner]{} // Reset to zero value
}
