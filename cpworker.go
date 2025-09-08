// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/workq"
)

type cpWorker struct {
	integrationExEnv
	cp *CombinerPool

	activeCombiners *activeCombinerMap
	idleTimer       *time.Timer
	doneCh          <-chan struct{}
	doneErr         func() error

	idleTimerCh            <-chan time.Time
	flushDeadlineTimerCh   <-chan time.Time
	nextJobFlushCh         <-chan struct{}
	unregisterAsJobFlusher func()
	followupFn             func(context.Context)
	workRenotifyFn         workq.RenotifyFunc
	newWork                workq.Work
	err                    error
}

func (cw *cpWorker) Lock() {}

func (cw *cpWorker) Unlock() {}

func (cw *cpWorker) ExecuteNowOrQueue(ctx context.Context, ex workq.Execution, work workq.Work) error {
	return cw.cp.workQueue.ExecuteNowOrQueue(ctx, ex, work)
}

func (cw *cpWorker) TryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	if queuedFlush, _ := cw.flushToNextDeadline(ctx); queuedFlush {
		return nil
	}
	if work, ok := cw.cp.combineQueue.TryPopFront(); ok {
		queueFn(work)
		return nil
	}
	return nil
}

func (cw *cpWorker) AddWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	cw.PushQueueFunc(queueFn)
	defer cw.PopQueueFunc()

	if queuedFlush, _ := cw.flushToNextDeadline(ctx); queuedFlush {
		return nil, nil
	}

	if workWaiters == nil {
		// Non-blocking mode
		if work, ok := cw.cp.combineQueue.TryPopFront(); ok {
			cw.queue(work)
		}
		return nil, nil
	}

	cw.err = nil
	cw.newWork = nil
	cw.followupFn = nil
	cw.workRenotifyFn = nil
	defer func() {
		cw.err = nil
		cw.newWork = nil
		cw.followupFn = nil
		cw.workRenotifyFn = nil
	}()

	// Capture the current idle timeout value to ensure consistency
	idleTimeout := cw.cp.state.IdleTimeout()
	if idleTimeout >= 0 {
		cw.idleTimer.Reset(idleTimeout)
		cw.idleTimerCh = cw.idleTimer.C
		defer func() {
			cw.idleTimerCh = nil
		}()
	}

	// Primary goroutine, no need for idle detection
	workWaiters.WaitFunc(cw.Waiter(), confirmWorkWaitFn,
		func(workWaitInbox *rdvq.WaitInbox) {
			cw.cp.combineQueue.PopFrontFunc(cw.Receiver(), cw.QueueFunc(),
				func(inbox *rdvq.Inbox[workq.Work], outboxWaitInbox *rdvq.WaitInbox) rdvq.RenotifyFunc {
					return cw.popSelect(ctx, inbox, outboxWaitInbox, workWaitInbox)
				},
			)
		},
	)

	work := cw.newWork
	if work != nil {
		cw.queue(work)
	}

	followupFn := cw.followupFn
	if followupFn != nil {
		followupFn(ctx)
	}

	return cw.workRenotifyFn, cw.err
}

func (cw *cpWorker) queue(work workq.Work) {
	cw.queueFnStack[len(cw.queueFnStack)-1](work)
}

func (cw *cpWorker) popSelect(
	ctx context.Context,
	inbox *rdvq.Inbox[workq.Work],
	outboxWaitInbox *rdvq.WaitInbox,
	workWaitInbox *rdvq.WaitInbox,
) rdvq.RenotifyFunc {
	traceRegion := "cpWorker.popSelect"
	defer trace.StartRegion(ctx, traceRegion).End()

	queuedFlush, timeUntilNextFlushDeadline := cw.flushToNextDeadline(ctx)
	if queuedFlush {
		return nil
	}
	if timeUntilNextFlushDeadline > 0 {
		// Set up flush deadline timer
		flushDeadlineTimer := timerp.Get()
		flushDeadlineTimer.Reset(timeUntilNextFlushDeadline)
		cw.flushDeadlineTimerCh = flushDeadlineTimer.C
		defer func() {
			cw.flushDeadlineTimerCh = nil
			timerp.Put(flushDeadlineTimer)
		}()
	}

	inboxCh := inbox.Ch()
	outboxWaitCh := outboxWaitInbox.Ch()
	workWaitCh := workWaitInbox.Ch()
	trace.Logf(ctx, traceRegion,
		"entering select: inbox=%p inboxCh=%p, outboxWaitInbox=%p, outboxWaitCh=%p, workWaiter=%p, workWaiterCh=%p, "+
			"flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		inbox, inboxCh, outboxWaitInbox, outboxWaitCh, workWaitInbox, workWaitCh,
		cw.flushDeadlineTimerCh, cw.nextJobFlushCh)
	select {
	case work := <-inboxCh:
		inbox.Emptied()
		trace.Logf(ctx, traceRegion, "received work from inbox=%p, inboxCh=%p", inbox, inboxCh)
		cw.newWork = work
	case renotifyFn := <-outboxWaitCh:
		outboxWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitInbox=%p, outboxWaitCh=%p",
			outboxWaitInbox, outboxWaitCh)
		return renotifyFn
	case cw.workRenotifyFn = <-workWaitCh:
		workWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitInbox=%p, workWaitCh=%p",
			workWaitInbox, workWaitCh)
	case <-cw.flushDeadlineTimerCh:
		trace.Logf(ctx, traceRegion, "received flush deadline signal")
	case <-cw.idleTimerCh:
		trace.Logf(ctx, traceRegion, "received idle timer signal")
		cw.err = workq.ErrEndOfWork
	case <-cw.nextJobFlushCh:
		trace.Logf(ctx, traceRegion, "received job flush signal")
		cw.followupFn = func(ctx context.Context) {
			cw.flushAll(ctx)
		}
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		cw.err = ctx.Err()
	case <-cw.doneCh:
		trace.Logf(ctx, traceRegion, "received combiner goroutine done signal")
		cw.err = cw.doneErr()
	}
	return nil
}

func (cw *cpWorker) flushToNextDeadline(ctx context.Context) (bool, time.Duration) {
	queuedFlush := false
	for {
		next, deadline := cw.activeCombiners.NextToFlush()
		if next == nil {
			break
		}

		timeLeft := time.Until(deadline)
		if timeLeft > 0 {
			return queuedFlush, timeLeft
		}

		// Remove from map immediately to prevent infinite loop
		cw.activeCombiners.Remove(next)
		next.Flush(ctx, cw.Sender())
		queuedFlush = true
	}
	return queuedFlush, 0
}

func (cw *cpWorker) flushAll(ctx context.Context) bool {
	traceRegion := "cpWorker.flushAll"
	defer trace.StartRegion(ctx, traceRegion).End()
	if cw.nextJobFlushCh == nil {
		return false
	}
	cw.activeCombiners.FlushAll(ctx, cw.Sender())
	cw.nextJobFlushCh = nil
	cw.unregisterAsJobFlusher()
	return true
}

func (cw *cpWorker) executeCombine(ctx context.Context, bc boundCombineWork) {
	traceRegion := "cpWorker.executeCombine"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "cpWorker=%p", cw)
	if cw.nextJobFlushCh == nil {
		// Make sure the job won't terminate before the combiner is flushed
		cw.nextJobFlushCh, cw.unregisterAsJobFlusher = cw.cp.job.state.RegisterFlusher()
	}
	bc.Combine(ctx, cw.activeCombiners, cw.Sender())
	cw.cp.state.IncrementCompleted()
}
