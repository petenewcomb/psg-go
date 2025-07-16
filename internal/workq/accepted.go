// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"errors"
	"sync"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Queue manages work items with single-item processing logic using a two-queue
// priority system. It implements the priority-based backpressure algorithm that
// prioritizes newly accepted work over deferred work over new work, maintaining
// liveness through single-item processing and non-blocking retry logic.
//
// The two-queue design prevents starvation: newly accepted work gets first
// priority, work that has failed once goes to the deferred queue for lower
// priority retry, and new work is only processed if no accepted work succeeds.
type Accepted struct {
	fresh    nbcq.Queue[Work]
	deferred nbcq.Queue[Work]
	waiters  rdvq.Waiters
	monitor  Monitor
}

// Init initializes the work queue using the global pool.
//
//nolint:contextcheck // background context used only for tracing
func (q *Accepted) Init() {
	traceRegion := "workq.Accepted.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion,
		"Accepted=%p, fresh=%p, deferred=%p, waiters=%p, monitor=%p",
		q, &q.fresh, &q.deferred, &q.waiters, &q.monitor)

	q.fresh.Init(workPool)
	q.deferred.Init(workPool)
	q.waiters.Init()
	q.monitor.Notify = q.waiters.Notify
}

// AddWorkFunc provides new work to the queue processor. It is called with a
// waitCh that signals when there is deferred work ready to process. If waitCh
// is nil, AddWorkFunc should not block. A queueFn is provided that should be
// called for each work item accepted. Returns whether the waitCh was signaled
// or not.
type AddWorkFunc func(ctx context.Context, waitCh <-chan RenotifyFunc, queueFn QueueWorkFunc) (RenotifyFunc, error)

type RenotifyFunc = rdvq.RenotifyFunc

// QueueWorkFunc is called by AddWorkFunc to add new work items to processing.
type QueueWorkFunc func(Work)

// Signals the end of work
const ErrEndOfWork = cerr.Error("end of work")

// ExecuteOne processes exactly one work item using priority-based processing.
// It tries newly accepted work first (exhausting the queue), then deferred
// work (exhausting that queue), then new work via addWorkFn, all as non-blocking
// operations. If no immediately executable work is found, waits for new work
// or notification that a deferred work item is ready.
//
// Priority order: fresh → deferred → new work
//
// Returns the error value from the work item if one was executed, the error
// value from addWorkFn if called, or [ErrEndOfWork] if addWorkFn would have
// been called but was nil.
func (q *Accepted) ExecuteOne(ctx context.Context, addWorkFn AddWorkFunc) error {
	traceRegion := "workq.Accepted.ExecuteOne"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Accepted=%p", q)

	c := controller{
		q:         q,
		addWorkFn: addWorkFn,
	}
	defer c.Close()

	for {
		workExecuted, err := c.ExecuteOne(ctx)
		if workExecuted || err != nil {
			return err
		}
		c.Reset()
	}
}

// TryAddWorkFunc provides new work for non-blocking execution attempts.
// It should call queueFn for each available work item.
type TryAddWorkFunc func(context.Context, QueueWorkFunc) error

// TryExecuteOne attempts to process exactly one work item using priority-based processing.
// It tries newly accepted work first (exhausting the queue), then deferred work
// (exhausting that queue), then new work via addWorkFn, all as non-blocking operations.
// If no immediately executable work is found, returns false without blocking.
//
// Priority order: fresh → deferred → new work
//
// Returns true if a work item was executed, false if no work was ready to execute.
//
// Returns the error value from the work item if one was executed, the error
// value from addWorkFn if called, or [ErrEndOfWork] if addWorkFn would have
// been called but was nil.
func (q *Accepted) TryExecuteOne(ctx context.Context, addWorkFn TryAddWorkFunc) (bool, error) {
	c := controller{
		q:            q,
		tryAddWorkFn: addWorkFn,
	}
	defer c.Close()

	// Try accepted work first
	if err := c.TryAccepted(ctx, false); c.workWasExecuted || err != nil {
		return c.workWasExecuted, err
	}

	if addWorkFn == nil {
		if c.workWasDeferred {
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

	c.workWasDeferred = false
	err := c.TryAccepted(ctx, false)
	if !c.workWasDeferred && err == nil {
		err = addErr
	}
	return c.workWasExecuted, err
}

type controller struct {
	q                       *Accepted
	buffer                  *[]bufferedWork
	currentIndex            int
	currentWasDeferred      bool
	othersReleased          bool
	addWorkFn               AddWorkFunc
	tryAddWorkFn            TryAddWorkFunc
	renotifyFn              RenotifyFunc
	workWasExecuted         bool
	deferredWorkWasExecuted bool
	workWasDeferred         bool
	endOfWorkErr            error
}

func (c *controller) ExecuteOne(ctx context.Context) (bool, error) {

	var err error
	if err := c.TryAccepted(ctx, false); c.workWasExecuted || err != nil {
		return c.workWasExecuted, err
	}

	if !c.workWasDeferred && c.endOfWorkErr != nil {
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
	if c.workWasExecuted {
		return true, err
	}
	if err != nil && errors.Is(err, ErrEndOfWork) {
		c.endOfWorkErr = err
		return false, nil
	}
	return false, err
}

// TryAccepted attempts to execute work from both accepted queues.
// First exhausts newly accepted work, then tries deferred work.
func (c *controller) TryAccepted(ctx context.Context, blockOrSubscribe bool) error {
	traceRegion := "workq.controller.TryAccepted"
	defer trace.StartRegion(ctx, traceRegion).End()
	if err := c.tryAccepted(ctx, &c.q.fresh, blockOrSubscribe); c.workWasExecuted || err != nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.workWasExecuted, err)
		return err
	}
	err := c.tryAccepted(ctx, &c.q.deferred, blockOrSubscribe)
	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.workWasExecuted, err)
	return err
}

func (c *controller) tryAccepted(ctx context.Context, q *nbcq.Queue[Work], blockOrSubscribe bool) error {
	for c.collectAccepted(q) {
		if err := c.execute(ctx, blockOrSubscribe); c.workWasExecuted || err != nil {
			return err
		}
	}
	return nil
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) queueFresh(work Work) {
	traceRegion := "workq.Accepted.queueFresh"
	trace.Logf(context.Background(), traceRegion, "Accepted(%p) adding fresh %v", c.q, work)
	c.q.fresh.PushBack(workPool, work)
}

func (c *controller) TryAddNew(ctx context.Context) (bool, error) {
	traceRegion := "workq.controller.TryAddNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.tryAddWorkFn == nil && c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workAdded=false err=ErrEndOfWork")
		return false, ErrEndOfWork
	}
	workAdded := false
	queueFn := func(work Work) {
		c.queueFresh(work)
		workAdded = true
	}

	var err error
	if c.tryAddWorkFn != nil {
		err = c.tryAddWorkFn(ctx, queueFn)
	} else {
		_, err = c.addWorkFn(ctx, nil, queueFn)
	}
	trace.Logf(ctx, traceRegion, "returning workAdded=%v err=%v", workAdded, err)
	return workAdded, err
}

func (c *controller) WaitForNew(ctx context.Context) error {
	traceRegion := "workq.controller.WaitForNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=false err=ErrEndOfWork")
		return ErrEndOfWork
	}
	var err error
	confirmFn := func() bool {
		err = c.retryExecutionBeforeWait(ctx)
		return !c.workWasExecuted
	}
	waiter := c.q.waiters.New(confirmFn)
	waiter.WaitFunc(func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		c.renotifyFn, err = c.addWorkFn(ctx, waitCh, c.queueFresh)
		return c.renotifyFn
	})
	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", c.workWasExecuted, err)
	return err
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) collectAccepted(q *nbcq.Queue[Work]) bool {
	c.currentIndex = c.bufferLen()
	work, ok := q.PopFront(workPool)
	if !ok {
		return false
	}
	c.addToBuffer(work, q == &c.q.deferred)
	return true
}

func (c *controller) execute(ctx context.Context, blockOrSubscribe bool) error {
	traceRegion := "workq.Accepted.execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	bw := (*c.buffer)[c.currentIndex]
	c.currentWasDeferred = bw.wasDeferred

	if bw.wasDeferred {
		trace.Logf(ctx, traceRegion, "executing deferred %v at buffer index %d", bw.work, c.currentIndex)
	} else {
		trace.Logf(ctx, traceRegion, "executing fresh %v at buffer index %d", bw.work, c.currentIndex)
	}

	ex := Execution{
		Blocking: c.releaseOthers,
		Starting: c.starting,
		Queue:    c.queueFresh,
	}

	if blockOrSubscribe {
		ex.Subscribe = c.q.monitor.Subscribe
	}

	if c.workWasExecuted {
		panic("workWasExecuted should not be set before execution")
	}
	defer func() {
		if c.workWasExecuted {
			bw.work.Close()
		} else {
			c.workWasDeferred = true
		}
	}()

	err := bw.work.Execute(ctx, ex)

	if err != nil {
		trace.Logf(ctx, traceRegion, "%v returned err=%v", bw.work, err)
	}

	if errors.Is(err, ErrEndOfWork) {
		panic("ErrEndOfWork received from work function")
	}

	return err
}

func (c *controller) Reset() {
	c.requeueBuffer()
	if c.workWasExecuted {
		panic("Reset called after work was executed")
	}
	c.currentWasDeferred = false
	c.othersReleased = false
	c.workWasDeferred = false
}

func (c *controller) starting() {
	traceRegion := "workq.controller.starting"
	c.workWasExecuted = true
	if c.currentWasDeferred {
		// Invalidate any saved renotifyFn because we have productively used it.
		// This must be done before the call to releaseOthers, as it will
		// ultimately call renotifyFn if set.
		c.renotifyFn = nil
		trace.Logf(context.Background(), traceRegion, "deferred work at index %d started", c.currentIndex)
	} else {
		trace.Logf(context.Background(), traceRegion, "fresh work at index %d started", c.currentIndex)
	}
	c.releaseOthers()
}

func (c *controller) releaseOthers() {
	if !c.othersReleased {
		c.othersReleased = true
		// Make sure we don't requeue the executing item
		(*c.buffer)[c.currentIndex].work = nil
		// Requeue the remaining work items before actually
		// executing the work function
		c.requeueBuffer()
	}
}

// retryExecutionBeforeWait handles the race condition where work might arrive
// between our last attempt and registering as a waiter. It first checks both
// accepted queues for any new work, then retries deferred work items with
// the notification function to register for later wake-up.
func (c *controller) retryExecutionBeforeWait(ctx context.Context) error {
	// Walk through the deferred work items to retry (i.e., verify that the wait
	// is still needed) and pass the notification function to them.
	if c.buffer != nil {
		// Iterate through the buffer and retry each work item while giving each
		// a chance to register for notifications
		for c.currentIndex = range *c.buffer {
			err := c.execute(ctx, true)
			if c.workWasExecuted || err != nil {
				return err
			}
		}
	}

	// Check to make sure nothing else accumulated before we registered as a
	// waiter.
	if err := c.TryAccepted(ctx, true); c.workWasExecuted || err != nil {
		return err
	}

	// Requeue the remaining work items before waiting.
	c.requeueBuffer()
	return nil
}

// requeueBuffer moves all non-executed work items from the temp buffer
// to the deferred queue, then returns the buffer to the pool.
//
//nolint:contextcheck // background context used only for tracing
func (c *controller) requeueBuffer() {
	traceRegion := "workq.controller.requeueBuffer"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	if c.buffer != nil {
		for i := range *c.buffer {
			work := (*c.buffer)[i].work
			if work != nil {
				(*c.buffer)[i].work = nil
				trace.Logf(context.Background(), traceRegion, "pushing %v at index %d to deferred queue", work, i)
				c.q.deferred.PushBack(workPool, work)
			}
		}
		*c.buffer = (*c.buffer)[:0]
		bufferPool.Put(c.buffer)
		c.buffer = nil
		c.currentIndex = 0
	}

	// Renotify after requeueing to avoid race in which newly ready items are
	// not yet available in the deferred queue
	renotifyFn := c.renotifyFn
	if renotifyFn != nil && !c.deferredWorkWasExecuted {
		c.renotifyFn = nil
		renotifyFn()
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) addToBuffer(work Work, wasDeferred bool) {
	traceRegion := "workq.controller.addToBuffer"
	if c.buffer == nil {
		c.buffer = bufferPool.Get().(*[]bufferedWork)
	}
	index := len(*c.buffer)
	*c.buffer = append(*c.buffer, bufferedWork{
		work:        work,
		wasDeferred: wasDeferred,
	})
	trace.Logf(context.Background(), traceRegion, "added %v at index %d, wasDeferred=%v", work, index, wasDeferred)
}

func (c *controller) bufferLen() int {
	if c.buffer == nil {
		return 0
	}
	return len(*c.buffer)
}

func (c *controller) Close() {
	c.requeueBuffer()
}

type bufferedWork struct {
	work        Work
	wasDeferred bool
}

var workPool = &nbcq.Pool[Work]{}

var bufferPool = sync.Pool{
	New: func() any {
		return &[]bufferedWork{}
	},
}
