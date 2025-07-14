// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

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
	fresh    nbcq.Queue[WorkFunc]
	deferred nbcq.Queue[WorkFunc]
	waiters  rdvq.Waiters
	monitor  Monitor
}

// Init initializes the work queue using the global pool.
//
//nolint:contextcheck // background context used only for tracing
func (q *Accepted) Init() {
	traceRegion := "workq.Accepted.Init"

	q.fresh.Init(workPool)
	q.deferred.Init(workPool)
	q.waiters.Init()
	q.monitor.Notify = q.waiters.Notify

	trace.Logf(context.Background(), traceRegion,
		"Accepted=%p, fresh=%p, deferred=%p, waiters=%p, monitor=%p",
		q, &q.fresh, &q.deferred, &q.waiters, &q.monitor)
}

// AddWorkFunc provides new work to the queue processor. It is called with a
// waitCh that signals when there is deferred work ready to process. If waitCh
// is nil, AddWorkFunc should not block. A queueFn is provided that should be
// called for each work item accepted. Returns whether the waitCh was signaled
// or not.
type AddWorkFunc func(ctx context.Context, waitCh <-chan RenotifyFunc, queueFn QueueWorkFunc) (RenotifyFunc, error)

type RenotifyFunc = rdvq.RenotifyFunc

// QueueWorkFunc is called by AddWorkFunc to add new work items to processing.
type QueueWorkFunc func(WorkFunc)

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

	// Used to propagate a renotifyFn collected from the wait at the end of the
	// loop to the controller created at the beginning
	var renotifyFn RenotifyFunc
	defer func() {
		if renotifyFn != nil {
			renotifyFn()
		}
	}()

	var endOfWorkErr error
	for {
		workExecuted, err := func() (bool, error) {
			c := controller{
				q:          q,
				addWorkFn:  addWorkFn,
				renotifyFn: renotifyFn,
			}
			renotifyFn = nil
			defer func() {
				renotifyFn = c.renotifyFn
				c.renotifyFn = nil
				c.Close()
			}()

			var err error
			if workExecuted, err := c.TryAccepted(ctx, false); workExecuted || err != nil {
				return workExecuted, err
			}

			if !c.workWasDeferred && endOfWorkErr != nil {
				return false, endOfWorkErr
			}

			// Avoid full blocking protocol if we can
			workAdded, err := c.TryAddNew(ctx)
			if err != nil {
				if errors.Is(err, ErrEndOfWork) {
					endOfWorkErr = err
					return false, nil
				}
				return false, err
			}
			if workAdded {
				return false, nil
			}

			// Block and wait for work to become available
			workExecuted, err := c.WaitForNew(ctx)
			if workExecuted {
				return true, err
			}
			if err != nil && errors.Is(err, ErrEndOfWork) {
				endOfWorkErr = err
				return false, nil
			}
			return false, err
		}()
		if workExecuted || err != nil {
			return err
		}
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
		q: q,
	}
	defer c.Close()

	// Try accepted work first
	if workExecuted, err := c.TryAccepted(ctx, false); workExecuted || err != nil {
		return workExecuted, err
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

	c.addWorkFn = func(ctx context.Context, waitCh <-chan RenotifyFunc, queueFn QueueWorkFunc) (RenotifyFunc, error) {
		if waitCh != nil {
			panic("waitCh unexpectedly not nil")
		}
		err := addWorkFn(ctx, queueFn)
		return nil, err // waitCh was not signaled
	}

	// Try adding new work
	workAdded, addErr := c.TryAddNew(ctx)
	if !workAdded || (addErr != nil && !errors.Is(addErr, ErrEndOfWork)) {
		return false, addErr
	}

	c.workWasDeferred = false
	workExecuted, err := c.TryAccepted(ctx, false)
	if !c.workWasDeferred && err == nil {
		err = addErr
	}
	return workExecuted, err
}

type controller struct {
	q               *Accepted
	buffer          *[]bufferedWork
	addWorkFn       AddWorkFunc
	renotifyFn      RenotifyFunc
	workWasDeferred bool
}

func (c *controller) Close() {
	c.requeueBuffer()
}

// TryAccepted attempts to execute work from both accepted queues.
// First exhausts newly accepted work, then tries deferred work.
func (c *controller) TryAccepted(ctx context.Context, allowSubscription bool) (bool, error) {
	traceRegion := "workq.controller.TryAccepted"
	defer trace.StartRegion(ctx, traceRegion).End()
	if workExecuted, err := c.tryAccepted(ctx, &c.q.fresh, allowSubscription); workExecuted || err != nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", workExecuted, err)
		return workExecuted, err
	}
	workExecuted, err := c.tryAccepted(ctx, &c.q.deferred, allowSubscription)
	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", workExecuted, err)
	return workExecuted, err
}

func (c *controller) tryAccepted(ctx context.Context, q *nbcq.Queue[WorkFunc], allowSubscription bool) (bool, error) {
	for {
		i := c.collectAccepted(q)
		if i < 0 {
			return false, nil
		}
		if workExecuted, err := c.execute(ctx, i, allowSubscription); workExecuted || err != nil {
			return workExecuted, err
		}
	}
}

var acceptedWorkCounter atomic.Int64

//nolint:contextcheck // background context used only for tracing
func (c *controller) queueFresh(workFn WorkFunc) {
	traceRegion := "workq.Accepted.queueFresh"
	if trace.IsEnabled() {
		defer trace.StartRegion(context.Background(), traceRegion).End()
		acceptedWorkID := acceptedWorkCounter.Add(1)
		trace.Logf(context.Background(), traceRegion,
			"Accepted(%p) queuing fresh work with acceptedWorkID=%d",
			c.q, acceptedWorkID)
		originalWorkFn := workFn
		workFn = func(ctx context.Context, ex Execution) error {
			traceRegion := traceRegion + ".workFn"
			trace.Logf(ctx, traceRegion, "executing acceptedWorkID=%d", acceptedWorkID)
			err := originalWorkFn(ctx, ex)
			if err != nil {
				trace.Logf(ctx, traceRegion, "returning err=%v", err)
			}
			return err
		}
	}
	c.q.fresh.PushBack(workPool, workFn)
}

func (c *controller) TryAddNew(ctx context.Context) (bool, error) {
	traceRegion := "workq.controller.TryAddNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workAdded=false err=ErrEndOfWork")
		return false, ErrEndOfWork
	}
	workAdded := false
	queueFn := func(workFn WorkFunc) {
		c.queueFresh(workFn)
		workAdded = true
	}

	_, err := c.addWorkFn(ctx, nil, queueFn)
	trace.Logf(ctx, traceRegion, "returning workAdded=%v err=%v", workAdded, err)
	return workAdded, err
}

func (c *controller) WaitForNew(ctx context.Context) (bool, error) {
	traceRegion := "workq.controller.WaitForNew"
	defer trace.StartRegion(ctx, traceRegion).End()
	if c.addWorkFn == nil {
		trace.Logf(ctx, traceRegion, "returning workExecuted=false err=ErrEndOfWork")
		return false, ErrEndOfWork
	}
	workExecuted := false
	var err error
	confirmFn := func() bool {
		workExecuted, err = c.retryExecutionBeforeWait(ctx)
		return !workExecuted
	}
	waiter := c.q.waiters.New(confirmFn)
	waiter.WaitFunc(func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		c.renotifyFn, err = c.addWorkFn(ctx, waitCh, c.queueFresh)
		return c.renotifyFn
	})
	trace.Logf(ctx, traceRegion, "returning workExecuted=%v err=%v", workExecuted, err)
	return workExecuted, err
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) collectAccepted(q *nbcq.Queue[WorkFunc]) int {
	workFn, ok := q.PopFront(workPool)
	if !ok {
		return -1
	}
	i := c.bufferLen()
	c.addToBuffer(workFn, q == &c.q.deferred)
	return i
}

func (c *controller) execute(ctx context.Context, i int, allowSubscription bool) (bool, error) {
	traceRegion := "workq.Accepted.execute"
	workExecuted := false
	bw := (*c.buffer)[i]
	if bw.wasDeferred {
		trace.Logf(ctx, traceRegion, "executing deferred work at buffer index %d", i)
	} else {
		trace.Logf(ctx, traceRegion, "executing fresh work at buffer index %d", i)
	}
	var commitStart func()
	commitStart = func() {
		c.commitStart(i)
		commitStart = func() {}
	}

	ex := Execution{
		Blocking: commitStart,
		Starting: func() {
			commitStart()
			workExecuted = true
		},
		Queue: c.queueFresh,
	}

	if allowSubscription {
		ex.Subscribe = c.q.monitor.Subscribe
	}

	err := bw.workFn(ctx, ex)
	if errors.Is(err, ErrEndOfWork) {
		panic("ErrEndOfWork received from work function")
	}

	if !workExecuted {
		c.workWasDeferred = true
	}

	return workExecuted, err
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) commitStart(i int) {
	traceRegion := "workq.Accepted.confirmStart"
	bw := &(*c.buffer)[i]
	if bw.wasDeferred {
		trace.Logf(context.Background(), traceRegion, "deferred work at index %d started", i)
		// The notification, if any, was productively consumed
		c.renotifyFn = nil
	} else {
		trace.Logf(context.Background(), traceRegion, "fresh work at index %d started", i)
	}
	// Make sure we don't requeue the executing item
	bw.workFn = nil
	// Requeue the remaining work items before actually
	// executing the work function
	c.requeueBuffer()
}

// retryExecutionBeforeWait handles the race condition where work might arrive
// between our last attempt and registering as a waiter. It first checks both
// accepted queues for any new work, then retries deferred work items with
// the notification function to register for later wake-up.
func (c *controller) retryExecutionBeforeWait(ctx context.Context) (bool, error) {
	// Walk through the deferred work items to retry (i.e., verify that the wait
	// is still needed) and pass the notification function to them.
	if c.buffer != nil {
		// Iterate through the buffer and retry each work item while giving each
		// a chance to register for notifications
		for i := range *c.buffer {
			workExecuted, err := c.execute(ctx, i, true)
			if workExecuted || err != nil {
				return workExecuted, err
			}
		}
	}

	// Check to make sure nothing else accumulated before we registered as a
	// waiter.
	if workExecuted, err := c.TryAccepted(ctx, true); workExecuted || err != nil {
		return workExecuted, err
	}

	// Requeue the remaining work items before waiting.
	c.requeueBuffer()
	return false, nil
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
			workFn := (*c.buffer)[i].workFn
			if workFn != nil {
				(*c.buffer)[i].workFn = nil
				trace.Logf(context.Background(), traceRegion, "pushing workFn at index %d to deferred queue", i)
				c.q.deferred.PushBack(workPool, workFn)
			}
		}
		*c.buffer = (*c.buffer)[:0]
		bufferPool.Put(c.buffer)
		c.buffer = nil
	}

	// Renotify after requeueing to avoid race in which newly ready items are
	// not yet available in the deferred queue
	renotifyFn := c.renotifyFn
	if renotifyFn != nil {
		c.renotifyFn = nil
		renotifyFn()
	}

}

//nolint:contextcheck // background context used only for tracing
func (c *controller) addToBuffer(workFn WorkFunc, wasDeferred bool) {
	traceRegion := "workq.controller.addToBuffer"
	if c.buffer == nil {
		c.buffer = bufferPool.Get().(*[]bufferedWork)
	}
	index := len(*c.buffer)
	*c.buffer = append(*c.buffer, bufferedWork{
		workFn:      workFn,
		wasDeferred: wasDeferred,
	})
	trace.Logf(context.Background(), traceRegion, "added workFn at index %d, wasDeferred=%v", index, wasDeferred)
}

func (c *controller) bufferLen() int {
	if c.buffer == nil {
		return 0
	}
	return len(*c.buffer)
}

type bufferedWork struct {
	workFn      WorkFunc
	wasDeferred bool
}

// Global pools for all Accepted instances
var workPool = &nbcq.Pool[WorkFunc]{}
var bufferPool = sync.Pool{
	New: func() any {
		return &[]bufferedWork{}
	},
}
