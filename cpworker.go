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
	emitOutbox      *workq.Outbox
	idleTimer       *time.Timer
	doneCh          <-chan struct{}
	doneErr         func() error

	idleTimerCh            <-chan time.Time
	inbox                  *rdvq.Inbox[workq.Work]
	flushDeadlineTimerCh   <-chan time.Time
	nextJobFlushCh         <-chan struct{}
	unregisterAsJobFlusher func()
	followupFn             func(context.Context)
	renotifyFn             workq.RenotifyFunc
	newWork                workq.Work
	err                    error

	idleFollowupFn func(context.Context) // avoid closure reallocation
}

func (cw *cpWorker) IsSpare() bool {
	return cw.idleTimer != nil
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
	cw.renotifyFn = nil
	defer func() {
		cw.err = nil
		cw.newWork = nil
		cw.followupFn = nil
		cw.renotifyFn = nil
	}()
	if !cw.IsSpare() {
		// Primary goroutine, no need for idle detection
		cw.cp.combineQueue.PopFrontFunc(cw.Receiver(), cw.QueueFunc(),
			func(inbox *rdvq.Inbox[workq.Work], outboxWaiter *rdvq.Waiter) {
				cw.waitForWork(ctx, inbox, outboxWaiter, workWaiters, confirmWorkWaitFn,
					func(inbox *rdvq.Inbox[workq.Work], outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter) {
						cw.primaryPopSelect(ctx, inbox, outboxWaiter, workWaiter)
					},
				)
			},
		)
	} else {
		// This is the goroutine that has elected itself to execute only the
		// excess work that other goroutines didn't immediately take. This
		// will keep this goroutine idle unless it's really needed, thus
		// allowing the idle timeout to elapse (if enabled).

		// Capture the current idle timeout value to ensure consistency
		idleTimeout := cw.cp.state.IdleTimeout()
		if idleTimeout >= 0 {
			cw.idleTimer.Reset(idleTimeout)
			cw.idleTimerCh = cw.idleTimer.C
			defer func() {
				cw.idleTimerCh = nil
			}()
		}

		// Spare goroutine processes excess work (outboxes + shared channel)
		// without registering for immediate delivery
		work, ok := cw.cp.combineQueue.PopFrontExcessFunc(
			cw.Receiver(),
			func(outboxWaiter *rdvq.Waiter) {
				cw.waitForWork(ctx, nil, outboxWaiter, workWaiters, confirmWorkWaitFn,
					func(inbox *rdvq.Inbox[workq.Work], outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter) {
						cw.sparePopSelect(ctx, outboxWaiter, workWaiter)
					},
				)
			},
		)
		if ok {
			cw.queue(work)
		}
	}

	work := cw.newWork
	if work != nil {
		cw.queue(work)
	}

	followupFn := cw.followupFn
	if followupFn != nil {
		followupFn(ctx)
	}

	return cw.renotifyFn, cw.err
}

func (cw *cpWorker) queue(work workq.Work) {
	cw.queueFnStack[len(cw.queueFnStack)-1](work)
}

func (cw *cpWorker) waitForWork(
	ctx context.Context,
	inbox *rdvq.Inbox[workq.Work],
	outboxWaiter *rdvq.Waiter,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	selectFn func(inbox *rdvq.Inbox[workq.Work], outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter),
) {
	workWaiter := cw.WaiterFor(workWaiters)
	workWaiters.WaitFunc(workWaiter, confirmWorkWaitFn,
		func(waiter *rdvq.Waiter) {
			if waiter != workWaiter {
				panic("waiter does not match")
			}
			selectFn(inbox, outboxWaiter, waiter)
		},
	)
	cw.renotifyFn = workWaiter.ExtractRenotifyFn()
}

func (cw *cpWorker) primaryPopSelect(
	ctx context.Context,
	inbox *rdvq.Inbox[workq.Work],
	outboxWaiter *rdvq.Waiter,
	workWaiter *workq.Waiter,
) {
	cw.inbox = inbox
	defer func() {
		cw.inbox = nil
	}()
	cw.popSelect(ctx, outboxWaiter, workWaiter, cw.primaryInnerPopSelect)
}

func (cw *cpWorker) sparePopSelect(ctx context.Context, outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter) {
	cw.popSelect(ctx, outboxWaiter, workWaiter, cw.spareInnerPopSelect)
}

func (cw *cpWorker) popSelect(ctx context.Context, outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter,
	innerSelect func(ctx context.Context, outboxWaiter *rdvq.Waiter, workWaiter *workq.Waiter),
) {
	queuedFlush, timeUntilNextFlushDeadline := cw.flushToNextDeadline(ctx)
	if queuedFlush {
		return
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

	innerSelect(ctx, outboxWaiter, workWaiter)
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
		next.Flush(ctx, cw.emitOutbox)
		queuedFlush = true
	}
	return queuedFlush, 0
}

func (cw *cpWorker) primaryInnerPopSelect(
	ctx context.Context,
	outboxWaiter *rdvq.Waiter,
	workWaiter *workq.Waiter,
) {
	traceRegion := "cpWorker.primaryInnerPopSelect"
	inboxCh := cw.inbox.Ch()
	outboxWaiterCh := outboxWaiter.Ch()
	workWaiterCh := workWaiter.Ch()
	trace.Logf(ctx, traceRegion,
		"entering select: inbox=%p inboxCh=%p, outboxWaiter=%p, outboxWaiterCh=%p, workWaiter=%p, workWaiterCh=%p, "+
			"flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		cw.inbox, inboxCh, outboxWaiter, outboxWaiterCh, workWaiter, workWaiterCh,
		cw.flushDeadlineTimerCh, cw.nextJobFlushCh)
	select {
	case work := <-inboxCh:
		cw.inbox.Emptied()
		trace.Logf(ctx, traceRegion, "received work from inbox=%p, inboxCh=%p", cw.inbox, inboxCh)
		cw.newWork = work

	// Here down should be identical to spareInnerPopSelect below
	case renotifyFn := <-outboxWaiterCh:
		outboxWaiter.Notified(renotifyFn)
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaiter=%p, outboxWaiterCh=%p",
			outboxWaiter, outboxWaiterCh)
	case renotifyFn := <-workWaiterCh:
		workWaiter.Notified(renotifyFn)
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaiter=%p, workWaiterCh=%p", workWaiter, workWaiterCh)
	case <-cw.flushDeadlineTimerCh:
		trace.Logf(ctx, traceRegion, "received flush deadline signal")
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
}

func (cw *cpWorker) spareInnerPopSelect(
	ctx context.Context,
	outboxWaiter *rdvq.Waiter,
	workWaiter *workq.Waiter,
) {
	traceRegion := "cpWorker.spareInnerPopSelect"

	outboxWaiterCh := outboxWaiter.Ch()
	workWaiterCh := workWaiter.Ch()
	trace.Logf(ctx, traceRegion,
		"entering select: idleTimerCh=%p, outboxWaiter=%p, outboxWaiterCh=%p, workWaiter=%p, workWaiterCh=%p, "+
			"flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		cw.idleTimerCh, outboxWaiter, outboxWaiterCh, workWaiter, workWaiterCh,
		cw.flushDeadlineTimerCh, cw.nextJobFlushCh)

	// Track idle time
	waitStartTime := time.Now()
	cw.cp.state.SpareWaitStarted(waitStartTime)
	defer cw.cp.state.SpareWaitEnded(waitStartTime)

	select {
	case <-cw.idleTimerCh:
		trace.Logf(ctx, traceRegion, "received idle timer signal")
		cw.followupFn = cw.idleFollowupFn

	// Here down should be identical to primaryInnerPopSelect above
	case renotifyFn := <-outboxWaiterCh:
		outboxWaiter.Notified(renotifyFn)
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaiter=%p, outboxWaiterCh=%p",
			outboxWaiter, outboxWaiterCh)
	case renotifyFn := <-workWaiterCh:
		workWaiter.Notified(renotifyFn)
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaiter=%p, workWaiterCh=%p", workWaiter, workWaiterCh)
	case <-cw.flushDeadlineTimerCh:
		trace.Logf(ctx, traceRegion, "received flush deadline signal")
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
}

func (cw *cpWorker) idleFollowup(context.Context) {
	if cw.cp.state.ShouldExitGoroutine() {
		cw.err = workq.ErrEndOfWork
	}
}

func (cw *cpWorker) flushAll(ctx context.Context) bool {
	traceRegion := "cpWorker.flushAll"
	defer trace.StartRegion(ctx, traceRegion).End()
	if cw.nextJobFlushCh == nil {
		return false
	}
	cw.activeCombiners.FlushAll(ctx, cw.emitOutbox)
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
	bc.Combine(ctx, cw.activeCombiners, cw.emitOutbox)
	cw.cp.state.IncrementCompleted()
}
