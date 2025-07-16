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
	outboxMap        outboxMap
	emitGatherOutbox *workq.Outbox
	idleTimer        *time.Timer
	doneCh           <-chan struct{}
	doneErr          func() error

	idleTimerCh            <-chan time.Time
	workReadyCh            <-chan workq.RenotifyFunc
	queueFn                workq.QueueWorkFunc
	inboxCh                <-chan workq.Work
	flushDeadlineTimerCh   <-chan time.Time
	nextJobFlushCh         <-chan struct{}
	unregisterAsJobFlusher func()
	workReadyRenotifyFn    workq.RenotifyFunc
	followupFn             func(context.Context)
	err                    error
}

func (cw *cpWorker) IsSpare() bool {
	return cw.idleTimer != nil
}

func (cw *cpWorker) MayQueue() workq.QueueWorkFunc {
	return cw.queueFn
}

func (cw *cpWorker) WithQueueFunc(queueFn workq.QueueWorkFunc, fn func()) {
	if cw.queueFn != nil {
		panic("cpWorker WithQueueFunc called while queueFn is not nil")
	}
	defer func() {
		cw.queueFn = nil
	}()
	cw.queueFn = queueFn
	fn()
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
	workReadyCh <-chan workq.RenotifyFunc,
	queueFn workq.QueueWorkFunc,
) (workq.RenotifyFunc, error) {
	cw.workReadyCh = workReadyCh
	cw.queueFn = queueFn
	defer func() {
		cw.workReadyCh = nil
		cw.queueFn = nil
	}()

	if queuedFlush, _ := cw.flushToNextDeadline(ctx); queuedFlush {
		return nil, nil
	}

	if workReadyCh == nil {
		// Non-blocking mode
		if work, ok := cw.cp.combineQueue.TryPopFront(); ok {
			cw.queueFn(work)
		}
		return nil, nil
	}

	cw.workReadyRenotifyFn = nil
	cw.err = nil
	defer func() {
		cw.workReadyRenotifyFn = nil
		cw.err = nil
	}()
	if !cw.IsSpare() {
		// Primary goroutine, no need for idle detection
		cw.cp.combineQueue.PopFrontFunc(cw.queueFn,
			func(inboxCh <-chan workq.Work, outboxFilledCh <-chan rdvq.RenotifyFunc) (rdvq.SelectResult, rdvq.RenotifyFunc) {
				return cw.primaryPopSelect(ctx, inboxCh, outboxFilledCh)
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
			func(outboxFilledCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				return cw.sparePopSelect(ctx, outboxFilledCh)
			},
		)
		if ok {
			cw.queueFn(work)
		}
	}

	followupFn := cw.followupFn
	if followupFn != nil {
		cw.followupFn = nil
		followupFn(ctx)
	}

	return cw.workReadyRenotifyFn, cw.err
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
		cw.followupFn = func(context.Context) {
			cw.queueFn(work)
		}
		return rdvq.SelectInboxEmptied, nil

	// Here down should be identical to spareWaiterSelect below
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
		cw.followupFn = func(context.Context) {
			if cw.cp.state.ShouldExitGoroutine() {
				cw.err = workq.ErrEndOfWork
			}
		}

	// Here down should be identical to primaryWaiterSelect above
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
	combineFn(ctx, &cw.combinerMap, cw.queueFn, cw.emitGatherOutbox)
	cw.cp.state.IncrementCompleted()
}
