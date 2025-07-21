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
	cp *CombinerPool

	combinerMap      combinerMap
	workReceiver     workq.Receiver
	workWaiter       workq.Waiter
	outboxMap        outboxMap
	emitGatherOutbox *workq.Outbox
	idleTimer        *time.Timer
	doneCh           <-chan struct{}
	doneErr          func() error

	idleTimerCh            <-chan time.Time
	workReadyCh            <-chan workq.RenotifyFunc
	queueFn                []workq.QueueWorkFunc
	inboxCh                <-chan workq.Work
	flushDeadlineTimerCh   <-chan time.Time
	nextJobFlushCh         <-chan struct{}
	unregisterAsJobFlusher func()
	workReadyRenotifyFn    workq.RenotifyFunc
	followupFn             func(context.Context)
	newWork                workq.Work
	err                    error

	idleFollowupFn func(context.Context) // avoid closure reallocation
}

func (cw *cpWorker) IsSpare() bool {
	return cw.idleTimer != nil
}

func (cw *cpWorker) MayQueue() workq.QueueWorkFunc {
	if len(cw.queueFn) == 0 {
		return nil
	}
	return cw.queueFn[len(cw.queueFn)-1]
}

func (cw *cpWorker) LockAndSetQueueFunc(queueFn workq.QueueWorkFunc) (
	workReceiver *workq.Receiver, workWaiter *workq.Waiter, blockWaiter *workq.Waiter,
) {
	cw.queueFn = append(cw.queueFn, queueFn)
	return &cw.workReceiver, &cw.workWaiter, nil
}

func (cw *cpWorker) UnlockAndResetQueueFunc() {
	cw.queueFn = cw.queueFn[:len(cw.queueFn)-1]
}

func (cw *cpWorker) WithOutbox(key outboxKey[workq.Work], fn func(outbox *workq.Outbox)) {
	outbox := OutboxFor[workq.Work](&cw.outboxMap, key)
	fn(outbox)
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
	cw.queueFn = append(cw.queueFn, queueFn)
	defer func() {
		cw.queueFn = cw.queueFn[:len(cw.queueFn)-1]
	}()

	if queuedFlush, _ := cw.flushToNextDeadline(ctx); queuedFlush {
		return nil, nil
	}

	if workWaiters == nil {
		// Non-blocking mode
		if work, ok := cw.cp.combineQueue.TryPopFront(); ok {
			cw.queueFn[len(cw.queueFn)-1](work)
		}
		return nil, nil
	}

	cw.workReadyRenotifyFn = nil
	cw.err = nil
	cw.newWork = nil
	cw.followupFn = nil
	defer func() {
		cw.workReadyRenotifyFn = nil
		cw.err = nil
		cw.newWork = nil
		cw.followupFn = nil
	}()
	if !cw.IsSpare() {
		// Primary goroutine, no need for idle detection
		cw.cp.combineQueue.PopFrontFunc(&cw.workReceiver, cw.queueFn[len(cw.queueFn)-1],
			func(inboxCh <-chan workq.Work, outboxFilledCh <-chan rdvq.RenotifyFunc) (rdvq.SelectResult, rdvq.RenotifyFunc) {
				return cw.waitForWork(ctx, inboxCh, outboxFilledCh, workWaiters, confirmWorkWaitFn,
					func(inboxCh <-chan workq.Work, outboxFilledCh <-chan rdvq.RenotifyFunc) (rdvq.SelectResult, rdvq.RenotifyFunc) {
						return cw.primaryPopSelect(ctx, inboxCh, outboxFilledCh)
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
			&cw.workReceiver,
			func(outboxFilledCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				_, outboxFilledRenotifyFn := cw.waitForWork(ctx, nil, outboxFilledCh, workWaiters, confirmWorkWaitFn,
					func(
						_ <-chan workq.Work,
						outboxFilledCh <-chan rdvq.RenotifyFunc,
					) (rdvq.SelectResult, rdvq.RenotifyFunc) {
						return rdvq.SelectAborted, cw.sparePopSelect(ctx, outboxFilledCh)
					},
				)
				return outboxFilledRenotifyFn
			},
		)
		if ok {
			cw.queueFn[len(cw.queueFn)-1](work)
		}
	}

	work := cw.newWork
	if work != nil {
		cw.queueFn[len(cw.queueFn)-1](work)
	}

	followupFn := cw.followupFn
	if followupFn != nil {
		followupFn(ctx)
	}

	return cw.workReadyRenotifyFn, cw.err
}

func (cw *cpWorker) waitForWork(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxFilledCh <-chan rdvq.RenotifyFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	selectFn workq.PopSelectFunc,
) (rdvq.SelectResult, rdvq.RenotifyFunc) {
	var result rdvq.SelectResult
	var outboxFilledRenotifyFn workq.RenotifyFunc
	_ = workWaiters.WaitFunc(&cw.workWaiter, confirmWorkWaitFn,
		func(workReadyCh <-chan workq.RenotifyFunc) workq.RenotifyFunc {
			cw.workReadyCh = workReadyCh
			defer func() {
				cw.workReadyCh = nil
			}()
			result, outboxFilledRenotifyFn = selectFn(inboxCh, outboxFilledCh)
			return cw.workReadyRenotifyFn
		},
	)
	return result, outboxFilledRenotifyFn
}

func (cw *cpWorker) primaryPopSelect(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxFilledCh <-chan rdvq.RenotifyFunc,
) (result rdvq.SelectResult, renotifyFn rdvq.RenotifyFunc) {
	cw.inboxCh = inboxCh
	defer func() {
		cw.inboxCh = nil
	}()
	return cw.popSelect(ctx, outboxFilledCh, cw.primaryInnerPopSelect)
}

func (cw *cpWorker) sparePopSelect(ctx context.Context, outboxFilledCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
	_, renotifyFn := cw.popSelect(ctx, outboxFilledCh, cw.spareInnerPopSelect)
	return renotifyFn
}

func (cw *cpWorker) popSelect(ctx context.Context, outboxFilledCh <-chan rdvq.RenotifyFunc,
	innerSelect func(ctx context.Context, outboxFilledCh <-chan rdvq.RenotifyFunc) (rdvq.SelectResult, rdvq.RenotifyFunc),
) (result rdvq.SelectResult, renotifyFn rdvq.RenotifyFunc) {
	queuedFlush, timeUntilNextFlushDeadline := cw.flushToNextDeadline(ctx)
	if queuedFlush {
		return rdvq.SelectAborted, nil
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

	return innerSelect(ctx, outboxFilledCh)
}

func (cw *cpWorker) flushToNextDeadline(ctx context.Context) (bool, time.Duration) {
	queuedFlush := false
	for {
		nextBCToFlush := cw.combinerMap.NextToFlush()
		if nextBCToFlush == nil {
			break
		}
		deadline := nextBCToFlush.FlushDeadline
		timeLeft := time.Until(deadline)
		if timeLeft > 0 {
			return queuedFlush, timeLeft
		}
		// Remove from heap immediately to prevent infinite loop
		cw.combinerMap.deadlines.Remove(nextBCToFlush)
		nextBCToFlush.FlushFn(ctx)
		queuedFlush = true
	}
	return queuedFlush, 0
}

func (cw *cpWorker) primaryInnerPopSelect(
	ctx context.Context,
	outboxFilledCh <-chan rdvq.RenotifyFunc,
) (result rdvq.SelectResult, renotifyFn rdvq.RenotifyFunc) {
	traceRegion := "cpWorker.primaryInnerPopSelect"
	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxFilledCh=%p, workReadyCh=%p, flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		cw.inboxCh, outboxFilledCh, cw.workReadyCh, cw.flushDeadlineTimerCh, cw.nextJobFlushCh)
	select {
	case work := <-cw.inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", cw.inboxCh)
		cw.newWork = work
		return rdvq.SelectInboxEmptied, nil

	// Here down should be identical to spareInnerPopSelect below
	case renotifyFn := <-outboxFilledCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxFilledCh=%p", outboxFilledCh)
		return rdvq.SelectOutboxFilled, renotifyFn
	case renotifyFn := <-cw.workReadyCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workReadyCh=%p", cw.workReadyCh)
		cw.workReadyRenotifyFn = renotifyFn
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
	return rdvq.SelectAborted, nil
}

func (cw *cpWorker) spareInnerPopSelect(
	ctx context.Context,
	outboxFilledCh <-chan rdvq.RenotifyFunc,
) (result rdvq.SelectResult, renotifyFn rdvq.RenotifyFunc) {
	traceRegion := "cpWorker.spareInnerPopSelect"

	trace.Logf(ctx, traceRegion,
		//nolint:lll // doesn't make sense break up
		"entering select: idleTimerCh=%p, inboxCh=%p, outboxFilledCh=%p, workReadyCh=%p, flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		cw.idleTimerCh, cw.inboxCh, outboxFilledCh, cw.workReadyCh, cw.flushDeadlineTimerCh, cw.nextJobFlushCh)

	// Track idle time
	waitStartTime := time.Now()
	cw.cp.state.SpareWaitStarted(waitStartTime)
	defer cw.cp.state.SpareWaitEnded(waitStartTime)

	select {
	case <-cw.idleTimerCh:
		trace.Logf(ctx, traceRegion, "received idle timer signal")
		if cw.idleFollowupFn == nil {
			cw.idleFollowupFn = cw.idleFollowup
		}
		cw.followupFn = cw.idleFollowupFn

	// Here down should be identical to primaryInnerPopSelect above
	case renotifyFn := <-outboxFilledCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxFilledCh=%p", outboxFilledCh)
		return rdvq.SelectOutboxFilled, renotifyFn
	case renotifyFn := <-cw.workReadyCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workReadyCh=%p", cw.workReadyCh)
		cw.workReadyRenotifyFn = renotifyFn
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
	return rdvq.SelectAborted, nil
}

func (cw *cpWorker) idleFollowup(context.Context) {
	if cw.cp.state.ShouldExitGoroutine() {
		cw.err = workq.ErrEndOfWork
	}
}

func (cw *cpWorker) flushAll(ctx context.Context) bool {
	if cw.nextJobFlushCh == nil {
		return false
	}
	cw.combinerMap.FlushAll(ctx)
	cw.nextJobFlushCh = nil
	cw.unregisterAsJobFlusher()
	return true
}

func (cw *cpWorker) executeCombine(ctx context.Context, combineFn boundCombineFunc) {
	traceRegion := "cpWorker.executeCombine"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "cpWorker=%p", cw)
	if cw.nextJobFlushCh == nil {
		// Make sure the job won't terminate before the combiner is flushed
		cw.nextJobFlushCh, cw.unregisterAsJobFlusher = cw.cp.job.state.RegisterFlusher()
	}
	combineFn(ctx, &cw.combinerMap, cw.queueFn[len(cw.queueFn)-1], cw.emitGatherOutbox)
	cw.cp.state.IncrementCompleted()
}
