// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/workq"
)

type cpWorker struct {
	integrationExEnv
	cp *FunnelPool

	idleTimer *time.Timer
	doneCh    <-chan struct{}
	doneErr   func() error

	idleTimerCh            <-chan time.Time
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

func (cw *cpWorker) TryAddWork(_ context.Context, queueFn workq.QueueWorkFunc) error {
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
	timedCh <-chan time.Time, // fires when the next scheduled flush deadline arrives
) (workq.RenotifyFunc, error) {
	cw.PushQueueFunc(queueFn)
	defer cw.PopQueueFunc()

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
					return cw.popSelect(ctx, inboxCh, outboxWaitCh, workWaitCh, timedCh)
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
	timedCh <-chan time.Time,
) (result rdvq.PopSelectResult[workq.Work]) {
	traceRegion := "cpWorker.popSelect"
	defer trace.StartRegion(ctx, traceRegion).End()

	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxWaitCh=%p, workWaitCh=%p, timedCh=%p, nextJobFlushCh=%p",
		inboxCh, outboxWaitCh, workWaitCh, timedCh, cw.nextJobFlushCh)
	select {
	case work := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", inboxCh)
		result.InboxEmptied(work)
	case renotifyFn := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitCh=%p", outboxWaitCh)
		result.OutboxReady(renotifyFn)
	case cw.workRenotifyFn = <-workWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitCh=%p", workWaitCh)
	case <-timedCh:
		// A scheduled flush deadline arrived; return so ExecuteOne re-drains
		// the now-due timed work into the fresh queue.
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

// timedFlusher is the end-of-work view of a scheduled flush item: a
// funnelInstance satisfies it. flushAll runs these synchronously rather
// than routing them back through ExecuteOne, so a flush is never left
// queued when the last goroutine decides to exit.
type timedFlusher interface {
	Flush(ctx context.Context, sender *rdvq.Sender)
}

// flushAll drains every still-pending scheduled flush from the timed work
// queue and flushes each synchronously, then unregisters this worker as a
// job flusher. Used at job-end to deliver final flushes for not-yet-due
// instances. Returns false only when this worker is not registered.
func (cw *cpWorker) flushAll(ctx context.Context) bool {
	traceRegion := "cpWorker.flushAll"
	defer trace.StartRegion(ctx, traceRegion).End()
	if cw.nextJobFlushCh == nil {
		return false
	}
	for _, w := range cw.cp.workQueue.DrainAllTimed(nil) {
		w.(timedFlusher).Flush(ctx, cw.Sender())
	}
	cw.nextJobFlushCh = nil
	cw.unregisterAsJobFlusher()
	return true
}

// maxFlushAllSkew is the offset added to time.Now() for the no-deadline
// flush placeholder so the job-end sweep finds such instances. Large
// enough to subsume any reasonable future deadline.
const maxFlushAllSkew = 24 * time.Hour

func (cw *cpWorker) executeFunnel(ctx context.Context, bc boundFunnelWork) {
	traceRegion := "cpWorker.executeFunnel"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "cpWorker=%p", cw)
	if cw.nextJobFlushCh == nil {
		// Make sure the job won't terminate before the funnel is flushed
		cw.nextJobFlushCh, cw.unregisterAsJobFlusher = cw.cp.job.state.RegisterFlusher()
	}
	bc.Funnel(ctx, cw.Sender())
	cw.cp.state.IncrementCompleted()
}
