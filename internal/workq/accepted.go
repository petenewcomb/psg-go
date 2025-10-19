// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"errors"
	"slices"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Queue manages work items with single-item processing logic using a two-queue
// priority system. It implements the priority-based backpressure algorithm that
// prioritizes newly accepted work over postponed work over new work, maintaining
// liveness through single-item processing and non-blocking retry logic.
//
// The two-queue design prevents starvation: newly accepted work gets first
// priority, work that has failed once goes to the postponed queue for lower
// priority retry, and new work is only processed if no accepted work succeeds.
type Accepted struct {
	fresh     nbcq.Queue[Work]
	postponed nbcq.Queue[Work]
	waiters   rdvq.Waiters
	listener  rdvq.Listener
}

// Init initializes the work queue using the global pool.
//
//nolint:contextcheck // background context used only for tracing
func (q *Accepted) Init() {
	traceRegion := "workq.Accepted.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Accepted=%p, fresh=%p, postponed=%p, waiters=%p, listener=%p",
		q, &q.fresh, &q.postponed, &q.waiters, &q.listener)

	q.fresh.Init()
	q.postponed.Init()
	q.waiters.Init()
	q.listener.Notify = q.waiters.Notify
}

// AddWorkFunc provides new work to the queue processor. It is called with a
// waitCh that signals when there is postponed work ready to process. If waitCh
// is nil, AddWorkFunc should not block. A queueFn is provided that should be
// called for each work item accepted. Returns whether the waitCh was signaled
// or not.
type AddWorkFunc func(
	ctx context.Context,
	queueFn QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
) (RenotifyFunc, error)

type RenotifyFunc = rdvq.RenotifyFunc

// QueueWorkFunc is called by AddWorkFunc to add new work items to processing.
type QueueWorkFunc func(Work)

// Signals the end of work
const ErrEndOfWork = cerr.Error("end of work")

func (q *Accepted) ExecuteNowOrQueue(
	ctx context.Context,
	ex Execution,
	work Work,
) error {
	traceRegion := "workq.Accepted.ExecuteNowOrQueue"
	defer trace.StartRegion(ctx, traceRegion).End()

	err := work.Execute(ctx, ex)
	if err != nil || ex.Started() {
		work.Free()
	} else {
		q.postponed.PushBack(work)
	}
	return err
}

// ExecuteOne processes exactly one work item using priority-based processing.
// It tries newly accepted work first (exhausting the queue), then postponed
// work (exhausting that queue), then new work via addWorkFn, all as non-blocking
// operations. If no immediately executable work is found, waits for new work
// or notification that a postponed work item is ready.
//
// Priority order: fresh → postponed → new work
//
// The unmetDemandFn is called when excess fresh work accumulates (count > 1) and
// no idle workers are available. This enables spawning new workers when needed.
// Pass nil if worker spawning is not applicable for this queue.
//
// Returns the error value from the work item if one was executed, the error
// value from addWorkFn if called, or [ErrEndOfWork] if addWorkFn would have
// been called but was nil.
func (q *Accepted) ExecuteOne(ctx context.Context, addWorkFn AddWorkFunc, unmetDemandFn RenotifyFunc) error {
	traceRegion := "workq.Accepted.ExecuteOne"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Accepted=%p", q)

	err := ctx.Err()
	if err != nil {
		return err
	}

	c := newController(q)
	c.addWorkFn = addWorkFn
	c.unmetDemandFn = unmetDemandFn
	defer c.Free()

	for {
		workExecuted, err := c.ExecuteOne(ctx)
		if workExecuted || err != nil {
			return err
		}

		err = ctx.Err()
		if err != nil {
			return err
		}

		c.ResetForRetry()
	}
}

// TryAddWorkFunc provides new work for non-blocking execution attempts.
// It should call queueFn for each available work item.
type TryAddWorkFunc func(context.Context, QueueWorkFunc) error

// TryExecuteOne attempts to process exactly one work item using priority-based processing.
// It tries newly accepted work first (exhausting the queue), then postponed work
// (exhausting that queue), then new work via addWorkFn, all as non-blocking operations.
// If no immediately executable work is found, returns false without blocking.
//
// Priority order: fresh → postponed → new work
//
// Returns true if a work item was executed, false if no work was ready to execute.
//
// Returns the error value from the work item if one was executed, the error
// value from addWorkFn if called, or [ErrEndOfWork] if addWorkFn would have
// been called but was nil.
func (q *Accepted) TryExecuteOne(ctx context.Context, addWorkFn TryAddWorkFunc) (bool, error) {
	err := ctx.Err()
	if err != nil {
		return false, err
	}

	c := newController(q)
	c.tryAddWorkFn = addWorkFn
	defer c.Free()

	// Try accepted work first
	if err := c.TryAccepted(ctx, false); c.ex.Started() || err != nil {
		return c.ex.Started(), err
	}

	if addWorkFn == nil {
		if c.workWasPostponed {
			return false, nil
		} else {
			return false, ErrEndOfWork
		}
	}

	// Requeue immediately because there's no need to hold buffered items to
	// recheck before waiting.
	c.requeueBuffer()

	// Try adding new work
	workAdded, addErr := c.TryAddNew(ctx)
	if !workAdded || (addErr != nil && !errors.Is(addErr, ErrEndOfWork)) {
		return false, addErr
	}

	c.workWasPostponed = false
	err = c.TryAccepted(ctx, false)
	if !c.workWasPostponed && err == nil {
		err = addErr
	}
	return c.ex.Started(), err
}

type controller struct {
	q                        *Accepted
	executor                 Executor
	buffer                   []bufferedWork
	currentIndex             int
	currentWasPostponed      bool
	othersReleased           bool
	addWorkFn                AddWorkFunc
	tryAddWorkFn             TryAddWorkFunc
	renotifyFn               RenotifyFunc
	unmetDemandFn            RenotifyFunc
	workAddedCount           int
	postponedWorkWasExecuted bool
	workWasPostponed         bool
	endOfWorkErr             error

	ex Execution // avoid closure reallocations

	queueFreshFn QueueWorkFunc // avoid closure reallocations

	shouldStillWaitFn  func() bool     // avoid closure reallocations
	shouldStillWaitCtx context.Context //nolint:containedctx // temporary to avoid closure allocation
	shouldStillWaitErr error
}

// Init implements omnipool.Initer to set up self-referential closures
func (c *controller) Init() {
	// Allocate reusable self-referential closures
	c.ex = c.executor.BaseEx()
	c.ex.Blocking = c.blocking
	c.ex.Starting = c.starting
	c.ex.AddToListeners = c.addToListeners
	c.queueFreshFn = c.queueFresh
	c.shouldStillWaitFn = c.shouldStillWait
}

// Reset implements omnipool.Resetter to clear state while preserving allocations
func (c *controller) Reset() {
	c.executor.Reset()

	// Reset internal state before returning to pool
	c.ResetForRetry()

	// Clear all but reusable allocations
	*c = controller{
		buffer:            c.buffer[:0],
		ex:                c.ex,
		queueFreshFn:      c.queueFreshFn,
		shouldStillWaitFn: c.shouldStillWaitFn,
	}
}

var controllerPool = omnipool.For[controller]()

func newController(q *Accepted) *controller {
	c := controllerPool.Get()
	c.q = q
	return c
}

func (c *controller) ExecuteOne(ctx context.Context) (bool, error) {

	var err error
	if err := c.TryAccepted(ctx, false); c.ex.Started() || err != nil {
		return c.ex.Started(), err
	}

	if !c.workWasPostponed && c.endOfWorkErr != nil {
		return false, c.endOfWorkErr
	}

	// Avoid full blocking protocol if we can
	workAdded, err := c.TryAddNew(ctx)
	if err != nil {
		if errors.Is(err, ErrEndOfWork) {
			c.endOfWorkErr = err
			return false, nil
		}
		return false, err
	}
	if workAdded {
		return false, nil
	}

	// Block and wait for work to become available
	err = c.WaitForNew(ctx)
	if c.ex.Started() {
		return true, err
	}
	if err != nil && errors.Is(err, ErrEndOfWork) {
		c.endOfWorkErr = err
		return false, nil
	}
	return false, err
}

// TryAccepted attempts to execute work from both accepted queues.
// First exhausts newly accepted work, then tries postponed work.
func (c *controller) TryAccepted(ctx context.Context, blockOrListen bool) error {
	traceRegion := "workq.controller.TryAccepted"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "blockOrListen=%v", blockOrListen)
	if err := c.tryAccepted(ctx, &c.q.fresh, blockOrListen); c.ex.Started() || err != nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.ex.Started(), err)
		return err
	}
	err := c.tryAccepted(ctx, &c.q.postponed, blockOrListen)
	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.ex.Started(), err)
	return err
}

func (c *controller) tryAccepted(ctx context.Context, q *nbcq.Queue[Work], blockOrListen bool) error {
	for c.collectAccepted(q) {
		if err := c.execute(ctx, blockOrListen); c.ex.Started() || err != nil {
			return err
		}
	}
	return nil
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) queueFresh(work Work) {
	traceRegion := "workq.Accepted.queueFresh"
	trace.Logf(context.Background(), traceRegion, "Accepted(%p) adding fresh %v", c.q, work)
	c.q.fresh.PushBack(work)
	c.workAddedCount++
	if c.workAddedCount > 1 && c.unmetDemandFn != nil {
		c.q.waiters.Notify(c.unmetDemandFn)
	}
}

func (c *controller) TryAddNew(ctx context.Context) (bool, error) {
	traceRegion := "workq.controller.TryAddNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.tryAddWorkFn == nil && c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workAdded=false err=ErrEndOfWork")
		return false, ErrEndOfWork
	}

	c.workAddedCount = 0
	defer func() {
		c.workAddedCount = 0
	}()

	var err error
	if c.tryAddWorkFn != nil {
		err = c.tryAddWorkFn(ctx, c.queueFreshFn)
	} else {
		_, err = c.addWorkFn(ctx, c.queueFreshFn, nil, nil)
	}
	workWasAdded := c.workAddedCount > 0
	trace.Logf(ctx, traceRegion, "returning workAdded=%v err=%v", workWasAdded, err)
	return workWasAdded, err
}

func (c *controller) WaitForNew(ctx context.Context) error {
	traceRegion := "workq.controller.WaitForNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=false err=ErrEndOfWork")
		return ErrEndOfWork
	}

	c.shouldStillWaitCtx = ctx
	c.shouldStillWaitErr = nil
	defer func() {
		c.shouldStillWaitCtx = nil
		c.shouldStillWaitErr = nil
	}()

	var err error
	c.renotifyFn, err = c.addWorkFn(ctx, c.queueFreshFn, &c.q.waiters, c.shouldStillWaitFn)
	if err == nil {
		err = c.shouldStillWaitErr
	} else if c.shouldStillWaitErr != nil {
		err = errors.Join(c.shouldStillWaitErr, err)
	}

	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.ex.Started(), err)
	return err
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) collectAccepted(q *nbcq.Queue[Work]) bool {
	c.currentIndex = len(c.buffer)
	work, ok := q.TryPopFront()
	if !ok {
		return false
	}
	c.addToBuffer(work, q == &c.q.postponed)
	return true
}

func (c *controller) execute(ctx context.Context, blockOrListen bool) error {
	traceRegion := "workq.Accepted.execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	bw := c.buffer[c.currentIndex]
	c.currentWasPostponed = bw.wasPostponed

	if trace.IsEnabled() {
		if bw.wasPostponed {
			trace.Logf(ctx, traceRegion, "executing postponed %v at buffer index %d", bw.work, c.currentIndex)
		} else {
			trace.Logf(ctx, traceRegion, "executing fresh %v at buffer index %d", bw.work, c.currentIndex)
		}
	}

	ex := c.ex

	if !blockOrListen {
		ex.AddToListeners = nil
	}

	if c.ex.Started() {
		panic("started should not be set before execution")
	}

	err := bw.work.Execute(ctx, ex)

	if err != nil {
		trace.Logf(ctx, traceRegion, "%v returned err=%v", bw.work, err)
	}

	if c.ex.Started() {
		bw.work.Free()
	} else {
		c.workWasPostponed = true
	}

	if errors.Is(err, ErrEndOfWork) {
		panic("ErrEndOfWork received from work function")
	}

	return err
}

func (c *controller) addToListeners(listeners *Listeners) {
	c.q.listener.AddTo(listeners)
}

func (c *controller) ResetForRetry() {
	c.requeueBuffer()
	if c.ex.Started() {
		panic("Reset called after work was executed")
	}
	c.workAddedCount = 0
	c.currentWasPostponed = false
	c.othersReleased = false
	c.workWasPostponed = false
}

func (c *controller) blocking() {
	traceRegion := "workq.controller.blocking"
	if c.currentWasPostponed {
		trace.Logf(context.Background(), traceRegion, "postponed work at index %d blocking", c.currentIndex)
	} else {
		trace.Logf(context.Background(), traceRegion, "fresh work at index %d blocking", c.currentIndex)
	}
	c.releaseOthers()
}

func (c *controller) starting() {
	traceRegion := "workq.controller.starting"
	if c.currentWasPostponed {
		// Invalidate any saved renotifyFn because we have productively used it.
		// This must be done before the call to releaseOthers, as it will
		// ultimately call renotifyFn if set.
		c.renotifyFn = nil
		trace.Logf(context.Background(), traceRegion, "postponed work at index %d started", c.currentIndex)
	} else {
		trace.Logf(context.Background(), traceRegion, "fresh work at index %d started", c.currentIndex)
	}
	c.executor.Starting()
	c.releaseOthers()
}

func (c *controller) releaseOthers() {
	if !c.othersReleased {
		c.othersReleased = true
		// Make sure we don't requeue the executing item
		c.buffer[c.currentIndex].work = nil
		// Requeue the remaining work items before actually
		// executing the work function
		c.requeueBuffer()
	}
}

// shouldStillWait handles the race condition where work might arrive
// between our last attempt and registering as a waiter. It first checks both
// accepted queues for any new work, then retries postponed work items with
// the notification function to register for later wake-up.
func (c *controller) shouldStillWait() bool {
	// Walk through the postponed work items to retry (i.e., verify that the wait
	// is still needed) and pass the notification function to them.
	if c.buffer != nil {
		// Iterate through the buffer and retry each work item while giving each
		// a chance to register for notifications
		for c.currentIndex = range c.buffer {
			c.shouldStillWaitErr = c.execute(c.shouldStillWaitCtx, true)
			if c.ex.Started() || c.shouldStillWaitErr != nil {
				return false
			}
		}
	}

	// Check to make sure nothing else accumulated before we registered as a
	// waiter.
	if c.shouldStillWaitErr = c.TryAccepted(c.shouldStillWaitCtx, true); c.ex.Started() || c.shouldStillWaitErr != nil {
		return false
	}

	// Requeue the remaining work items before waiting.
	c.requeueBuffer()
	return true
}

// requeueBuffer moves all non-executed work items from the temp buffer
// to the postponed queue, then returns the buffer to the pool.
//
//nolint:contextcheck // background context used only for tracing
func (c *controller) requeueBuffer() {
	traceRegion := "workq.controller.requeueBuffer"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	// Minimize latencies, especially tail latencies, by ensuring that collected
	// work items are sorted by ascending group and work IDs before requeuing.
	// This prioritizes older work groups and items over newer ones, preventing
	// individual items from being starved by shuffling.
	slices.SortFunc(c.buffer, func(a, b bufferedWork) int {
		switch {
		case a.work == nil && b.work == nil:
			return 0
		case a.work == nil:
			return 1
		case b.work == nil:
			return -1
		case a.work.Group() < b.work.Group():
			return -1
		case a.work.Group() > b.work.Group():
			return 1
		case a.work.ID() < b.work.ID():
			return -1
		case a.work.ID() > b.work.ID():
			return 1
		default:
			panic("unexpected equal IDs in requeueBuffer")
		}
	})
	for i := range c.buffer {
		bw := &c.buffer[i]
		work := bw.work
		if work == nil {
			// All nil from here on out
			break
		}
		bw.work = nil
		trace.Logf(context.Background(), traceRegion, "pushing %v to postponed queue", work)
		c.q.postponed.PushBack(work)
	}
	c.buffer = c.buffer[:0]
	c.currentIndex = 0

	// Renotify after requeueing to avoid race in which newly ready items are
	// not yet available in the postponed queue
	renotifyFn := c.renotifyFn
	if renotifyFn != nil && !c.postponedWorkWasExecuted {
		c.renotifyFn = nil
		renotifyFn()
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) addToBuffer(work Work, wasPostponed bool) {
	traceRegion := "workq.controller.addToBuffer"
	c.buffer = append(c.buffer, bufferedWork{
		work:         work,
		wasPostponed: wasPostponed,
	})
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"added %v at index %d, wasPostponed=%v", work, len(c.buffer)-1, wasPostponed)
	}
}

func (c *controller) Free() {
	controllerPool.Put(c)
}

type bufferedWork struct {
	work         Work
	wasPostponed bool
}
