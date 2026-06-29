// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

// ─────────────────────────────────────────────────────────────────────────────
// Scheduler is the admission half of the dispatch/execution split (Phase 2b C2).
// It is a demand-spawned pool (execpool.Pool) of workers that admit work — never
// run user bodies — so a scheduler is always promptly available to take new work
// (the always-live-dispatcher invariant). It REPLACES the obsolete worker.Pool.
//
// Intake is an unbuffered rdvq.Handoff (`incoming`): producers PushBack admission
// work, scheduler workers PopFront it. The Handoff's block-as-demand IS the pool's
// spawn signal (Post's selectFn fires RegisterUnmetDemand, mirroring
// execpool.Executor.PushBack) — there is no separate demand counter. Admission
// itself runs through the existing Accepted priority engine (fresh → postponed →
// scheduled): a worker's Wait drives one ExecuteOne (composing execpool's idle for
// scale-to-zero), which executes one admission scatter-work; that scatter-work, on
// success, hands the admitted body to the executor pool (a SEPARATE Handoff, in
// streampool) and, on a permit miss, postpones onto Accepted. Work is therefore a
// no-op — ExecuteOne already did the admission in Wait.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/execpool"
	"github.com/petenewcomb/streampool/internal/rdvq"
)

// Scheduler is the admission pool. Construct with [NewScheduler]; the zero value is
// not usable. Producers reach it through [Scheduler.Post] and the scheduled/timed
// methods; the consuming side is driven by the pool's own [schedulerWorker]s.
type Scheduler struct {
	pool *execpool.Pool[*schedulerWorker]

	// incoming is the producer→scheduler intake: an unbuffered rendezvous whose
	// block-as-demand spawns scheduler workers (see [Scheduler.Post]).
	incoming rdvq.Handoff[Work]

	// accepted is the fresh/postponed priority engine plus scheduled/timed work.
	// The scheduler drives it via ExecuteOne; admission scatter-works that miss a
	// permit postpone here and are retried on a permit-free / governor-clear wake.
	accepted Accepted
}

// NewScheduler constructs an admission pool. The Accepted engine's internal
// demand signal (batch-fresh accumulation, Expedite/ForceFresh) is wired to
// [Scheduler.ensureWorker] so a promotion with no parked worker brings one up.
func NewScheduler() *Scheduler {
	s := &Scheduler{}
	s.incoming.Init()
	s.accepted.Init(s.ensureWorker)
	s.pool = execpool.NewPool(func() *schedulerWorker {
		w := &schedulerWorker{sched: s}
		w.pullFn = w.pull
		return w
	})
	return s
}

// ── Lifecycle (delegated to the pool) ─────────────────────────────────────────

func (s *Scheduler) Acquire() { s.pool.Acquire() }
func (s *Scheduler) Release() { s.pool.Release() }
func (s *Scheduler) Wait()    { s.pool.Wait() }

// ── Producer side ─────────────────────────────────────────────────────────────

// Post hands admission work w to a scheduler, blocking until one takes it or ctx is
// cancelled. If no scheduler is waiting it fires demand (block-as-demand): the
// producer parks holding w and a scheduler is spawned, which takes it directly — no
// buffer dwell. Because schedulers never run user bodies, a parked producer rendezvous
// is short (a scheduler is always promptly available or quickly spawned). Mirrors
// [execpool.Executor.PushBack].
func (s *Scheduler) Post(ctx context.Context, w Work) error {
	registered := false
	defer func() {
		if registered {
			// The demand this Post registered is now met (delivered) or withdrawn
			// (ctx cancelled) — the producer is no longer waiting.
			s.pool.UnregisterUnmetDemand()
		}
	}()

	var err error
	delivered := s.incoming.PushBackFunc(w, func(waitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
		// selectFn runs only when no scheduler was waiting — i.e. the producer is
		// about to park. Register unmet demand once (on the first park) so the pool
		// spawns toward it.
		if !registered {
			registered = true
			s.pool.RegisterUnmetDemand()
		}
		var rf rdvq.RenotifyFunc
		rf, err = rdvq.BasicWaitSelect(ctx, waitCh)
		return rf
	})
	if delivered {
		return nil
	}
	return err
}

// ensureWorker is the Accepted engine's unmet-demand hook: it is fired when fresh
// admission work accumulates with no parked worker (queueFresh batch) or when a
// scheduled item is forced/expedited. It brings up a scheduler if none is around to
// drain that work — otherwise a flush coming due after the pool has scaled to zero
// would never run. This Accepted-internal demand is fire-and-forget (not
// counter-balanced like the incoming Handoff's Post demand), so it routes to
// [execpool.Pool.Nudge] — the direct analog of the obsolete worker.Pool's TrySpawn.
func (s *Scheduler) ensureWorker() { s.pool.Nudge() }

// ── Scheduled / timed work (flush deadlines) ─────────────────────────────────

func (s *Scheduler) Schedule(w ScheduledWork, at time.Time) { s.accepted.Schedule(w, at) }
func (s *Scheduler) Reschedule(w ScheduledWork, at time.Time) bool {
	return s.accepted.Reschedule(w, at)
}
func (s *Scheduler) ClaimForFlush(w ScheduledWork) bool { return s.accepted.ClaimForFlush(w) }
func (s *Scheduler) ForceFresh(w Work)                  { s.accepted.ForceFresh(w) }
func (s *Scheduler) DrainAllScheduled(dst []ScheduledWork) []ScheduledWork {
	return s.accepted.DrainAllScheduled(dst)
}

// ── Synchronous dispatch (a body running sub-work inline) ─────────────────────

// ExecuteNowOrQueue runs w synchronously on the caller's goroutine, or — if it
// postpones — parks it on the Accepted postponed queue for a scheduler to retry.
func (s *Scheduler) ExecuteNowOrQueue(ctx context.Context, ex Execution, w Work) error {
	return s.accepted.ExecuteNowOrQueue(ctx, ex, w)
}

// ── Consumer side: the scheduler worker (an execpool.Worker) ──────────────────

// schedulerWorker drives the Accepted engine on behalf of one pool goroutine. The
// execpool.Pool runs `for { Wait; Work } ; Close`; the scheduler does all its work in
// Wait (one ExecuteOne) and leaves Work empty, because admission + the body handoff
// both happen inside ExecuteOne's work.Execute.
type schedulerWorker struct {
	sched *Scheduler

	// pullFn is the bound pull (the AddWorkFunc ExecuteOne calls), cached to avoid a
	// per-Wait closure alloc.
	pullFn AddWorkFunc

	// per-Wait scratch (set in Wait, read in pull/selectWork):
	idleCh   <-chan time.Time
	renotify RenotifyFunc
	exit     bool  // idle/stop fired: stop this worker
	selErr   error // ctx cancel surfaced from the select
}

// Wait drives one ExecuteOne: it finds and executes a single admission item (fresh →
// postponed → pulled from incoming), composing execpool's idle channel for
// scale-to-zero. It returns true to continue the loop (an item was admitted) and false
// to stop (idle scale-to-zero, pool teardown, or ctx cancel) — matching the obsolete
// worker.Pool.driveQueue's "return on any error".
func (w *schedulerWorker) Wait(workerCtx context.Context, idle <-chan time.Time) bool {
	w.idleCh = idle
	err := w.sched.accepted.ExecuteOne(workerCtx, w.pullFn, nil)
	return err == nil
}

// Work is a no-op: Wait's ExecuteOne already admitted the item (and handed any body to
// the executor). The execpool.Worker contract still calls it once per Wait==true.
func (w *schedulerWorker) Work(context.Context) {}

// Close releases per-worker resources. The scheduler holds none beyond the pool-owned
// idle timer, so this is currently empty.
func (w *schedulerWorker) Close(context.Context) {}

// pull is the AddWorkFunc ExecuteOne invokes when fresh+postponed are empty. The
// non-blocking probe (waiters == nil) is a no-op: incoming is an unbuffered Handoff
// with no TryPopFront. The blocking path registers on the Accepted waiters, then parks
// on incoming.PopFrontFunc composing the workWaitCh (fresh/postponed became ready), the
// scheduled deadline, execpool's idle, and pool-teardown/ctx.
func (w *schedulerWorker) pull(
	ctx context.Context,
	queueFn QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
	deadlineCh <-chan time.Time,
) (RenotifyFunc, error) {
	if waiters == nil {
		// Unbuffered Handoff: nothing to probe without blocking.
		return nil, nil
	}

	w.renotify, w.selErr, w.exit = nil, nil, false

	// Design B: workers idle-exit (scale to zero) freely; scheduled-flush deadlines are
	// honored by the queue-owned timer ([Accepted.armScheduledTimer]), which spawns a worker
	// when a deadline comes due. No per-worker idle-exit suppression. (deadlineCh is now
	// always nil — WaitForNew no longer arms a per-worker timer; the param is vestigial,
	// stripped in a follow-up.)
	idleCh := w.idleCh

	var newWork Work
	waiters.WaitFunc(confirmWaitFn,
		func(workWaitCh <-chan RenotifyFunc) RenotifyFunc {
			work, ok := w.sched.incoming.PopFrontFunc(
				func(inboxCh <-chan Work) (Work, bool) {
					return w.selectWork(ctx, inboxCh, workWaitCh, deadlineCh, idleCh)
				},
			)
			if ok {
				newWork = work
			}
			return w.renotify
		},
	)
	if newWork != nil {
		queueFn(newWork)
	}
	if w.exit && w.selErr == nil {
		// idle scale-to-zero or pool teardown: tell ExecuteOne to stop.
		w.selErr = ErrEndOfWork
	}
	return w.renotify, w.selErr
}

// selectWork is the scheduler's canonical park select. Cases that don't apply are nil
// channels (idleCh is nil while a deadline is pending). It mirrors the obsolete
// workq.Worker.selectWork minus the outbox case (incoming is an outbox-free Handoff).
func (w *schedulerWorker) selectWork(
	ctx context.Context,
	inboxCh <-chan Work,
	workWaitCh <-chan RenotifyFunc,
	deadlineCh <-chan time.Time,
	idleCh <-chan time.Time,
) (Work, bool) {
	select {
	case work := <-inboxCh:
		return work, true
	case w.renotify = <-workWaitCh:
		// notified that fresh/postponed work may be ready; return to re-probe
		return nil, false
	case <-deadlineCh:
		// a scheduled flush came due; return so ExecuteOne re-drains it
		return nil, false
	case <-idleCh:
		w.exit = true // idle scale-to-zero
		return nil, false
	case <-ctx.Done():
		// pool teardown (workerCtx.Done() == poolCtx) or cancel: stop this worker.
		w.exit = true
		return nil, false
	}
}
