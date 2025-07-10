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
}

// Init initializes the work queue using the global pool.
func (q *Accepted) Init() {
	q.fresh.Init(workPool)
	q.deferred.Init(workPool)
	q.waiters.Init()
}

// AddWorkFunc provides new work to the queue processor. It is called with a
// waitCh that signals when there is deferred work ready to process. If waitCh
// is nil, AddWorkFunc should not block. A queueFn is provided that should be
// called for each work item accepted. Returns whether the waitCh was signaled
// or not.
type AddWorkFunc = func(ctx context.Context, waitCh <-chan RenotifyFunc, queueFn QueueWorkFunc) (RenotifyFunc, error)

type RenotifyFunc = rdvq.RenotifyFunc

// QueueWorkFunc is called by AddWorkFunc to add new work items to processing.
type QueueWorkFunc = func(WorkFunc)

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
	defer trace.StartRegion(ctx, "workq.Accepted.ExecuteOne").End()
	trace.Logf(ctx, "workq.Accepted.ExecuteOne", "q=%p", q)

	// Used to propagate wokeByNotify flag from a wait at the end of the loop to
	// the controller created at the beginning
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

			trace.Log(ctx, "workq.Accepted.ExecuteOne", "controller created, trying accepted work")
			var err error
			if workExecuted, err := c.TryAccepted(ctx, nil); workExecuted || err != nil {
				trace.Logf(ctx, "workq.Accepted.ExecuteOne", "TryAccepted returned workExecuted=%v err=%v", workExecuted, err)
				return workExecuted, err
			}
			trace.Log(ctx, "workq.Accepted.ExecuteOne", "TryAccepted found no work, trying new work")

			if endOfWorkErr != nil {
				return false, endOfWorkErr
			}

			// Avoid full blocking protocol if we can
			workAdded, err := c.TryAddNew(ctx)
			if err != nil {
				trace.Logf(ctx, "workq.Accepted.ExecuteOne", "TryAddNew returned workAdded=%v err=%v", workAdded, err)
				if errors.Is(err, ErrEndOfWork) {
					endOfWorkErr = err
					return false, nil
				}
				return false, err
			}
			if workAdded {
				trace.Logf(ctx, "workq.Accepted.ExecuteOne", "TryAddNew succeeded")
				return false, nil
			}

			// Block and wait for work to become available
			trace.Logf(ctx, "workq.Accepted.ExecuteOne", "calling WaitForNew")
			workExecuted, err := c.WaitForNew(ctx)
			trace.Logf(ctx, "workq.Accepted.ExecuteOne", "WaitForNew returned err=%v", err)
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
type TryAddWorkFunc = func(context.Context, QueueWorkFunc) error

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
	if workExecuted, err := c.TryAccepted(ctx, nil); workExecuted || err != nil {
		return workExecuted, err
	}

	if addWorkFn == nil {
		return false, ErrEndOfWork
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

	workExecuted, err := c.TryAccepted(ctx, nil)
	if err == nil {
		err = addErr
	}
	return workExecuted, err
}

type controller struct {
	q          *Accepted
	buffer     *[]bufferedWork
	addWorkFn  AddWorkFunc
	renotifyFn RenotifyFunc
}

func (c *controller) Close() {
	c.requeueBuffer()
}

// TryAccepted attempts to execute work from both accepted queues.
// First exhausts newly accepted work, then tries deferred work.
func (c *controller) TryAccepted(ctx context.Context, readyFn NotifyFunc) (bool, error) {
	if workExecuted, err := c.tryAccepted(ctx, &c.q.fresh, readyFn); workExecuted || err != nil {
		return workExecuted, err
	}
	return c.tryAccepted(ctx, &c.q.deferred, readyFn)
}

func (c *controller) tryAccepted(ctx context.Context, q *nbcq.Queue[WorkFunc], readyFn NotifyFunc) (bool, error) {
	for {
		i := c.collectAccepted(q)
		if i < 0 {
			return false, nil
		}
		if workExecuted, err := c.execute(ctx, i, readyFn); workExecuted || err != nil {
			return workExecuted, err
		}
	}
}

var acceptedWorkCounter atomic.Int64

//nolint:contextcheck // background context used only for tracing
func (c *controller) queueFresh(workFn WorkFunc) {
	traceRegion := "workq.Accepted.queueFresh"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	acceptedWorkID := acceptedWorkCounter.Add(1)
	trace.Logf(context.Background(), traceRegion, "Accepted(%p) queuing fresh work with acceptedWorkID=%d", c.q, acceptedWorkID)
	c.q.fresh.PushBack(workPool, func(ctx context.Context, ex Execution) error {
		trace.Logf(ctx, traceRegion+".workFn", "executing acceptedWorkID=%d", acceptedWorkID)
		return workFn(ctx, ex)
	})
}

func (c *controller) TryAddNew(ctx context.Context) (bool, error) {
	if c.addWorkFn == nil {
		return false, ErrEndOfWork
	}
	workAdded := false
	queueFn := func(workFn WorkFunc) {
		c.queueFresh(workFn)
		workAdded = true
	}

	_, err := c.addWorkFn(ctx, nil, queueFn)
	return workAdded, err
}

func (c *controller) WaitForNew(ctx context.Context) (bool, error) {
	if c.addWorkFn == nil {
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

func (c *controller) execute(ctx context.Context, i int, readyFn NotifyFunc) (bool, error) {
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
	err := bw.workFn(ctx, Execution{
		Blocking: func() {
			commitStart()
		},
		Starting: func() {
			commitStart()
			workExecuted = true
		},
		ReadyFn: readyFn,
		Queue:   c.queueFresh,
	})
	if errors.Is(err, ErrEndOfWork) {
		panic("ErrEndOfWork received from work function")
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
	readyFn := func(renotifyFn RenotifyFunc) {
		trace.Logf(ctx, "workq.retryExecutionBeforeWait", "readyFn called, notifying waiters")
		c.q.waiters.Notify(renotifyFn)
	}

	// Walk through the deferred work items to retry (i.e., verify that the wait
	// is still needed) and pass the notification function to them.
	if c.buffer != nil {
		// Iterate through the buffer and retry each work item while giving each
		// a chance to register the readyFn
		for i := range *c.buffer {
			workExecuted, err := c.execute(ctx, i, readyFn)
			if workExecuted || err != nil {
				return workExecuted, err
			}
		}
	}

	// Check to make sure nothing else accumulated before we registered as a
	// waiter.
	if workExecuted, err := c.TryAccepted(ctx, readyFn); workExecuted || err != nil {
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
	defer trace.StartRegion(context.Background(), "workq.requeueBuffer").End()
	trace.Logf(context.Background(), "workq.requeueBuffer", "buffer=%p", c.buffer)
	if c.buffer != nil {
		trace.Logf(context.Background(), "workq.requeueBuffer", "buffer contents=%v", *c.buffer)
		for i := range *c.buffer {
			workFn := (*c.buffer)[i].workFn
			if workFn != nil {
				(*c.buffer)[i].workFn = nil
				trace.Logf(context.Background(), "workq.requeueBuffer", "pushing %p at index %d to deferred queue", workFn, i)
				c.q.deferred.PushBack(workPool, workFn)
				trace.Logf(context.Background(), "workq.requeueBuffer", "pushed %p", workFn)
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

	trace.Logf(context.Background(), "workq.requeueBuffer", "done")
}

//nolint:contextcheck // background context used only for tracing
func (c *controller) addToBuffer(workFn WorkFunc, wasDeferred bool) {
	defer trace.StartRegion(context.Background(), "workq.addToBuffer").End()
	trace.Logf(context.Background(), "workq.addToBuffer", "buffer=%p workFn=%p", c.buffer, workFn)
	if c.buffer == nil {
		c.buffer = bufferPool.Get().(*[]bufferedWork)
	}
	*c.buffer = append(*c.buffer, bufferedWork{
		workFn:      workFn,
		wasDeferred: wasDeferred,
	})
	trace.Logf(context.Background(), "workq.addToBuffer", "buffer contents: %v", *c.buffer)
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
