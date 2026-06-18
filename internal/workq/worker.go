// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

// ─────────────────────────────────────────────────────────────────────────────
// DRAFT — first cut of the Worker driver. NOT hardened; may not compile.
// For review of the shape (see WORKING_NOTES "Worker pool + workq
// consolidation"). This is where cpWorker.popSelect + Pool.skimSelect collapse
// into one canonical select, and where Pool.block + reclaimRequest's help loop
// become a nested Worker drive.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
)

// ExecEnv is the per-worker state a Worker holds: the execution environment for
// work executing on this worker. The concrete E also serves as the ctxMeta
// executionEnvironment for work executing on this worker; that ctxMeta wiring is
// main-package, so the constructor is handed the already-wired worker ctx rather
// than building it here (keeps workq main-package-independent). If a pointer to E
// implements interface{ Release() } it is released when the worker is done (see
// worker.Pool's use).
type ExecEnv interface{}

// Worker drives a Queue, holding per-worker state E. The SAME type serves both
// driver populations — only the driving cadence differs:
//
//   - a user / skim goroutine drives one episode:   w.DriveUntilDrained(ctx)
//   - a worker.Pool goroutine drives a loop:         for { w.DriveOne(ctx) }
//
// It encapsulates the wait/notify ceremony and the ONE canonical select that
// replaces cpWorker.popSelect (8 cases) and Pool.skimSelect (7 cases).
type Worker[E ExecEnv] struct {
	q     *Queue
	state E
	ctx   context.Context //nolint:containedctx // the worker ctx (E wired into ctxMeta); work executes under it

	// Optional select policy. A zero/nil knob simply makes its select case a
	// nil channel, so one select body serves both the persistent pool worker
	// (idle + done set) and the ephemeral skim driver (both unset).
	idle time.Duration   // >0: idle-exit after this long with nothing to do
	done <-chan struct{} // pool stop / termination; nil for skim

	// onSecure, if set, fires EXACTLY ONCE the first time this worker secures a
	// work item from the queue — before the body runs. worker.Pool uses it to
	// release its spawn-concurrency slot at work-secure rather than after the
	// (possibly long/blocking) body, so a blocked body never pins the slot.
	onSecure func()
	secured  bool

	// reusable scratch (avoid per-drive alloc)
	pullFn    AddWorkFunc
	idleTimer *time.Timer

	// per-select outputs
	renotify RenotifyFunc
	exit     bool  // idle/done fired: this worker should stop
	selErr   error // ctx cancel surfaced from the select
}

// WorkerOption configures optional driving policy.
type WorkerOption func(*workerOpts)

type workerOpts struct {
	idle     time.Duration
	done     <-chan struct{}
	onSecure func()
}

// WithIdleExit makes the worker exit after d with nothing to do (pool workers
// scale to zero). Omit for skim drivers, whose lifetime the caller controls.
func WithIdleExit(d time.Duration) WorkerOption { return func(o *workerOpts) { o.idle = d } }

// WithStop wires a termination channel; when it closes the worker exits its
// current drive (worker.Pool's definitive Wait).
func WithStop(done <-chan struct{}) WorkerOption { return func(o *workerOpts) { o.done = done } }

// WithOnSecure registers a callback fired once, the first time the worker secures
// a work item (before the body runs). worker.Pool uses it to release its spawn slot
// at work-secure rather than post-body.
func WithOnSecure(fn func()) WorkerOption { return func(o *workerOpts) { o.onSecure = fn } }

// NewWorker binds a worker to q with per-worker state and the already-wired
// worker ctx (E is its ctxMeta executionEnvironment).
func NewWorker[E ExecEnv](q *Queue, state E, workerCtx context.Context, opts ...WorkerOption) *Worker[E] {
	var o workerOpts
	for _, opt := range opts {
		opt(&o)
	}
	w := &Worker[E]{q: q, state: state, ctx: workerCtx, idle: o.idle, done: o.done, onSecure: o.onSecure}
	w.pullFn = w.pull
	return w
}

// markSecured fires onSecure exactly once, when this worker first obtains work.
func (w *Worker[E]) markSecured() {
	if !w.secured {
		w.secured = true
		if w.onSecure != nil {
			w.onSecure()
		}
	}
}

// DriveOne processes at most one work item: priority fresh → postponed → pull a
// new item from the queue's incoming handoff (blocking per this worker's
// policy). Returns whether an item executed; ErrEndOfWork means the queue is
// drained and (for this worker) nothing more is coming.
func (w *Worker[E]) DriveOne(ctx context.Context) (executed bool, err error) {
	w.renotify, w.exit, w.selErr = nil, false, nil
	err = w.q.driveOne(w.execCtx(ctx), w.pullFn)
	// FIRST CUT: legacy ExecuteOne returns nil when one executed, ErrEndOfWork
	// when drained. Map to (executed, err); the native driveOne will return
	// the pair directly.
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrEndOfWork):
		return false, w.endErr()
	default:
		return false, err
	}
}

// DriveUntilDrained drives until the queue reports end-of-work or nothing more
// is ready — the skim SkimAll / CloseAndSkimAll pattern. A user goroutine calls
// this to exhaust currently-ready work, then returns to its own logic.
func (w *Worker[E]) DriveUntilDrained(ctx context.Context) error {
	for {
		executed, err := w.DriveOne(ctx)
		if err != nil {
			if errors.Is(err, ErrEndOfWork) {
				return nil
			}
			return err
		}
		if !executed {
			return nil
		}
	}
}

// pull is the addWorkFn the Queue invokes when fresh+postponed are empty: it
// pulls one item from the incoming handoff, blocking through the canonical
// select when given waiters. This is where cpWorker.AddWork + Pool.addWork /
// addWorkWhileMaybeBlocking collapse.
func (w *Worker[E]) pull(
	ctx context.Context,
	queueFn QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
	deadlineCh <-chan time.Time,
) (RenotifyFunc, error) {
	if waiters == nil {
		// Non-blocking probe (the old TryAddWorkFunc path).
		if work, ok := w.q.incoming.TryPopFront(); ok {
			w.markSecured()
			queueFn(work)
		}
		return nil, nil
	}

	w.renotify, w.selErr, w.exit = nil, nil, false
	idleCh := w.armIdle()

	// Register on the queue's work waiters before parking on the incoming
	// handoff: WaitFunc supplies workWaitCh, which fires when postponed/fresh
	// work becomes ready (a notify that didn't reach a parked worker) so the
	// canonical select can wake and re-probe the priority engine. confirmWaitFn
	// closes the race between "found nothing" and committing to the wait.
	var newWork Work
	waiters.WaitFunc(confirmWaitFn,
		func(workWaitCh <-chan RenotifyFunc) RenotifyFunc {
			work, ok := w.q.incoming.PopFrontFunc(
				func(inboxCh <-chan Work, outboxWaitCh <-chan RenotifyFunc) rdvq.PopSelectResult[Work] {
					return w.selectWork(ctx, inboxCh, outboxWaitCh, workWaitCh, deadlineCh, idleCh)
				},
			)
			if ok {
				newWork = work
			}
			return w.renotify
		},
	)
	if newWork != nil {
		w.markSecured()
		queueFn(newWork)
	}
	if w.exit && w.selErr == nil {
		// idle scale-to-zero or definitive stop: tell the driver to stop. The
		// driver population (worker.Pool loop / skim drain) interprets it.
		w.selErr = ErrEndOfWork
	}
	return w.renotify, w.selErr
}

// selectWork is the ONE canonical select, replacing cpWorker.popSelect and
// Pool.skimSelect. Cases that don't apply to a given driver are nil channels:
//   - inbox / outbox-drained / work-waiter: always (the core handoff).
//   - deadlineCh: scheduled-flush due (task/funnel only; nil otherwise).
//   - idleCh: scale-to-zero (pool worker only; nil for skim).
//   - done: definitive stop (pool worker only; nil for skim).
//   - ctx.Done: cancel.
//
// Note: the legacy skim path also folded in block-and-help (blockTimer /
// blockWait). In this design block-and-help is NOT a select case — a blocked
// body drives the help-domain Queue with a nested Worker (see Help), so those
// cases disappear here.
func (w *Worker[E]) selectWork(
	ctx context.Context,
	inboxCh <-chan Work,
	outboxWaitCh <-chan RenotifyFunc,
	workWaitCh <-chan RenotifyFunc,
	deadlineCh <-chan time.Time,
	idleCh <-chan time.Time,
) (result rdvq.PopSelectResult[Work]) {
	select {
	case work := <-inboxCh:
		result.InboxEmptied(work)
	case rf := <-outboxWaitCh:
		result.OutboxReady(rf)
	case w.renotify = <-workWaitCh:
		// notified that fresh/postponed work may be ready; return to re-probe
	case <-deadlineCh:
		// a scheduled flush came due; return so driveOne re-drains it
	case <-idleCh:
		w.exit = true // idle scale-to-zero
	case <-w.done:
		w.exit = true // definitive stop (Wait)
	case <-ctx.Done():
		w.selErr = ctx.Err()
	}
	return
}

// Help drives a different Queue (a subwave's) with a nested ephemeral Worker
// sharing this worker's exEnv, until `until` reports the blocking condition has
// cleared. This REPLACES Pool.block + reclaimRequest's help-shaped loop: instead
// of parking a permit-holding body, the body helps drain the domain it is
// waiting on. Reclaim (re-acquiring a suspended limiter permit on return) layers
// on top of this.
//
// FIRST CUT: sketch — the exact suspend/reclaim bracket and the help-domain
// selection (which Queue) are supplied during hardening.
func (w *Worker[E]) Help(helpQ *Queue, until func() bool) error {
	helper := NewWorker(helpQ, w.state, w.ctx) // shares exEnv; no idle/done (ephemeral)
	for !until() {
		executed, err := helper.DriveOne(w.ctx)
		if err != nil {
			if errors.Is(err, ErrEndOfWork) {
				break // help domain exhausted; fall back to a plain wait (TODO)
			}
			return err
		}
		_ = executed
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// execCtx returns the context work should run under. The worker ctx (with E
// wired into ctxMeta) carries the exEnv; the driving ctx (from the caller)
// carries cancellation. FIRST CUT: returns the worker ctx; the real version
// reconciles the two (the work itself borrows its wave's exec ctx — per-wave
// cancellation — while still reaching this exEnv via ctxMeta).
func (w *Worker[E]) execCtx(driveCtx context.Context) context.Context {
	_ = driveCtx
	return w.ctx
}

// armIdle prepares the idle-exit timer channel, or nil when idle exit is off.
func (w *Worker[E]) armIdle() <-chan time.Time {
	if w.idle <= 0 {
		return nil
	}
	if w.idleTimer == nil {
		w.idleTimer = timerp.Get()
	}
	timerp.Reset(w.idleTimer, w.idle)
	return w.idleTimer.C
}

// endErr maps a drained-queue outcome to either ErrEndOfWork (the worker should
// stop) or nil (just nothing ready right now), depending on policy. FIRST CUT:
// always surface ErrEndOfWork; the persistent pool worker decides whether that
// means idle-exit vs keep-looping in its own loop.
func (w *Worker[E]) endErr() error { return ErrEndOfWork }

// Release returns the worker's per-worker resources when it is done (timer; and
// E's own Release if it has one). Called by the driver population on exit.
func (w *Worker[E]) Release() {
	if w.idleTimer != nil {
		timerp.Put(w.idleTimer)
		w.idleTimer = nil
	}
	if r, ok := any(&w.state).(interface{ Release() }); ok {
		r.Release()
	}
}
