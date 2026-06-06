// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/workq"
)

type cpWorker struct {
	integrationExEnv
	cp *FunnelPool

	idleTimer *time.Timer
	doneCh    <-chan struct{}
	doneErr   func() error

	// readyBuf is the worker's reusable scratch slice for delayq.Drain
	// returns. Lives on the worker to avoid allocating on every drain.
	readyBuf []funnelFlusher

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
	if work, ok := cw.cp.funnelQueue.TryPopFront(); ok {
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
	_ <-chan time.Time, // wired in checkpoint 1b-ii; funnel flush still uses cp.flushQ
) (workq.RenotifyFunc, error) {
	cw.PushQueueFunc(queueFn)
	defer cw.PopQueueFunc()

	if queuedFlush, _ := cw.flushToNextDeadline(ctx); queuedFlush {
		return nil, nil
	}

	if workWaiters == nil {
		// Non-blocking mode
		if work, ok := cw.cp.funnelQueue.TryPopFront(); ok {
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
		// Add jitter to spread out mutex contention when multiple workers timeout
		maxJitter := cw.cp.state.IdleJitter()
		jitter := time.Duration(rand.Int64N(int64(maxJitter))) //nolint:gosec // jitter doesn't need crypto/rand
		cw.idleTimer.Reset(idleTimeout + jitter)
		cw.idleTimerCh = cw.idleTimer.C
		defer func() {
			cw.idleTimerCh = nil
		}()
	}

	// Primary goroutine, no need for idle detection
	workWaiters.WaitFunc(cw.Waiter(), confirmWorkWaitFn,
		func(workWaitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
			work, ok := cw.cp.funnelQueue.PopFrontFunc(cw.Receiver(),
				func(inboxCh <-chan workq.Work, outboxWaitCh <-chan rdvq.RenotifyFunc) rdvq.PopSelectResult[workq.Work] {
					return cw.popSelect(ctx, inboxCh, outboxWaitCh, workWaitCh)
				},
			)
			if ok {
				cw.newWork = work
			}
			return cw.workRenotifyFn
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
	inboxCh <-chan workq.Work,
	outboxWaitCh <-chan rdvq.RenotifyFunc,
	workWaitCh <-chan rdvq.RenotifyFunc,
) (result rdvq.PopSelectResult[workq.Work]) {
	traceRegion := "cpWorker.popSelect"
	defer trace.StartRegion(ctx, traceRegion).End()

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

	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxWaitCh=%p, workWaitCh=%p, flushDeadlineTimerCh=%p, nextJobFlushCh=%p",
		inboxCh, outboxWaitCh, workWaitCh, cw.flushDeadlineTimerCh, cw.nextJobFlushCh)
	select {
	case work := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", inboxCh)
		result.InboxEmptied(work)
	case renotifyFn := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitCh=%p", outboxWaitCh)
		result.OutboxReady(renotifyFn)
	case cw.workRenotifyFn = <-workWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitCh=%p", workWaitCh)
	case <-cw.flushDeadlineTimerCh:
		trace.Logf(ctx, traceRegion, "received flush deadline signal")
	case <-cw.idleTimerCh:
		trace.Logf(ctx, traceRegion, "received idle timer signal")
		if cw.cp.state.TryIdleExit() {
			cw.err = workq.ErrEndOfWork
		}
	case <-cw.nextJobFlushCh:
		trace.Logf(ctx, traceRegion, "received job flush signal")
		cw.followupFn = func(ctx context.Context) {
			cw.flushAll(ctx)
		}
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		cw.err = ctx.Err()
	case <-cw.doneCh:
		trace.Logf(ctx, traceRegion, "received funnel goroutine done signal")
		cw.err = cw.doneErr()
	}
	return
}

// flushToNextDeadline drains the pool's shared flush queue of every
// item whose deadline has expired, flushes each, and returns
// (queuedFlush, timeLeft). queuedFlush reports whether any items were
// flushed; timeLeft is the duration until the next pending deadline
// (zero when the queue is empty). The caller uses timeLeft to arm its
// per-worker flush deadline timer.
func (cw *cpWorker) flushToNextDeadline(ctx context.Context) (bool, time.Duration) {
	ready, next := cw.cp.flushQ.Drain(time.Now(), cw.readyBuf[:0])
	cw.readyBuf = ready

	for _, c := range ready {
		c.Flush(ctx, cw.Sender())
	}

	queuedFlush := len(ready) > 0
	if next.IsZero() {
		return queuedFlush, 0
	}
	timeLeft := time.Until(next)
	if timeLeft < 0 {
		timeLeft = 0
	}
	return queuedFlush, timeLeft
}

// flushAll drains every still-pending entry from the flushQ and flushes
// each. Used at job-end when the pool needs to deliver final flushes
// before exiting.
func (cw *cpWorker) flushAll(ctx context.Context) bool {
	traceRegion := "cpWorker.flushAll"
	defer trace.StartRegion(ctx, traceRegion).End()
	if cw.nextJobFlushCh == nil {
		return false
	}
	// Use a far-future "now" so every queued item is treated as expired.
	farFuture := time.Now().Add(maxFlushAllSkew)
	ready, _ := cw.cp.flushQ.Drain(farFuture, cw.readyBuf[:0])
	cw.readyBuf = ready
	for _, c := range ready {
		c.Flush(ctx, cw.Sender())
	}
	cw.nextJobFlushCh = nil
	cw.unregisterAsJobFlusher()
	return true
}

// maxFlushAllSkew is the offset added to time.Now() when draining the
// flushQ wholesale at job end. Large enough to subsume any reasonable
// future deadline.
const maxFlushAllSkew = 24 * time.Hour

func (cw *cpWorker) executeFunnel(ctx context.Context, bc boundFunnelWork) {
	traceRegion := "cpWorker.executeFunnel"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "cpWorker=%p", cw)
	if cw.nextJobFlushCh == nil {
		// Make sure the job won't terminate before the funnel is flushed
		cw.nextJobFlushCh, cw.unregisterAsJobFlusher = cw.cp.job.state.RegisterFlusher()
	}
	bc.Funnel(ctx, &cw.cp.flushQ, cw.Sender())
	cw.cp.state.IncrementCompleted()
}
