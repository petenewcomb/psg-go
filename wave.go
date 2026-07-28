// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/cerr"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/petenewcomb/streampool/internal/timerp"
	"github.com/petenewcomb/streampool/internal/wavestate"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Wave is the unit that admits, drains, and cancels a batch of scatter-gather work
// together. It is a small, copyable handle to a pooled, reference-managed substrate
// ([waveImpl]); construct one with [NewWave]. Bind ops to it with op.In(wave) at top
// level, or dispatch from inside a body (the ambient wave). Done/Close are terminal —
// there is no re-arm; the next batch is a fresh [NewWave]. Copies share the same
// underlying wave. It owns NO context — cancellation rides the caller's ctx by
// ancestry.
type Wave struct {
	// h is a weak (referenceless) handle to the substrate: it captures the impl
	// pointer plus the generation, so a Get after the wave has drained-and-recycled
	// fails cleanly rather than misidentifying a reused incarnation. The owner
	// reference (refs=1) lives on the impl from NewWave until Close, keeping the impl
	// alive across the open-but-idle window.
	h omnipool.Handle[*waveImpl]
}

// wavePool is the shared pool of reference-managed wave substrates. Get hands out an
// impl with a single (owner) reference and a warm, one-time-Init'd substrate; Release
// recycles it (running Reset) only when the last reference is dropped.
var wavePool = omnipool.For[waveImpl]()

// NewWave constructs a fresh, open Wave, ready for ops to dispatch into. Release it by
// draining ([Wave.SkimAll] / [Wave.CloseAndSkimAll]) or by [Wave.Close].
func NewWave() Wave {
	return Wave{h: omnipool.NewHandle(wavePool.Get())}
}

// Skim skims one result. A wave that has already drained has nothing left to skim, so
// Skim returns [ErrWaveDone].
func (w Wave) Skim(ctx context.Context) error {
	impl, ok := w.h.Get()
	if !ok {
		return ErrWaveDone
	}
	defer wavePool.Release(impl)
	return impl.Skim(ctx)
}

// TrySkim skims one immediately-available result without blocking.
func (w Wave) TrySkim(ctx context.Context) (bool, error) {
	impl, ok := w.h.Get()
	if !ok {
		return false, ErrWaveDone
	}
	defer wavePool.Release(impl)
	return impl.TrySkim(ctx)
}

// SkimAll drains the wave, skimming results until it completes.
func (w Wave) SkimAll(ctx context.Context) error {
	impl, ok := w.h.Get()
	if !ok {
		return nil // already drained and recycled
	}
	defer wavePool.Release(impl)
	return impl.SkimAll(ctx)
}

// TrySkimAll skims all immediately-available results without blocking.
func (w Wave) TrySkimAll(ctx context.Context) error {
	impl, ok := w.h.Get()
	if !ok {
		return ErrWaveDone
	}
	defer wavePool.Release(impl)
	return impl.TrySkimAll(ctx)
}

// Close closes the wave and drops the owner reference. The drop happens exactly once —
// only on the goroutine that wins the Open→Closed transition — so the "Close may be
// called more than once" contract holds and the owner reference is never
// double-released.
func (w Wave) Close() {
	impl, ok := w.h.Get()
	if !ok {
		return // already closed and recycled
	}
	defer wavePool.Release(impl)
	if impl.Close() {
		wavePool.Release(impl) // drop the owner reference
	}
}

// CloseAndSkimAll closes the wave via [Wave.Close] and then drains it via [Wave.SkimAll],
// resolving the substrate once.
func (w Wave) CloseAndSkimAll(ctx context.Context) error {
	impl, ok := w.h.Get()
	if !ok {
		return nil // already drained and recycled
	}
	defer wavePool.Release(impl)
	if impl.Close() {
		wavePool.Release(impl) // drop the owner reference
	}
	return impl.SkimAll(ctx)
}

// waveImpl is the pooled substrate behind a [Wave]. It owns the batch lifecycle —
// wavestate (Open→Done), the admission governor, the skim queue — and dispatches op
// bodies onto the global worker pool (defaultPool). It is reference-managed
// (embeds [omnipool.GenRefCounter] — the generation-guarded counter, because the user's
// Wave and cross-lifetime holders capture it as a weak [omnipool.Handle]): framework
// sub-waves draw a warm impl from the pool and it is recycled only when the last reference
// drops, deferring reuse past any straggler. [waveImpl.Init] brings up the warm queues once
// per physical allocation; [waveImpl.Reset] re-opens a recycled impl to a fresh cycle
// without re-Init'ing them. It owns NO context — cancellation rides the caller's ctx.
//
//nolint:contextcheck // background context used only for tracing
type waveImpl struct {
	// GenRefCounter is the object-lifetime counter (packed generation + refs). It MUST NOT
	// be copied, and Reset MUST NOT touch it — the generation must survive recycling.
	omnipool.GenRefCounter

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

	// caches holds this wave's per-Limiter permit caches (C_W^L), one per distinct
	// permits.Pool used by ops dispatched into this wave — plus any held=0 pass-through
	// nodes a descendant wave's mkdir-p created here (see wavepermits.go). Each entry
	// holds this wave's self-ref on that cache, dropped at wave-Done (releaseCaches);
	// descendant NewChild refs keep a node alive past Done until its subtree drains.
	// Guarded by cachesMu; lazily allocated; nil between Done and the next re-arm.
	caches   map[*permits.Pool]*permits.Cache
	cachesMu sync.Mutex

	// funnelInstances holds this wave's per-funnel accumulator-instance caches, keyed
	// by funnel id (funnelID → *funnelInstanceQueue[T], stored as the funnelSweep
	// interface for the heterogeneous-T sweep). Funnels are plain values, and the
	// wave owns their live instances. The end-of-work
	// sweep (sweepFunnels, wired as wavestate's onFlushing callback) ranges this map.
	// Body contexts are not per-wave: task/funnel bodies borrow them from ctxpool keyed
	// on the submit ctx (see bodyctx.go), so the wave owns no context — cancellation
	// rides the submit ctx by ancestry.
	funnelInstances sync.Map
}

// boundTask is the internal interface every task work item satisfies.
// Launcher constructs boundTask values and feeds them through
// [Wave.newTaskWork] for execution on a worker.
type boundTask interface {
	Execute(ctx context.Context, group workq.GroupID, completedFn func())
	Free()
}

//nolint:contextcheck // background context for tracing; submitCtx is the body-ctx borrow source
func (wv *waveImpl) newTaskWork(
	submitCtx context.Context, group workq.GroupID, task boundTask, h *heldPermit,
) *taskWork {
	traceRegion := "Wave.newTaskWork"

	wk := taskWorkPool.Get()
	wk.Init(group, wv)
	wk.task = task
	wk.h = h
	if h != nil {
		// Reuse the handle's bound release method value (lazily bound once per pooled
		// handle, preserved across Reset) so a limited dispatch does not allocate a fresh
		// method-value closure here every time.
		if h.releaseFn == nil {
			h.releaseFn = h.release
		}
		wk.completedFn = h.releaseFn
	}
	wk.wave = wv
	// Borrow the body context at dispatch (descended from the submit ctx, so
	// cancellation rides ancestry), resolving the source meta here on the
	// dispatcher's goroutine where it is provably alive. The borrow pins it as
	// parent but stays a permitRoot for synchronous-extent walks; the worker's
	// E is stamped at Execute, not known yet here.
	srcMeta, _ := metaFromContext(submitCtx)
	wk.bodyCtx, wk.bodyMeta = borrowBodyContext(submitCtx, srcMeta, wv, taskContext, h, nil)

	trace.Logf(context.Background(), traceRegion, "Wave=%p created %v", wv, wk)
	return wk
}

type taskWork struct {
	poolWork
	task boundTask
	// h is the native limiter handle this task's admission runs through; nil for
	// unlimited ops. The taskWork owns the handle's lifecycle: stamped on the body
	// ctxMeta at borrow, the permit acquired at the gate, released at body completion
	// (completedFn) or in Free (idempotent), recycled in Free.
	h           *heldPermit
	completedFn func() // h.release, captured once at creation
	// wave is the dispatching Wave; stored so Free() satisfies workq.Work (no-arg)
	// and stamped onto the body ctxMeta so nil-wave op dispatches from the task body
	// can resolve it.
	wave *waveImpl
	// bodyCtx is the body context borrowed at dispatch (borrowBodyContext),
	// descended from the submit ctx and carrying bodyMeta; the task body runs
	// under it and Free returns it. bodyMeta is the same meta the ctx carries,
	// kept so Execute can stamp the worker's E without a ctx.Value lookup.
	bodyCtx  context.Context //nolint:containedctx // the borrowed body ctx, released in Free
	bodyMeta *ctxMeta
}

func (wk *taskWork) Reset() {
	wk.poolWork = poolWork{}
	wk.task = nil
	wk.h = nil
	wk.completedFn = nil
	wk.wave = nil
	wk.bodyCtx = nil
	wk.bodyMeta = nil
}

// Run is the [execpool.Task] entry: the executor hands it the worker environment ee and
// Run owns ALL of its own cleanup. It runs the body against ee and frees the work. The
// executor never touches a body through the priority controller — Run is the whole
// contract (no Execution, no confirm-execute; admission already happened on the scheduler
// side, and the post-work called ex.Starting before PushBack).
func (wk *taskWork) Run(ee *workerExEnv) {
	wk.run(ee)
	wk.Free()
}

// run executes the task body against the per-worker environment ee. The body context
// (bodyCtx) was borrowed at dispatch; run stamps ee — the only piece not known until a
// worker picks the task up — onto the body meta and runs the body under bodyCtx. The held
// limiter request was stamped at borrow. It takes ee directly (not via the worker ctx), so
// it carries no Execution and no ctx dependency — the shape the executor pool's Task.Run
// needs.
func (wk *taskWork) run(ee *workerExEnv) {
	wk.bodyMeta.executionEnvironment = ee
	//nolint:contextcheck // the body runs under the borrowed body ctx by design
	wk.task.Execute(wk.bodyCtx, wk.Group(), wk.completedFn)
}

//nolint:contextcheck // background context used only for tracing
func (wk *taskWork) Free() {
	traceRegion := "taskWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	wave := wk.wave
	wk.task.Free()
	if wk.h != nil {
		// Normal completion already released (completedFn); release here is the
		// idempotent backstop for tasks freed without executing (dispatch failure,
		// cancellation drain) — a held permit is given back, a never-acquired handle
		// no-ops. release() and Put recurse over the whole joint set (head + rest).
		wk.h.release()
		rest := wk.h.rest
		heldPermitPool.Release(wk.h) // Reset nils rest, so capture it first
		for _, r := range rest {
			heldPermitPool.Release(r)
		}
		wk.h = nil
	}
	// Return the body context borrowed at dispatch — its child ctx to ctxpool and
	// its meta to bodyMetaPool. Runs whether or not the body executed (a task freed
	// without running still borrowed at dispatch).
	if wk.bodyCtx != nil {
		releaseBodyContext(wk.bodyCtx)
		wk.bodyCtx = nil
		wk.bodyMeta = nil
	}
	wk.Close(wave)
	taskWorkPool.Release(wk)
}

var taskWorkPool = omnipool.For[taskWork]()

// Init brings up the one-time warm substrate, called once per physical allocation by
// the pool ([omnipool.Initer]). It Inits the nbcq-backed queues and the governor — kept
// warm across recycles, NEVER re-Init'd (the twin-anchor design forbids the count
// restart) — plus the cached closure fields and the wavestate callbacks, which are
// stable because they close over this impl.
func (wv *waveImpl) Init() {
	wv.skimQueue.Init()
	wv.governor.Init()
	wv.workQueue.Init(nil)
	wv.protoBB.ShouldBlock = wv.shouldBlock
	wv.blockFn = wv.block
	wv.tryAddWorkFn = wv.tryAddWork
	wv.addWorkFn = wv.addWork
	wv.state.Init(wv.sweepFunnels, wv.releaseCaches)
}

// Reset re-opens a recycled impl to a fresh Open cycle on the last-reference release
// ([omnipool.Resetter]). It clears the per-cycle payload — including the workQueue's
// rest-state reset, which zeroes the waiter set's missed-notification balance (whose
// recorded misses carry relay notifications of the finished cycle) and asserts the
// cycle drained its queues — and re-opens wavestate (stage→Open, fresh doneChan,
// counters already drained to zero) WITHOUT re-Init'ing the warm queues and WITHOUT
// touching the embedded RefCount (whose generation must survive). It runs
// single-owner — a recycle means refs hit zero — so the map clear, the queue reset,
// and the state re-open are quiescent (no live holder, no concurrent skim). The
// onDone callback (releaseCaches) has already cleared wv.caches by the time Done was
// reached.
func (wv *waveImpl) Reset() {
	wv.funnelInstances.Range(func(k, _ any) bool {
		wv.funnelInstances.Delete(k)
		return true
	})
	wv.workQueue.Reset()
	wv.state.Init(wv.sweepFunnels, wv.releaseCaches)
}

// sweepFunnels is the wave's enqueue-only end-of-work flush sweep, wired as
// wavestate's onFlushing callback: it fires synchronously on each Closed→Flushing
// transition (with references outstanding), on whatever goroutine drove the last work
// to completion or called Close. It pushes each funnel's pending flushes to the global
// pool — running NO user code — so the transition goroutine never re-enters the
// framework; the flushes themselves run later on pool workers.
//
// The reference bracket holds the wave open across the ranging: a deadline-driven
// Execute that drained before this sweep (and which the per-instance sweep skips via
// ClaimForFlush==false) could otherwise drop the last barrier mid-sweep, drive the wave
// to Done, and let a concurrent re-arm clear the map under us. The pin is the conditional
// TryIncrementReference for the same reason as suspendHeldPermit's: an increment
// resurrecting the count from zero cannot stop a Flushing→Done transition already in
// flight, so a failed pin means the wave's Done is reached or committed — every
// instance has flushed and there is nothing to sweep.
func (wv *waveImpl) sweepFunnels() {
	if !wv.state.TryIncrementReference() {
		return
	}
	defer wv.state.DecrementReference()
	wv.funnelInstances.Range(func(_, v any) bool {
		v.(funnelSweep).sweepFlush()
		return true
	})
}

// Skim processes outstanding task results and then waits for the next
// task result from a task previously launched via [Start]. It will block until
// a completed task is available or the provided context or wave is canceled.
// If the wave is closed and no tasks remain in flight, it will return immediately.
// See [Wave.TrySkim] for a non-blocking alternative.
//
// Returns an error if one occurred:
//
//   - nil: a task completed and was successfully skimmed
//   - ErrWaveDone: the wave is done and therefore nothing is left to skim
//   - other error: a task's skim function returned a non-nil error, or the
//     argument or wave-internal context was canceled
//
// If a skim function returns an error, the wave continues running and you can
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
func (wv *waveImpl) Skim(ctx context.Context) error {
	traceRegion := "Wave.Skim"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta, owned := wv.skimCtxMeta(ctx)
	if owned {
		// Recycle the minted skim meta after the drive (and after reclaim, below, which
		// is deferred later and so runs first). Safe: the skim ctx is used only to drive
		// the synchronous skim; handler-launched async work resolves its own nearest meta.
		defer releaseTopLevelContext(ctx)
	}
	// Suspend-class episode: a body driving this skim lends its limiter permit for the
	// duration (a sub-wave inherits it; deadlock-free) and reacquires on return.
	if h := suspendHeldPermit(meta, wv); h != nil {
		defer h.reclaimJoint(ctx, wv)
	}
	_, err := wv.skim(ctx)
	return err
}

func (wv *waveImpl) tryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	traceRegion := "Wave.tryAddWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", wv)
	if workFn, ok := wv.skimQueue.TryPopFront(); ok {
		queueFn(workFn)
	}
	return nil
}

func (wv *waveImpl) trySkim(ctx context.Context) (bool, error) {
	return wv.workQueue.TryExecuteOne(ctx, wv.tryAddWorkFn)
}

func (wv *waveImpl) skim(ctx context.Context) (bool, error) {
	return true, wv.workQueue.ExecuteOne(ctx, wv.addWorkFn, nil)
}

// This function is designed to be called before scattering a new task to
// preemptively skim or skim results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task
// pools.
func (wv *waveImpl) yield(ctx context.Context, deadline time.Time) error {
	traceRegion := "Wave.yield"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, _, owned := wv.skimCtxMeta(ctx)
	if owned {
		defer releaseTopLevelContext(ctx)
	}
	for {
		ok, err := wv.trySkim(ctx)
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

func (wv *waveImpl) shouldBlock(ctx context.Context) workq.BlockFunc {
	// Read the meta directly (not via wv.ctxMeta, which panics on a missing meta).
	// A dispatch chain (launcherScatterWork governor gate / limiterScatterWork
	// acquire) that postponed onto the global shared queue is re-run by a global
	// worker whose ctx carries NO ctxMeta — and such work is never a TOP-LEVEL
	// dispatch (top-level runs on the user goroutine, where the meta is present).
	// So "no matching meta → not top-level → don't block" is correct.
	meta, ok := metaFromContext(ctx)
	if ok && meta.wave == wv && meta.IsTopLevel() {
		return wv.blockFn
	}
	return nil
}

// block is the [workq.BlockFunc]-shaped block-and-help (no withdraw bracket — the
// governor/backpressure blocks; no permit demand is standing). It parks in the
// governor's waiter set, composing the registered wait channel into the
// block-and-help select; the WaitFunc confirm is the one-shot register-recheck,
// and the caller's loop re-tests its condition after every wake.
func (wv *waveImpl) block(
	ctx context.Context,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) error {
	var err error
	blockWaiters.WaitFunc(confirmBlockWaitFn, func(waitCh <-chan struct{}) bool {
		var woken bool
		woken, err = wv.blockAndHelp(ctx, blockDeadline, waitCh, nil)
		return woken
	})
	return err
}

// blockAndHelp waits on blockWaiters while help-executing this wave's work.
// withdrawFn, when non-nil, is the caller's withdraw-before-going-deep bracket
// (docs/plan/conservation-rework.md seam 5): it fires exactly once, just before
// the first help item this call executes — a helped item is where the goroutine
// can go deep, so the caller's standing demands must not keep their
// reservations past that point. A call that only parks, sees the block signal,
// or errors out never fires it, and the caller's next acquire re-registers
// after the call returns.
// blockWaitCh is the caller's registered wait channel — a permit gate's
// prepared [rdvq.Waiter] (armed as its demand's attendant) or a governor
// park's waiter registration. The caller settles that registration when this
// returns, using the returned blockWoken.
func (wv *waveImpl) blockAndHelp(
	ctx context.Context,
	blockDeadline time.Time,
	blockWaitCh <-chan struct{},
	withdrawFn func(),
) (blockWoken bool, err error) {
	traceRegion := "Wave.blockAndHelp"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", wv)
	ctx, meta, owned := wv.skimCtxMeta(ctx)
	if owned {
		defer releaseTopLevelContext(ctx)
	}
	// Suspend-class episode: the block-and-help wait both parks and
	// synchronously runs other framework-gated work. Bracketing here is
	// correctness-required, not just utilization — without it, a
	// subwave-top-level dispatch acquiring a limiter held by this same
	// goroutine's enclosing body self-deadlocks (see "Where suspend
	// fires" in docs/limiter-suspend-resume.md). Re-entrant: while an
	// enclosing bracket holds the whole set suspended this one no-ops
	// (nothing held); inside an enclosing reclaimJoint it suspends —
	// and on unwind reclaims — exactly the holds held at this level
	// (per-hold suspendTarget scoping, see reclaimJoint), so no wait
	// in the reclaim ever parks while holding a permit.
	if h := suspendHeldPermit(meta, wv); h != nil {
		defer h.reclaimJoint(ctx, wv)
	}
	adder := blockingWorkAdderPool.Get()
	defer blockingWorkAdderPool.Release(adder)
	adder.wave = wv
	adder.meta = meta
	adder.blockDeadline = blockDeadline
	adder.blockWaitCh = blockWaitCh

	err = wv.workQueue.ExecuteOne(ctx, adder.addWorkFn, withdrawFn)
	if errors.Is(err, errBlockWaitSignaled) {
		err = nil
	}
	return adder.blockWoken, err
}

var blockingWorkAdderPool = omnipool.For[blockingWorkAdder]()

type blockingWorkAdder struct {
	wave          *waveImpl
	meta          *ctxMeta
	blockDeadline time.Time
	blockWaitCh   <-chan struct{}
	blockWoken    bool

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
) error {
	var err error
	a.blockWoken, err = a.wave.addWorkWhileMaybeBlocking(
		ctx, a.meta, queueFn, workWaiters, confirmWorkWaitFn, a.blockDeadline, a.blockWaitCh)
	return err
}

func (wv *waveImpl) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
) error {
	traceRegion := "Wave.addWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Wave=%p", wv)
	ctx, meta := wv.ctxMeta(ctx)
	_, err := wv.addWorkWhileMaybeBlocking(ctx, meta, queueFn, waiters,
		confirmWaitFn, time.Time{}, nil)
	return err
}

func (wv *waveImpl) addWorkWhileMaybeBlocking(
	ctx context.Context,
	meta *ctxMeta,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	blockDeadline time.Time,
	blockWaitCh <-chan struct{},
) (blockWoken bool, err error) {
	meta.PushQueueFunc(queueFn)
	defer meta.PopQueueFunc()

	if workWaiters == nil {
		err = wv.tryAddWork(ctx, queueFn)
	} else {
		work, ok := wv.skimQueue.PopFrontFunc(
			func(inboxCh <-chan workq.Work, outboxWaitCh <-chan struct{}) rdvq.PopSelectResult[workq.Work] {
				// Declared per invocation: skimSelect (which is the only thing
				// that populates this) is skipped on any iteration where the
				// block confirm short-circuits — i.e. once the permit is
				// acquired/reclaimed. A value hoisted across iterations would
				// retain a stale outbox-ready result, keeping PopFrontFunc's
				// loop from ever reaching its no-wake exit.
				var psResult rdvq.PopSelectResult[workq.Work]
				workWaiters.WaitFunc(
					confirmWorkWaitFn,
					func(workWaitCh <-chan struct{}) bool {
						var workWoken bool
						if blockWaitCh == nil {
							psResult, workWoken, err = wv.skimSelect(
								ctx, inboxCh, outboxWaitCh, workWaitCh, nil, nil,
							)
						} else {
							var blockTimerCh <-chan time.Time
							// Zero or Forever deadline: no timer (block until ctx
							// cancel or wake). Non-zero, non-Forever: set up a
							// timer; if it fires we'll return up through
							// errBlockWaitSignaled.
							if !blockDeadline.IsZero() && !isForever(blockDeadline) {
								blockTimer := timerp.Get()
								defer timerp.Put(blockTimer)
								timerp.Reset(blockTimer, max(0, time.Until(blockDeadline)))
								blockTimerCh = blockTimer.C
							}
							var bw bool
							psResult, workWoken, bw, err = wv.skimSelectBlocking(
								ctx, inboxCh, outboxWaitCh, workWaitCh, blockTimerCh, blockWaitCh,
							)
							blockWoken = blockWoken || bw
						}
						return workWoken
					},
				)
				return psResult
			},
		)
		if ok {
			queueFn(work)
		}
	}
	return blockWoken, err
}

func (wv *waveImpl) skimSelect(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxWaitCh <-chan struct{},
	workWaitCh <-chan struct{},
	blockTimerCh <-chan time.Time,
	blockWaitCh <-chan struct{},
) (psResult rdvq.PopSelectResult[workq.Work], workWoken bool, err error) {
	psResult, workWoken, _, err = wv.skimSelectBlocking(ctx, inboxCh, outboxWaitCh, workWaitCh, blockTimerCh, blockWaitCh)
	return
}

func (wv *waveImpl) skimSelectBlocking(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxWaitCh <-chan struct{},
	workWaitCh <-chan struct{},
	blockTimerCh <-chan time.Time,
	blockWaitCh <-chan struct{},
) (psResult rdvq.PopSelectResult[workq.Work], workWoken, blockWoken bool, err error) {
	traceRegion := "Wave.skimSelect"
	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxWaitCh=%p, workWaitCh=%p, blockWaitCh=%p",
		inboxCh, outboxWaitCh, workWaitCh, blockWaitCh)
	select {
	case work := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", inboxCh)
		psResult.InboxEmptied(work)
	case <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received wake from outboxWaitCh=%p", outboxWaitCh)
		psResult.OutboxReady()
	case <-workWaitCh:
		trace.Logf(ctx, traceRegion, "received wake from workWaitCh=%p", workWaitCh)
		workWoken = true
	case <-blockTimerCh:
		trace.Logf(ctx, traceRegion, "received block deadline timer signal")
		err = errBlockWaitSignaled
	case <-blockWaitCh:
		trace.Logf(ctx, traceRegion, "received wake from blockWaitCh=%p", blockWaitCh)
		blockWoken = true
		err = errBlockWaitSignaled
	case <-wv.state.Done():
		trace.Logf(ctx, traceRegion, "received wave done signal")
		err = ErrWaveDone
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		err = ctx.Err()
	}
	return
}

type skimPostWork struct {
	poolWork
	wave *waveImpl
	work boundSkimWork
	// shouldBlock is captured at dispatch (from the dispatching meta) rather than
	// re-derived from the run ctx: when this producer postpones onto the global
	// shared queue, a global worker re-runs it under the worker ctx, which carries
	// no ctxMeta — so ctxMeta(ctx) would panic ("Context not associated with a
	// wave"). The post only needs ShouldBlock + the ctx for cancellation.
	shouldBlock bool
}

func (wk *skimPostWork) Init(group workq.GroupID, wv *waveImpl, work boundSkimWork, shouldBlock bool) {
	wk.poolWork.Init(group, wv)
	wk.wave = wv
	wk.work = work
	wk.shouldBlock = shouldBlock
}

func (wk *skimPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "skimPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", wk)

	posted, err := func() (bool, error) {
		waiting := func() {
			// Call Waiting on the nested skimWork to notify the governor
			wk.work.Waiting(&wk.wave.governor)
		}

		tryPost := func() bool {
			// Try non-blocking post - can be retried if it fails
			return wk.wave.skimQueue.TryPushBack(wk.work, nil)
		}

		if tryPost() {
			return true, nil
		}

		if !ex.ShouldBlockOrPostpone() {
			return false, nil
		}

		if !wk.shouldBlock {
			// Nested/queued: postpone. Register for a skim-queue slot and re-check;
			// a scheduler worker re-drives this post when a slot frees.
			ex.Listener.AddTo(wk.wave.skimQueue.Listeners())
			posted := tryPost()
			if !posted {
				waiting()
			}
			trace.Logf(ctx, traceRegion, "postponed, posted=%v", posted)
			return posted, nil
		}

		// Top-level backpressure: block-and-help. wv.block parks on skimSelect —
		// which composes new skim work as a wake source — so this help-drains the
		// wave (freeing skim-queue room) while retrying the push, and never
		// just-blocks the driver on a full queue.
		ex.Blocking()
		for !tryPost() {
			waiting()
			if _, err := wk.wave.blockAndHelp(ctx, time.Time{}, nil, nil); err != nil {
				trace.Logf(ctx, traceRegion, "block-and-help ended, err=%v", err)
				return false, err
			}
		}
		return true, nil
	}()

	if posted {
		ex.Starting() // Signal success only if we actually posted
		wk.work = nil // Clear the work reference since it's now owned by the queue
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (wk *skimPostWork) Free() {
	traceRegion := "skimPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	// Free the nested work item if we still own it
	if wk.work != nil {
		trace.Logf(context.Background(), traceRegion, "wk.work.Free()")
		wk.work.Free()
		wk.work = nil
	}

	wk.Close(wk.wave)
	skimPostWorkPool.Release(wk)
}

var skimPostWorkPool = omnipool.For[skimPostWork]()

//nolint:contextcheck // background context used only for tracing
func (wv *waveImpl) newSkimPostWork(group workq.GroupID, skimWork boundSkimWork, shouldBlock bool) *skimPostWork {
	traceRegion := "Wave.newSkimPostWork"

	wk := skimPostWorkPool.Get()
	wk.Init(group, wv, skimWork, shouldBlock)

	trace.Logf(context.Background(), traceRegion, "Wave=%p created %v", wv, wk)
	return wk
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
//   - ErrWaveDone: the wave is done and no more tasks will ever be available
//   - other error: a skim function returned an error or the context was canceled
//
// If a skim function returns an error, the wave continues running and you can
// keep calling TrySkim to process more tasks (and errors, if any) until you
// receive ErrWaveDone.
//
// See Skim for additional details.
func (wv *waveImpl) TrySkim(ctx context.Context) (bool, error) {
	traceRegion := "Wave.TrySkim"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, _, owned := wv.skimCtxMeta(ctx)
	if owned {
		defer releaseTopLevelContext(ctx)
	}
	return wv.trySkim(ctx)
}

// SkimAll processes task results until the wave completes or an error occurs.
// If the wave has not been closed, SkimAll will block indefinitely, as new
// tasks might be added at any time. It will return an error if the provided context
// or wave is canceled. After the wave is closed, SkimAll will continue processing
// tasks until all work completes (including tasks spawned during result processing)
// and then return.
//
// Returns nil when the wave is done, or an error if the context is canceled or a
// task's [Skim] returns a non-nil error. If a skim function returns an
// error, you can call SkimAll again to continue processing more tasks (and
// errors, if any) until the wave is done (i.e., SkimAll returns nil).
//
// If all skim functions are thread-safe, then SkimAll is thread-safe and
// can be called concurrently from multiple goroutines. In this case they will
// collectively process all results, with each call handling a subset. Blocking
// and non-blocking calls may also be mixed, as can calls to any of the other
// skim methods.
//
// NOTE: This method will serially call each skimmed task's [Skim] and
// wait until it returns.
func (wv *waveImpl) SkimAll(ctx context.Context) error {
	traceRegion := "Wave.SkimAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Suspend-class episode (one per SkimAll, not per inner skim): a
	// body driving this drain — e.g. a subwave's CloseAndSkimAll —
	// parks here while holding its limiter permit; give the slot back
	// for the whole drain and reclaim (help-shaped) on return.
	ctx, meta, owned := wv.skimCtxMeta(ctx)
	if owned {
		defer releaseTopLevelContext(ctx)
	}
	if h := suspendHeldPermit(meta, wv); h != nil {
		defer h.reclaimJoint(ctx, wv)
	}

	err := wv.skimAll(ctx, wv.skim)
	if errors.Is(err, ErrWaveDone) {
		return nil
	}
	return err
}

// TrySkimAll processes all currently available task results without blocking.
// Unlike [Wave.SkimAll], TrySkimAll will return immediately if there are no
// completed tasks ready to process, regardless of whether the wave is closed or
// whether there are still tasks in flight.
//
// Returns nil when all immediately available tasks have been processed, ErrWaveDone
// when the wave is done, or an error if the context is canceled or a task's
// [Skim] returns a non-nil error. If a skim function returns an error,
// you can call TrySkimAll again to continue processing more tasks (and errors,
// if any) until you receive ErrWaveDone.
//
// See SkimAll for information about thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [Skim] and wait until it finishes processing.
func (wv *waveImpl) TrySkimAll(ctx context.Context) error {
	traceRegion := "Wave.TrySkimAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	return wv.skimAll(ctx, wv.trySkim)
}

func (wv *waveImpl) skimAll(ctx context.Context, skimFn func(context.Context) (bool, error)) error {
	ctx, _, owned := wv.skimCtxMeta(ctx)
	if owned {
		defer releaseTopLevelContext(ctx)
	}
	for {
		ok, err := skimFn(ctx)
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
	wave *waveImpl
	task *taskWork
}

func (wk *taskPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "%v", wk)
	}

	// Hand the admitted body to the EXECUTOR (dispatch/execution split): the body runs
	// off the scheduler so it never pins one. Try a non-blocking direct handoff first; if
	// no executor is waiting, block-as-demand spawns one — but only block when the caller
	// can wait (ex.ShouldBlockOrPostpone): a TryExecuteNow probe (AddToListeners nil) gives
	// up instead, and a controller drive postpones to retry under its wait protocol. This
	// runs only on a scheduler worker or a top-level/skim producer goroutine — never on an
	// executor body goroutine (nested submits are dropped to the scheduler, not run inline)
	// — so a blocking PushBack here can never wedge waiting for an executor.
	if bodyExecutor.TryPushBack(wk.task) {
		ex.Starting()
		wk.task = nil // ownership transferred to the executor, which runs + Frees it
		return nil
	}
	if !ex.ShouldBlockOrPostpone() {
		return nil // try-once probe: didn't start, caller (TryExecuteNow / drive) retries
	}
	err := bodyExecutor.PushBack(ctx, wk.task)
	if err == nil {
		ex.Starting()
		wk.task = nil
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (wk *taskPostWork) Free() {
	traceRegion := "taskPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	// Free the nested task work if we still own it
	if wk.task != nil {
		trace.Logf(context.Background(), traceRegion, "wk.task.Free()")
		wk.task.Free()
	}

	wk.Close(wk.wave)
	taskPostWorkPool.Release(wk)
}

var taskPostWorkPool = omnipool.For[taskPostWork]()

type poolWork struct {
	workq.WorkItem
}

func (wk *poolWork) Init(group workq.GroupID, wv *waveImpl) {
	wk.WorkItem.Init(group)
	trace.Logf(context.Background(), "poolWork.Init", "%v", &wk.WorkItem)
	wv.state.IncrementWork()
	// Per-holder object-lifetime reference, parallel to the wavestate work reference:
	// this work item keeps the impl alive for as long as it holds its naked *waveImpl.
	// Minted under the dispatching method's live Get (refs >= 1), so it cannot race a
	// recycle or increment from zero. Released in Close, beside DecrementWork.
	wv.AddRef()
}

//nolint:contextcheck // background context used only for tracing
func (wk *poolWork) Close(wv *waveImpl) {
	if wk.ID() == 0 {
		// This check and panic is best-effort only as it may also be a race if
		// Close() is called from multiple goroutines -- which it should not be.
		panic("already closed")
	}
	trace.Logf(context.Background(), "poolWork.Close", "%v", &wk.WorkItem)
	wv.state.DecrementWork()
	// Drop the per-holder object-lifetime reference (paired with Init's AddRef), AFTER
	// DecrementWork has run any Done transition: on the last reference this recycles the
	// impl (running Reset), which is safe precisely because refs hit zero means this is
	// the sole remaining holder.
	wavePool.Release(wv)
}

func (wv *waveImpl) newTaskPostWork(group workq.GroupID, deadline time.Time, task *taskWork) workq.Work {
	wk := taskPostWorkPool.Get()
	wk.Init(group, wv)
	wk.wave = wv
	wk.task = task
	return wk
}

// panicIfDone panics if the wave is in the done state
func (wv *waveImpl) panicIfDone() {
	wv.state.PanicIfDone()
}

// Close changes the wave's state from open to closed, which allows it to eventually
// progress to the done state once all tasks complete. When a wave is closed,
// [Wave.SkimAll] will return after processing all existing tasks and any tasks
// they spawn, rather than blocking indefinitely.
//
// After a wave is closed and all tasks have completed, launching new tasks will panic.
// Skimming operations will continue to work normally but will always return
// immediately with no results.
//
// Note that tasks can still be added after Close is called but before all tasks
// have completed.
//
// Close may be called from any goroutine and may safely be called more than once.
//
// Close transitions the substrate Open→Closed and reports whether THIS call won the
// transition (the winning caller is the one that drops the owner reference; see
// [Wave.Close]). Safe to call from any goroutine and more than once — only the winner
// returns true.
//
//nolint:contextcheck // background context used only for tracing
func (wv *waveImpl) Close() bool {
	traceRegion := "Wave.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	return wv.state.Close()
}

// resolveWave upgrades the op's bound wave (if the op was bound with In(wave)), else
// the ambient wave attached to ctx (the framework stamps the dispatching wave onto a
// body's ctx), to a PINNED substrate. The returned impl carries an extra reference that
// the caller MUST drop with wavePool.Release once it has finished dispatching — the pin
// keeps the impl alive across meta minting and work-item creation (each created holder
// takes its own reference under this pin, so the AddRef-from-zero panic is unreachable).
//
// ok is false only for a bound wave that has already drained and recycled (a submit to a
// terminal wave); the caller surfaces that as ErrWaveDone. The ambient path cannot fail:
// the running body that stamped the ambient wave still holds a work reference on it, so
// the AddRef is under a live reference. Panics if neither a bound nor an ambient wave is
// available — a wave-agnostic op must be dispatched via op.In(wave) or from inside a
// wave body.
func resolveWave(opWave Wave, ctx context.Context) (*waveImpl, bool) {
	if !opWave.h.Empty() {
		return opWave.h.Get()
	}
	meta, ok := metaFromContext(ctx)
	if !ok || meta.wave == nil {
		panic("op constructed with nil wave dispatched without op.In(wave) and outside any wave body")
	}
	// The ambient wave is a naked pointer, provably live here: the running body that
	// stamped it holds a work reference on it, so AddRef is under a live reference and
	// cannot race a recycle. The caller Releases this pin after dispatching.
	meta.wave.AddRef()
	return meta.wave, true
}
