// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/cerr"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/petenewcomb/streampool/internal/timerp"
	"github.com/petenewcomb/streampool/internal/wavestate"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Wave is the unit that admits, drains, and cancels a batch of scatter-gather
// work together. It owns the batch lifecycle — wavestate (Open→Done), the
// admission governor, the skim queue — and dispatches op bodies onto the global
// worker pool (defaultPool). A zero-value Wave is ready to use (no constructor);
// it self-inits on first use and owns no context. Bind ops to it with op.In(&w)
// at top level, or dispatch from inside a body (the ambient wave). It is reusable
// after a drain (see [Wave.ensureArmed]).
//
//nolint:contextcheck // background context used only for tracing
type Wave struct {
	// initialized is set once the substrate (below) has been brought up for the
	// current cycle; initMu guards (re)initialization. A zero-value Wave is usable:
	// ensureInit lazily brings up the substrate on first ctx-bearing use and
	// re-arms it after a drain (see ensureInit). The Wave owns NO context — it is
	// driver-specific; cancellation rides the caller's ctx by ancestry.
	initialized atomic.Bool
	initMu      sync.Mutex

	state wavestate.WaveState

	skimQueue workq.Pending

	// If there are tasks waiting to post work to skimQueue, the governor
	// will block new top-level scatters, thereby applying backpressure to
	// regulate the system.
	governor workq.Governor

	workQueue workq.Accepted

	protoBB      workq.BlockBehavior  // avoid closure reallocation
	blockFn      workq.BlockFunc      // avoid closure reallocation
	tryAddWorkFn workq.TryAddWorkFunc // avoid closure reallocation
	addWorkFn    workq.AddWorkFunc    // avoid closure reallocation

	// fEngine is the lazily-created funnel engine (nil until the first NewFunnel).
	// Body contexts are not per-wave: task/funnel bodies borrow them from ctxpool
	// keyed on the submit ctx (see bodyctx.go), so the wave owns no context —
	// cancellation rides the submit ctx by ancestry.
	fEngineMu sync.Mutex
	fEngine   atomic.Pointer[funnelEngine]
}

//nolint:contextcheck // background context for tracing; submitCtx is the body-ctx borrow source
func (j *Wave) newTaskWork(
	submitCtx context.Context, group workq.GroupID, task boundTask, req request, wave *Wave,
) *taskWork {
	traceRegion := "Wave.newTaskWork"

	w := taskWorkPool.Get()
	w.Init(group, j)
	w.job = j
	w.task = task
	w.req = req
	if req != nil {
		// Stored once so the per-execution completion callback doesn't
		// allocate a fresh method value.
		w.completedFn = req.release
	}
	w.wave = wave
	// Borrow the body context at dispatch (descended from the submit ctx, so
	// cancellation rides ancestry). Async worker bodies are fresh permit-roots
	// (parent nil); the worker's E is stamped at Execute, not known yet here.
	w.bodyCtx, w.bodyMeta = borrowBodyContext(submitCtx, wave, taskContext, nil, req, nil)

	trace.Logf(context.Background(), traceRegion, "Wave=%p created %v", j, w)
	return w
}

type taskWork struct {
	poolWork
	// job is stored so Free() satisfies workq.Work (no-arg); taskWork is now a
	// workq.Work run by the global pool's workers.
	job  *Wave
	task boundTask
	// req is the Limiter request handle this task's admission was granted
	// through; nil for unlimited ops. The taskWork owns the handle's
	// lifecycle: stamped on the worker's ctxMeta during Execute, released
	// at body completion (completedFn) or in Free (idempotent), recycled
	// in Free.
	req         request
	completedFn func() // req.release, captured once at creation
	// wave is the dispatching Wave; stamped onto the body
	// ctxMeta so nil-wave op dispatches from the task body can resolve it.
	wave *Wave
	// bodyCtx is the body context borrowed at dispatch (borrowBodyContext),
	// descended from the submit ctx and carrying bodyMeta; the task body runs
	// under it and Free returns it. bodyMeta is the same meta the ctx carries,
	// kept so Execute can stamp the worker's E without a ctx.Value lookup.
	bodyCtx  context.Context //nolint:containedctx // the borrowed body ctx, released in Free
	bodyMeta *ctxMeta
}

func (w *taskWork) Reset() {
	w.poolWork = poolWork{}
	w.job = nil
	w.task = nil
	w.req = nil
	w.completedFn = nil
	w.wave = nil
	w.bodyCtx = nil
	w.bodyMeta = nil
}

// Execute is the workq.Work entry run by a global-pool worker. The body context
// (bodyCtx) was borrowed at dispatch; this stamps the running worker's E onto the
// body meta (the only piece not known at dispatch) and runs the task body under
// bodyCtx. The held limiter request was stamped at borrow. Free returns the body
// context.
func (w *taskWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	ex.Starting()
	w.bodyMeta.executionEnvironment = workerEnvFromContext(ctx)
	//nolint:contextcheck // the body runs under the borrowed body ctx by design
	w.task.Execute(w.bodyCtx, w.Group(), w.completedFn)
	return nil
}

//nolint:contextcheck // background context used only for tracing
func (w *taskWork) Free() {
	traceRegion := "taskWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	job := w.job
	w.task.Free()
	if w.req != nil {
		// Normal completion already released (completedFn); release here
		// is the idempotent backstop for tasks freed without executing
		// (dispatch failure, cancellation drain) — by-state: abandon a
		// PENDING request, discard a POSTPONED one, give back a HELD one.
		w.req.release()
		freeRequest(w.req)
		w.req = nil
	}
	// Return the body context borrowed at dispatch — its child ctx to ctxpool and
	// its meta to bodyMetaPool. Runs whether or not the body executed (a task freed
	// without running still borrowed at dispatch).
	if w.bodyCtx != nil {
		releaseBodyContext(w.bodyCtx)
		w.bodyCtx = nil
		w.bodyMeta = nil
	}
	w.Close(job)
	taskWorkPool.Put(w)
}

var taskWorkPool = omnipool.For[taskWork]()

// ensureInit lazily brings up the Wave's lifecycle substrate (state, queues,
// governor, closure fields) the FIRST time a zero-value Wave is used, so it is
// usable with no constructor. It is idempotent and does NOT re-arm a drained wave —
// re-arming is [Wave.ensureArmed], reached only from dispatch entries. This split is
// essential: a drain (Skim/SkimAll/Close) routes through here too, and a CloseAndSkimAll
// drives an empty wave to Done during Close() before SkimAll runs — if init re-armed
// on Done, that skim would reset the wave to Open and block forever instead of
// observing Done. Skimming a Done wave must return ErrWaveDone, not re-arm.
//
// A zero-value Wave's state reads as Open (stageOpen == 0) but with nil channels, so
// init keys off the explicit initialized flag, not the stage. The common case (an
// already-initialized wave) is a single atomic load.
func (w *Wave) ensureInit() {
	if w.initialized.Load() {
		return
	}
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.initialized.Load() {
		return
	}
	w.initState()
	w.initialized.Store(true)
}

// initState (re)initializes the substrate to a fresh Open cycle. Caller holds initMu.
func (w *Wave) initState() {
	w.state.Init()
	w.skimQueue.Init()
	w.governor.Init()
	w.workQueue.Init(nil)
	w.protoBB.ShouldBlock = w.shouldBlock
	w.blockFn = w.block
	w.tryAddWorkFn = w.tryAddWork
	w.addWorkFn = w.addWork
}

// ensureArmed brings the Wave up for NEW work: it first-time-inits (ensureInit) and,
// if the wave's prior cycle has drained to Done, re-arms it to a fresh Open cycle. It
// is called only from dispatch entries (op Start/Submit, funnelEngine) — never from a
// skim/drain — so that reusing a drained Wave by dispatching into it (e.g. a *Wave
// pooled via sync.Pool for allocation-free sub-waves) starts a new cycle, while
// skimming a drained wave still observes Done.
//
// The prior cycle's flusher is joined before re-Init: a Done stage means every
// work/funnel-instance reference drained, so the flusher's done-watcher has fired and
// the goroutine is exiting — the join is bounded and acts as a barrier ensuring nothing
// reads the old WaveState while we overwrite it. Reuse is sequential (a new cycle
// begins after the prior drain returns); a dispatch racing a concurrent self-drain
// stays a misuse guarded by panicIfDone.
func (w *Wave) ensureArmed() {
	w.ensureInit()
	if !w.state.IsDone() {
		return // fast path: live (or freshly inited)
	}
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if !w.state.IsDone() {
		return // another dispatch re-armed it
	}
	if fe := w.fEngine.Swap(nil); fe != nil {
		fe.joinFlusher() // barrier: prior flusher fully exited before re-Init
	}
	w.initState()
}

// Skim processes outstanding task results and then waits for the next
// task result from a task previously launched via [Start]. It will block until
// a completed task is available or the provided context or job is canceled.
// If the job is closed and no tasks remain in flight, it will return immediately.
// See [Wave.TrySkim] for a non-blocking alternative.
//
// Returns an error if one occurred:
//
//   - nil: a task completed and was successfully skimmed
//   - ErrWaveDone: the job is done and therefore nothing is left to skim
//   - other error: a task's skim function returned a non-nil error, or the
//     argument or job-internal context was canceled
//
// If a skim function returns an error, the job continues running and you can
// keep calling Skim to process more tasks (and errors, if any) until you
// receive ErrWaveDone.
//
// If all skim functions are thread-safe, then Skim is thread-safe and
// may be called concurrently from multiple goroutines. Blocking and
// non-blocking calls may also be mixed, as can calls to any of the other skim
// methods.
//
// NOTE: If a task result is skimmed, this method will call the task's
// [Skim] and wait until it returns.
func (j *Wave) Skim(ctx context.Context) error {
	traceRegion := "Wave.Skim"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetSkim(ctx)
	// A blocking gather from inside a skim handler would monopolize the
	// sole serial skim driver and deadlock; redirect to a funnel/task.
	meta.vetNotNestedInSkim()
	// Suspend-class episode: a body driving this skim parks here while
	// holding its limiter permit; give the slot back for the duration
	// and reclaim (help-shaped) on return.
	if r := suspendForEpisode(meta); r != nil {
		defer reclaimRequest(ctx, j.blockFn, r)
	}
	_, err := j.skim(ctx, meta)
	return err
}

func (j *Wave) tryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	traceRegion := "Wave.tryAddWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", j)
	if workFn, ok := j.skimQueue.TryPopFront(); ok {
		queueFn(workFn)
	}
	return nil
}

func (j *Wave) vetSkim(ctx context.Context) (context.Context, *ctxMeta) {
	return j.skimCtxMeta(ctx)
}

func (j *Wave) trySkim(ctx context.Context, _ *ctxMeta) (bool, error) {
	return j.workQueue.TryExecuteOne(ctx, j.tryAddWorkFn)
}

func (j *Wave) skim(ctx context.Context, meta *ctxMeta) (bool, error) {
	return true, j.workQueue.ExecuteOne(ctx, j.addWorkFn, nil)
}

// This function is designed to be called before scattering a new task to
// preemptively skim or skim results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task
// pools.
func (j *Wave) yield(ctx context.Context, deadline time.Time) error {
	traceRegion := "Wave.yield"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetSkim(ctx)
	for {
		ok, err := j.trySkim(ctx, meta)
		if err != nil {
			return err
		}
		// Test for deadline passing only after trying at least one skim
		if !ok || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			break
		}
	}
	return nil
}

const errBlockWaitSignaled = cerr.Error("block wait signaled")

func (j *Wave) shouldBlock(ctx context.Context) workq.BlockFunc {
	// Read the meta directly (not via j.ctxMeta, which panics on a missing meta).
	// A dispatch chain (launcherScatterWork governor gate / limiterScatterWork
	// acquire) that postponed onto the global shared queue is re-run by a global
	// worker whose ctx carries NO ctxMeta — and such work is never a TOP-LEVEL
	// dispatch (top-level runs on the user goroutine, where the meta is present).
	// So "no matching meta → not top-level → don't block" is correct.
	meta, ok := metaFromContext(ctx)
	if ok && meta.job == j && meta.IsTopLevel() {
		return j.blockFn
	}
	return nil
}

func (j *Wave) block(
	ctx context.Context,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	traceRegion := "Wave.block"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", j)
	ctx, meta := j.vetSkim(ctx)
	// Suspend-class episode: the block-and-help wait both parks and
	// synchronously runs other framework-gated work. Bracketing here is
	// correctness-required, not just utilization — without it, a
	// subjob-top-level dispatch acquiring a limiter held by this same
	// goroutine's enclosing body self-deadlocks (see "Where suspend
	// fires" in docs/limiter-suspend-resume.md). Re-entrant: the
	// reclaim's own block-and-help finds the handle already SUSPENDED
	// and no-ops.
	if r := suspendForEpisode(meta); r != nil {
		defer reclaimRequest(ctx, j.blockFn, r)
	}
	adder := blockingWorkAdderPool.Get()
	defer blockingWorkAdderPool.Put(adder)
	adder.job = j
	adder.meta = meta
	adder.blockDeadline = blockDeadline
	adder.blockWaiters = blockWaiters
	adder.confirmBlockWaitFn = confirmBlockWaitFn

	err := j.workQueue.ExecuteOne(ctx, adder.addWorkFn, nil)
	if errors.Is(err, errBlockWaitSignaled) {
		err = nil
	}
	return adder.blockWaitRenotifyFn, err
}

var blockingWorkAdderPool = omnipool.For[blockingWorkAdder]()

type blockingWorkAdder struct {
	job                 *Wave
	meta                *ctxMeta
	blockDeadline       time.Time
	blockWaiters        *workq.Waiters
	confirmBlockWaitFn  func() bool
	blockWaitRenotifyFn workq.RenotifyFunc

	addWorkFn workq.AddWorkFunc
}

func (a *blockingWorkAdder) Init() {
	a.addWorkFn = a.addWork
}

func (a *blockingWorkAdder) Reset() {
	*a = blockingWorkAdder{
		addWorkFn: a.addWorkFn,
	}
}

func (a *blockingWorkAdder) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	_ <-chan time.Time, // skim queue has no scheduled work
) (workq.RenotifyFunc, error) {
	var workReadyRenotifyFn workq.RenotifyFunc
	var err error
	workReadyRenotifyFn, a.blockWaitRenotifyFn, err = a.job.addWorkWhileMaybeBlocking(
		ctx, a.meta, queueFn, workWaiters, confirmWorkWaitFn, a.blockDeadline, a.blockWaiters, a.confirmBlockWaitFn)
	return workReadyRenotifyFn, err
}

func (j *Wave) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
	_ <-chan time.Time, // skim queue has no scheduled work
) (workq.RenotifyFunc, error) {
	traceRegion := "Wave.addWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", j)
	ctx, meta := j.ctxMeta(ctx)
	workReadyRenotifyFn, _, err := j.addWorkWhileMaybeBlocking(ctx, meta, queueFn, waiters,
		confirmWaitFn, time.Time{}, nil, nil)
	return workReadyRenotifyFn, err
}

func (j *Wave) addWorkWhileMaybeBlocking(
	ctx context.Context,
	meta *ctxMeta,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) (workReadyRenotifyFn, blockWaitRenotifyFn workq.RenotifyFunc, err error) {
	meta.PushQueueFunc(queueFn)
	defer meta.PopQueueFunc()

	var workRf, blockRf rdvq.RenotifyFunc
	if workWaiters == nil {
		err = j.tryAddWork(ctx, queueFn)
	} else {
		work, ok := j.skimQueue.PopFrontFunc(
			func(inboxCh <-chan workq.Work, outboxWaitCh <-chan rdvq.RenotifyFunc) rdvq.PopSelectResult[workq.Work] {
				// Declared per invocation: skimSelect (which is the only thing
				// that populates this) is skipped on any iteration where the
				// block confirm short-circuits — i.e. once the permit is
				// acquired/reclaimed. A value hoisted across iterations would
				// retain a stale outbox-ready result, keeping PopFrontFunc's
				// loop from ever reaching its empty (renotifyFn==nil) exit.
				var psResult rdvq.PopSelectResult[workq.Work]
				workRf = workWaiters.WaitFunc(
					confirmWorkWaitFn,
					func(workWaitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
						var innerWorkRf rdvq.RenotifyFunc
						if blockWaiters == nil {
							psResult, innerWorkRf, _, err = j.skimSelect(
								ctx, inboxCh, outboxWaitCh, workWaitCh, nil, nil,
							)
						} else {
							blockRf = blockWaiters.WaitFunc(
								func() bool {
									shouldWait := confirmBlockWaitFn()
									if !shouldWait {
										err = errBlockWaitSignaled
									}
									return shouldWait
								},
								func(blockWaitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
									var blockTimerCh <-chan time.Time
									// Zero or Forever deadline: no timer (block until ctx
									// cancel or notification). Non-zero, non-Forever: set
									// up a timer; if it fires we'll return up through
									// errBlockWaitSignaled.
									if !blockDeadline.IsZero() && !isForever(blockDeadline) {
										blockTimer := timerp.Get()
										defer timerp.Put(blockTimer)
										timerp.Reset(blockTimer, max(0, time.Until(blockDeadline)))
										blockTimerCh = blockTimer.C
									}
									var innerBlockRf rdvq.RenotifyFunc
									psResult, innerWorkRf, innerBlockRf, err = j.skimSelect(
										ctx, inboxCh, outboxWaitCh, workWaitCh, blockTimerCh, blockWaitCh,
									)
									return innerBlockRf
								},
							)
						}
						return innerWorkRf
					},
				)
				return psResult
			},
		)
		if ok {
			queueFn(work)
		}
	}
	return workRf, blockRf, err
}

func (j *Wave) skimSelect(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxWaitCh <-chan rdvq.RenotifyFunc,
	workWaitCh <-chan rdvq.RenotifyFunc,
	blockTimerCh <-chan time.Time,
	blockWaitCh <-chan rdvq.RenotifyFunc,
) (psResult rdvq.PopSelectResult[workq.Work], workRf, blockRf rdvq.RenotifyFunc, err error) {
	traceRegion := "Wave.skimSelect"
	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxWaitCh=%p, workWaitCh=%p, blockWaitCh=%p",
		inboxCh, outboxWaitCh, workWaitCh, blockWaitCh)
	select {
	case work := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", inboxCh)
		psResult.InboxEmptied(work)
	case rf := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitCh=%p", outboxWaitCh)
		psResult.OutboxReady(rf)
	case workRf = <-workWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitCh=%p", workWaitCh)
	case <-blockTimerCh:
		trace.Logf(ctx, traceRegion, "received block deadline timer signal")
		err = errBlockWaitSignaled
	case blockRf = <-blockWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from blockWaitCh=%p", blockWaitCh)
		err = errBlockWaitSignaled
	case <-j.state.Done():
		trace.Logf(ctx, traceRegion, "received job done signal")
		err = ErrWaveDone
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		err = ctx.Err()
	}
	return
}

type skimPostWork struct {
	poolWork
	job  *Wave
	work boundSkimWork
	// shouldBlock is captured at dispatch (from the dispatching meta) rather than
	// re-derived from the run ctx: when this producer postpones onto the global
	// shared queue, a global worker re-runs it under the worker ctx, which carries
	// no ctxMeta — so ctxMeta(ctx) would panic ("Context not associated with a
	// job"). The post only needs ShouldBlock + the ctx for cancellation.
	shouldBlock bool
}

func (w *skimPostWork) Init(group workq.GroupID, job *Wave, work boundSkimWork, shouldBlock bool) {
	w.poolWork.Init(group, job)
	w.job = job
	w.work = work
	w.shouldBlock = shouldBlock
}

func (w *skimPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "skimPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	posted, err := func() (bool, error) {
		waiting := func() {
			// Call Waiting on the nested skimWork to notify the governor
			w.work.Waiting(&w.job.governor)
		}

		tryPost := func() bool {
			// Try non-blocking post - can be retried if it fails
			return w.job.skimQueue.TryPushBack(w.work, nil)
		}

		for {
			if tryPost() {
				return true, nil
			}

			if !ex.ShouldBlockOrPostpone() {
				return false, nil
			}

			if !w.shouldBlock {
				// We expect to be queued and called again, so listen and don't block
				ex.AddToListeners(w.job.skimQueue.ListenersFor())

				// Check again after registering for notification, but return
				// and expect to be called again if needed
				posted := tryPost()
				if !posted {
					waiting()
				}
				trace.Logf(ctx, traceRegion, "meta.QueueFunc() != nil, posted=%v", posted)
				return posted, nil
			}

			// Use blocking post
			posted := true
			var err error
			w.job.skimQueue.PushBackFunc(w.work, nil, func(outboxCh chan<- workq.Work) bool {
				posted = false

				// Slow path, really going to block now
				ex.Blocking()

				waiting()

				var sent bool
				sent, err = rdvq.BasicPushSelect[workq.Work](ctx, outboxCh, w.work)
				if sent {
					posted = true
				}
				return sent
			})
			trace.Logf(ctx, traceRegion, "meta.ShouldBlock(), posted=%v, err=%v", posted, err)
			if posted || err != nil {
				return posted, err
			}
		}
	}()

	if posted {
		ex.Starting() // Signal success only if we actually posted
		w.work = nil  // Clear the work reference since it's now owned by the queue
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (w *skimPostWork) Free() {
	traceRegion := "skimPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	// Free the nested work item if we still own it
	if w.work != nil {
		trace.Logf(context.Background(), traceRegion, "w.work.Free()")
		w.work.Free()
		w.work = nil
	}

	w.Close(w.job)
	skimPostWorkPool.Put(w)
}

var skimPostWorkPool = omnipool.For[skimPostWork]()

//nolint:contextcheck // background context used only for tracing
func (j *Wave) newSkimPostWork(group workq.GroupID, skimWork boundSkimWork, shouldBlock bool) *skimPostWork {
	traceRegion := "Wave.newSkimPostWork"

	w := skimPostWorkPool.Get()
	w.Init(group, j, skimWork, shouldBlock)

	trace.Logf(context.Background(), traceRegion, "Wave=%p created %v", j, w)
	return w
}

// TrySkim processes outstanding task results and then attempts to process
// the next task result from a task previously launched via [Start]. Unlike
// [Wave.Skim], it will not block if a completed task is not immediately available.
//
// Returns a boolean flag indicating whether there might be more task results
// immediately available to process and an error if one occurred.
//
// The error indicates:
//   - nil: no skim function returned an error
//   - ErrWaveDone: the job is done and no more tasks will ever be available
//   - other error: a skim function returned an error or the context was canceled
//
// If a skim function returns an error, the job continues running and you can
// keep calling TrySkim to process more tasks (and errors, if any) until you
// receive ErrWaveDone.
//
// See Skim for additional details.
func (j *Wave) TrySkim(ctx context.Context) (bool, error) {
	traceRegion := "Wave.TrySkim"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetSkim(ctx)
	return j.trySkim(ctx, meta)
}

// SkimAll processes task results until the job completes or an error occurs.
// If the job has not been closed, SkimAll will block indefinitely, as new
// tasks might be added at any time. It will return an error if the provided context
// or job is canceled. After the job is closed, SkimAll will continue processing
// tasks until all work completes (including tasks spawned during result processing)
// and then return.
//
// Returns nil when the job is done, or an error if the context is canceled or a
// task's [Skim] returns a non-nil error. If a skim function returns an
// error, you can call SkimAll again to continue processing more tasks (and
// errors, if any) until the job is done (i.e., SkimAll returns nil).
//
// If all skim functions are thread-safe, then SkimAll is thread-safe and
// can be called concurrently from multiple goroutines. In this case they will
// collectively process all results, with each call handling a subset. Blocking
// and non-blocking calls may also be mixed, as can calls to any of the other
// skim methods.
//
// NOTE: This method will serially call each skimmed task's [Skim] and
// wait until it returns.
func (j *Wave) SkimAll(ctx context.Context) error {
	traceRegion := "Wave.SkimAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Suspend-class episode (one per SkimAll, not per inner skim): a
	// body driving this drain — e.g. a subwave's CloseAndSkimAll —
	// parks here while holding its limiter permit; give the slot back
	// for the whole drain and reclaim (help-shaped) on return.
	ctx, meta := j.vetSkim(ctx)
	// A blocking gather from inside a skim handler would monopolize the
	// sole serial skim driver and deadlock; redirect to a funnel/task.
	meta.vetNotNestedInSkim()
	if r := suspendForEpisode(meta); r != nil {
		defer reclaimRequest(ctx, j.blockFn, r)
	}

	err := j.skimAll(ctx, j.skim)
	if errors.Is(err, ErrWaveDone) {
		return nil
	}
	return err
}

// TrySkimAll processes all currently available task results without blocking.
// Unlike [Wave.SkimAll], TrySkimAll will return immediately if there are no
// completed tasks ready to process, regardless of whether the job is closed or
// whether there are still tasks in flight.
//
// Returns nil when all immediately available tasks have been processed, ErrWaveDone
// when the job is done, or an error if the context is canceled or a task's
// [Skim] returns a non-nil error. If a skim function returns an error,
// you can call TrySkimAll again to continue processing more tasks (and errors,
// if any) until you receive ErrWaveDone.
//
// See SkimAll for information about thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [Skim] and wait until it finishes processing.
func (j *Wave) TrySkimAll(ctx context.Context) error {
	traceRegion := "Wave.TrySkimAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	return j.skimAll(ctx, j.trySkim)
}

func (j *Wave) skimAll(ctx context.Context, skimFn func(context.Context, *ctxMeta) (bool, error)) error {
	ctx, meta := j.vetSkim(ctx)
	for {
		ok, err := skimFn(ctx, meta)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

type taskPostWork struct {
	poolWork
	job  *Wave
	task *taskWork
}

func (w *taskPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "%v", w)
	}

	// Handoff to the GLOBAL pool's shared work queue: the unified Queue.Post
	// replaces the legacy taskQueue try/listen/block loop, and the pool's
	// unmet-demand signal (trySpawnWorker) replaces registerDemand/
	// trySpawnTaskWorker. Spawn/governor are handled elsewhere (governor at
	// launcherScatterWork, spawn at defaultPool). onWait is nil for task (no
	// downstream governor registration here).
	// shouldBlock is read directly from the ctx (not via j.ctxMeta, which panics on
	// a missing meta): a producer only postpones onto the global queue in LISTEN
	// mode (shouldBlock=false), and a global worker re-running it has no ctxMeta —
	// so "no matching meta → shouldBlock=false" matches the original dispatch.
	meta, _ := metaFromContext(ctx)
	shouldBlock := meta != nil && meta.job == w.job && meta.ShouldBlock()
	posted, err := defaultPool.Post(ctx, ex, shouldBlock, w.task, nil)
	if posted {
		// Ownership transferred to the queue; the worker will run + Free it.
		w.task = nil
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (w *taskPostWork) Free() {
	traceRegion := "taskPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	// Free the nested task work if we still own it
	if w.task != nil {
		trace.Logf(context.Background(), traceRegion, "w.task.Free()")
		w.task.Free()
	}

	w.Close(w.job)
	taskPostWorkPool.Put(w)
}

var taskPostWorkPool = omnipool.For[taskPostWork]()

type poolWork struct {
	workq.WorkItem
}

func (w *poolWork) Init(group workq.GroupID, job *Wave) {
	w.WorkItem.Init(group)
	trace.Logf(context.Background(), "poolWork.Init", "%v", &w.WorkItem)
	job.state.IncrementWork()
}

//nolint:contextcheck // background context used only for tracing
func (w *poolWork) Close(job *Wave) {
	if w.ID() == 0 {
		// This check and panic is best-effort only as it may also be a race if
		// Close() is called from multiple goroutines -- which it should not be.
		panic("already closed")
	}
	trace.Logf(context.Background(), "poolWork.Close", "%v", &w.WorkItem)
	job.state.DecrementWork()
}

func (j *Wave) newTaskPostWork(group workq.GroupID, deadline time.Time, task *taskWork) workq.Work {
	w := taskPostWorkPool.Get()
	w.Init(group, j)
	w.job = j
	w.task = task
	return w
}

// panicIfDone panics if the job is in the done state
func (j *Wave) panicIfDone() {
	j.state.PanicIfDone()
}

// Close changes the job's state from open to closed, which allows it to eventually
// progress to the done state once all tasks complete. When a job is closed,
// [Wave.SkimAll] will return after processing all existing tasks and any tasks
// they spawn, rather than blocking indefinitely.
//
// After a job is closed and all tasks have completed, launching new tasks will panic.
// Skimming operations will continue to work normally but will always return
// immediately with no results.
//
// Note that tasks can still be added after Close is called but before all tasks
// have completed.
//
// Close may be called from any goroutine and may safely be called more than once.
//
//nolint:contextcheck // background context used only for tracing
func (j *Wave) Close() {
	traceRegion := "Wave.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	j.ensureInit()
	j.state.Close()
}

// CloseAndSkimAll closes the job via [Wave.Close] and then waits for and
// skims the results of all in-flight tasks via [Wave.SkimAll].
func (j *Wave) CloseAndSkimAll(ctx context.Context) error {
	traceRegion := "Wave.CloseAndSkimAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	j.Close()
	return j.SkimAll(ctx)
}
