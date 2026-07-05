// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Funnel represents a stateful aggregation operation. Inputs flow in through
// [Funnel.Submit] (or via [Funnel.Start] for value-producing tasks); the
// user-supplied [Accumulator] processes them inside a funnel-body worker on the
// shared pool. Downstream emission is the Accumulator body's responsibility — it
// calls Submit on whatever downstream sinks it has captured. There is no
// framework-mediated output type; Accumulator errors are surfaced via the Wave's
// SkimAll path.
//
// Thread-safety and copying: a Funnel value is designed to be copied. While a
// single Funnel value does not support concurrent calls to Start or TryStart,
// copies of a Funnel can be used concurrently. All copies share the same funnel
// identity (a unique id) and route work to the same Accumulator instances, which
// are owned by the wave (keyed by that id) — so a Funnel is a plain value holding
// only its wave, factory, limiter, id, and (cached) instance/work pools; it has no
// internal heap object.
//
// Concurrency: a Funnel is parallel by default — the framework may run several
// [Accumulator] instances for one (Funnel, Wave) at once, spreading inputs across
// them. Each instance must therefore own all the state it touches; nothing is
// shared or ordered across instances. For strict-serial "reducer" behavior — a
// single instance that folds every input over shared state, such as a running
// aggregate or an ordering window — cap the op to one in-flight execution with
// WithLimits(NewSemaphore(1)). A stateful accumulator that assumes serial
// delivery will race or stall without that cap.
//
// Resource management: a Funnel is wave-scoped and needs no explicit close (there is
// no Close or Dup, and no factory Close). Its per-(funnel,wave) accumulator instances
// are owned by the wave and force-flushed when the wave drains; the framework
// guarantees an instance is never touched after its Flush, so the owner may release
// any factory-level state after the drain returns (see [AccumulatorFactory]).
type Funnel[T any] struct {
	wave    *Wave
	factory AccumulatorFactory[T]

	// limiter caps how many funnel-body executions this Funnel runs concurrently.
	// The zero Limiter (impl == nil) means unlimited. Acquired in funnelWork.Execute
	// and released when Execute completes.
	limiter Limiter

	// id uniquely identifies this Funnel so its accumulator instances are keyed in the
	// owning wave's funnelInstances map. Copies share it (and so the same instances).
	id funnelID

	// instancePool / workPool are the cached per-type omnipools (omnipool.For). Holding
	// them on the Funnel keeps the instance/work handling fully typed — instances are
	// *funnelInstance[T], never boxed — so the Accumulator[T] call path stays generic.
	instancePool *omnipool.Pool[funnelInstance[T]]
	workPool     *omnipool.Pool[funnelWork[T]]
}

// funnelErrSink is the framework-owned, wave-agnostic error sink for all funnels.
// Accumulator errors are routed through it; its handler returns the error as-is so it
// surfaces via the target wave's SkimAll path. It carries no wave — emitErr calls
// submit with the explicit target wave — so a single package-level sink serves every
// funnel on every wave.
var funnelErrSink = newInternalSkimmer[struct{}](NewErrHandler(func(_ context.Context, err error) error {
	return err
}))

// funnelID uniquely identifies a Funnel within the process so its accumulator
// instances can be keyed in the owning wave's funnelInstances map. A package-global
// monotonic counter; copies of a Funnel share the same id.
type funnelID uint64

var nextFunnelID atomic.Uint64

func newFunnelID() funnelID { return funnelID(nextFunnelID.Add(1)) }

// NewFunnel creates a new Funnel operation. Pass [WithLimits] in opts to bind a
// [Limiter] (e.g. via [NewSemaphore]) that caps the number of concurrent funnel-work
// executions for this Funnel.
//
// The framework manages an internal error sink (owned by the wave) that surfaces
// Accumulator errors through the Wave's SkimAll path; the user's Accumulator body is
// responsible for routing successful results via Submit on whatever downstream sinks
// it captures.
//
//nolint:contextcheck // background context used only for tracing
func NewFunnel[T any](
	wave *Wave,
	funnelFactory AccumulatorFactory[T],
	opts ...OpOption,
) Funnel[T] {
	traceRegion := "NewFunnel"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if wave == nil {
		panic("wave must be non-nil")
	}
	if funnelFactory == nil {
		panic("funnelFactory must be non-nil")
	}

	cfg := resolveOpConfig(opts)

	c := Funnel[T]{
		wave:         wave,
		factory:      funnelFactory,
		limiter:      cfg.singleLimiter(),
		id:           newFunnelID(),
		instancePool: omnipool.For[funnelInstance[T]](),
		workPool:     omnipool.For[funnelWork[T]](),
	}

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Funnel(id=%d), wave=%p", c.id, wave)
	}

	return c
}

// NewFnFunnel binds a closure-based factory function to a Funnel. Convenience
// wrapper for `NewFunnel(wave, NewAccumulatorFactory(newAccumulator), opts...)`.
func NewFnFunnel[T any](
	wave *Wave,
	newAccumulator func() Accumulator[T],
	opts ...OpOption,
) Funnel[T] {
	return NewFunnel(wave, NewAccumulatorFactory(newAccumulator), opts...)
}

// ErrFunnel is the [Funnel][struct{}] case viewed as an err
// aggregator — a funnel whose Accumulator receives err inputs (the
// value half is always void). Typically constructed via
// [NewErrFunnel], which wires a closure-based factory whose
// accumulator delivers errs through an err-receiving signature.
type ErrFunnel = Funnel[struct{}]

// NewErrFunnel constructs a [Funnel][struct{}] whose per-instance
// accumulator receives err inputs via an err-receiving signature.
// Convenience wrapper for [NewFunnel] + [NewErrAccumulatorFactory]
// — uses the direct-fn-storage err-only adapter so the framework
// adds no closure allocations of its own.
//
// accumulate is called for every [Funnel.SubmitErr] (and matching
// SubmitResult-with-non-nil-err); it returns the flush deadline
// (zero for "no specific deadline") and any error.
// flush is optional (pass nil for a no-op flush). For per-instance
// state, use [NewFunnel] + [NewAccumulatorFactory] with a
// [NewErrAccumulator] inside the factory closure.
func NewErrFunnel(
	wave *Wave,
	accumulate func(ctx context.Context, err error) (time.Time, error),
	flush func(ctx context.Context) error,
	opts ...OpOption,
) ErrFunnel {
	return NewFunnel(wave, NewErrAccumulatorFactory(accumulate, flush), opts...)
}

// Submit posts a value to the Funnel. Sugar for
// SubmitResult(ctx, value, nil).
func (c *Funnel[T]) Submit(
	ctx context.Context,
	value T,
) error {
	return c.SubmitResult(ctx, value, nil)
}

// SubmitErr posts an err-only result to the Funnel. Sugar for
// SubmitResult(ctx, *new(T), err). Meaningful primarily when
// T = struct{}; for other T, the Accumulator receives the type's
// zero value alongside the err.
func (c *Funnel[T]) SubmitErr(
	ctx context.Context,
	err error,
) error {
	var zero T
	return c.SubmitResult(ctx, zero, err)
}

// SubmitResult posts a (value, err) pair to the Funnel. The pair
// is forwarded to the Accumulator as-is; sinks that genuinely want
// both halves of a Go result tuple use this form.
func (c *Funnel[T]) SubmitResult(
	ctx context.Context,
	value T,
	err error,
) error {
	traceRegion := "Funnel.SubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Funnel(id=%d)", c.id)

	c.wave.ensureArmed() // dispatch entry: re-arm a drained wave
	// Mint-or-reuse a meta: in-body submits reuse the ambient body meta; a top-level
	// op.In(&wave).Submit from a bare ctx mints a fresh top-level meta (and a
	// cross-wave submit redirects into the funnel's wave, recording the source as
	// parent). No ctx-type restriction — a value may be submitted to a funnel from
	// anywhere.
	// Funnel does not yet recycle its minted meta (its body borrows from the
	// meta-stamped ctx, so freeing it needs the borrow-source fix first); owned ignored.
	ctx, meta, _ := c.wave.topLevelCtxMeta(ctx, func(contextType) {})
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return c.submit(ctx, meta, group, value, err)
}

// TrySubmit attempts to Submit without blocking past deadline.
// Sugar for TrySubmitResult(ctx, deadline, value, nil).
func (c *Funnel[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	value T,
) (bool, error) {
	return c.TrySubmitResult(ctx, deadline, value, nil)
}

// TrySubmitErr attempts to SubmitErr without blocking past
// deadline. Sugar for TrySubmitResult(ctx, deadline, *new(T), err).
func (c *Funnel[T]) TrySubmitErr(
	ctx context.Context,
	deadline time.Time,
	err error,
) (bool, error) {
	var zero T
	return c.TrySubmitResult(ctx, deadline, zero, err)
}

// TrySubmitResult attempts to SubmitResult without blocking past
// deadline. See [Funnel.TrySubmit] for return semantics.
func (c *Funnel[T]) TrySubmitResult(
	ctx context.Context,
	deadline time.Time,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Funnel.TrySubmitResult"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Funnel(id=%d)", c.id)

	c.wave.ensureArmed() // dispatch entry: re-arm a drained wave
	// Funnel does not yet recycle its minted meta (its body borrows from the
	// meta-stamped ctx, so freeing it needs the borrow-source fix first); owned ignored.
	ctx, meta, _ := c.wave.topLevelCtxMeta(ctx, func(contextType) {})
	meta.Lock()
	defer meta.Unlock()
	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return c.trySubmit(ctx, meta, group, value, err, deadline)
}

func (c *Funnel[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
) error {
	funnelWork := c.newFunnelWork(ctx, group, value, err)
	postWork := newFunnelPostWork(group, c.wave, funnelWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

func (c *Funnel[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	funnelWork := c.newFunnelWork(ctx, group, value, err)
	postWork := newFunnelPostWork(group, c.wave, funnelWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}

// funnelInstanceQueue is the per-(funnel,wave) state the owning wave keeps in its
// funnelInstances map, keyed by funnel id. It caches live accumulator instances for
// reuse (the lock-free queue) and holds the instance pool for recycling. The map
// value is this typed object; the only type erasure is at the map boundary (the wave
// reaches it through the funnelSweep interface), so per-item handling stays [T] and
// T is never boxed.
type funnelInstanceQueue[T any] struct {
	queue        nbcq.Queue[*funnelInstance[T]]
	instancePool *omnipool.Pool[funnelInstance[T]]
}

// funnelSweep is the heterogeneous-T view the wave holds in its funnelInstances map
// so the end-of-work sweep can drive each funnel's pending flushes without knowing T.
type funnelSweep interface {
	sweepFlush()
}

// sweepFlush is the enqueue-only end-of-work sweep for one funnel's instances, run
// synchronously from the wave's onFlushing callback (single-threaded per Flushing
// transition; no accumulate runs concurrently because inFlightWork is zero). It drains
// the cache queue and, per instance: a spent shell is recycled (rule R2 — its flusher
// already detached); a live instance is arbitrated via ClaimForFlush and, if won,
// marked detached and pushed to the global pool as flush work (its Execute flushes and
// recycles it, since it is no longer cached); if lost, a due Execute already owns the
// flush, so it is dropped (that Execute drops the barrier — never a leak, only a rare
// pooling miss). It runs no user code, so the transition goroutine never re-enters.
func (q *funnelInstanceQueue[T]) sweepFlush() {
	for {
		inst, ok := q.queue.TryPopFront()
		if !ok {
			return
		}
		inst.mu.Lock()
		switch {
		case inst.accumulator == nil:
			// Already flushed (a deadline Execute or owner). R2 guarantees the flusher
			// no longer references it; we hold it exclusively, so recycle now.
			inst.mu.Unlock()
			q.instancePool.Put(inst)
		case defaultPool.ClaimForFlush(inst):
			// Won the flush: no due Execute owns it. Enqueue it (its Execute flushes
			// and self-recycles via detached, since it is no longer cached here).
			inst.detached = true
			inst.mu.Unlock()
			defaultPool.ForceFresh(inst)
		default:
			// Lost: a due Execute already claimed it and will flush + drop the barrier.
			// We popped it out, so it won't be recycled (rare GC), but never leaks.
			inst.mu.Unlock()
		}
	}
}

type funnelInstance[T any] struct {
	// ScheduledWorkItem supplies the immutable [workq.Work] identity
	// (ID/Group, Init'd once at creation) and the scheduled-queue
	// position bookkeeping. The identity is never mutated after Init, so
	// the controller can read ID()/Group() without holding c.mu (it sorts
	// buffered work by group then ID). Group is the instance's flush group
	// — distinct from earliestGroup below, which tracks the lowest input
	// group seen. Free is overridden (see below); the scheduled-queue
	// position is owned entirely by delayq (mutated only under its mutex),
	// so this type never hooks position changes — which is what keeps
	// delayq's mutex from ever waiting on c.mu (the deadlock fix).
	workq.ScheduledWorkItem

	// wave owns this instance (the per-wave flush barrier, the error sink, ctxMeta).
	// factory builds the accumulator. Both are copied from the Funnel value at
	// creation; the instance carries no funnel back-pointer.
	wave    *Wave
	factory AccumulatorFactory[T]

	mu            sync.Mutex
	earliestGroup workq.GroupID

	// accumulator is the live user state. Non-nil means the instance is
	// live; flush sets it nil exactly once, which is the sole liveness
	// signal — there is no per-instance reference count. Mutated only
	// under mu.
	accumulator Accumulator[T]

	// detached marks an instance the end-of-work sweep popped out of the cache queue
	// and enqueued as flush work: its Run must recycle it (it is no longer cached,
	// so the owner lineage won't). A deadline-driven flush leaves detached false and
	// leaves the spent shell cached for the owner/sweep to recycle. Set under mu by the
	// sweep before ForceFresh; read under mu by Run.
	detached bool

	// borrowSrcCtx is the ctx the scheduler-side Execute stashes for Run to borrow the
	// flush body ctx from (the instance carries no ctx of its own). It is written in
	// Execute before the handoff that publishes the instance to an executor — the
	// handoff's rendezvous supplies the happens-before — and read exactly once at the top
	// of Run, before c.mu is taken. That ordering is what keeps it race-free against an
	// owner reuse-pop, which may recycle a non-detached spent shell the instant Run
	// releases c.mu (see Run).
	borrowSrcCtx context.Context //nolint:containedctx // borrow source for the flush body ctx

	// flowTags is the head of the union chain of DAG-scoped flow riders (tags)
	// carried by this instance's accumulated items — one carrier ref held per
	// distinct instance (collectFlowTags, called from accumulate under mu). The
	// flush takeover hands the whole chain, refs included, to the flush body ctx
	// (flowFanInContext), which is what carries a tag's presence and its
	// follow-up lifetimes across the accumulate→flush fan-in. Nil'd at
	// takeover; mutated only under mu.
	flowTags *flowRiderNode
}

// Execute implements [workq.Work] as the scheduler-side admission for a due flush
// (CP-B1b — the deferred D2). The instance becomes fresh work either when a worker
// drains its deadline or when the end-of-work sweep ForceFreshes it; Execute then hands
// the flush BODY to the EXECUTOR (mirrors taskPostWork / funnelPostWork) so a blocking
// user Flush never pins a scheduler. There is no permit gate — flush is not limited — so
// admission is just the handoff: a non-blocking direct handoff first, and if no executor
// waits the work postpones and is retried-and-blocked when the scheduler worker parks
// (shouldStillWait), exactly as a task body's handoff is.
//
// ex.Starting fires only on a successful handoff, which transfers the instance to the
// executor (Run owns the flush, the barrier drop, and any recycle). The controller's
// subsequent Free is a no-op, and after Starting the controller drops its buffer slot, so
// nothing on the scheduler side touches the instance once it is handed off.
func (c *funnelInstance[T]) Execute(ctx context.Context, ex workq.Execution) error {
	// Stash the borrow source for Run; see the field and Run for why writing it here
	// (before the publishing handoff) and reading it once at the top of Run is race-free.
	c.borrowSrcCtx = ctx
	if bodyExecutor.TryPushBack(c) {
		ex.Starting()
		return nil
	}
	if !ex.ShouldBlockOrPostpone() {
		return nil // postpone; retried (and blocked) when the scheduler worker parks
	}
	err := bodyExecutor.PushBack(ctx, c)
	if err == nil {
		ex.Starting()
	}
	return err
}

// Run is the [execpool.Task] entry: an executor goroutine runs the flush against its
// worker environment ee. It borrows a fresh funnel-body ctx from the stashed source (wave
// ambient for downstream Submit resolution, funnel ctxType; cancellation rides the source
// by ancestry, the wave owns none), flushes under c.mu — dropping the per-instance wave
// barrier inside flush, ordered after the user Flush body so a downstream Submit takes its
// reference first — and then, only if the sweep detached it from the cache, recycles the
// spent shell. Rule R2: it never touches the instance after releasing c.mu otherwise, so
// an owner reuse-pop may recycle a non-detached shell the instant c.mu is released.
//
//nolint:contextcheck // src is the borrow source for the flush body ctx, not a propagated arg
func (c *funnelInstance[T]) Run(ee *workerExEnv) {
	src := c.borrowSrcCtx
	c.borrowSrcCtx = nil
	bodyCtx, _ := borrowBodyContext(src, c.wave, funnelContext, nil, ee)
	defer releaseBodyContext(bodyCtx)
	c.mu.Lock()
	c.flush(bodyCtx)
	detached := c.detached
	c.mu.Unlock()
	if detached {
		// The sweep removed this from the cache queue, so no owner will reclaim it;
		// R2 guarantees we hold it exclusively now. Recycle the spent shell.
		omnipool.For[funnelInstance[T]]().Put(c)
	}
}

// Free implements [workq.Work] as a no-op. The flush completes everything it needs — the
// user flush, the wave barrier, and (if detached) recycling — inside [funnelInstance.Run]
// on the executor, not here. The controller calls Free on the scheduler side right after
// Execute's successful handoff, possibly concurrently with Run; Free must therefore not
// read any instance field (it would race Run and any owner reuse-pop).
func (c *funnelInstance[T]) Free() {}

func (c *funnelInstance[T]) allocate(
	ctx context.Context,
	newAccumulator AccumulatorFactory[T],
) {
	traceRegion := "funnelInstance.allocate"
	defer trace.StartRegion(ctx, traceRegion).End()

	panicked := true
	defer func() {
		if panicked {
			c.emitErr(ctx, ErrFunnelFactoryPanicked)
		}
	}()
	c.accumulator = newAccumulator.NewAccumulator()
	panicked = false
	if c.accumulator == nil {
		c.emitErr(ctx, ErrFunnelFactoryReturnedNil)
		c.accumulator = &errAccumulator[T]{err: ErrFunnelFactoryReturnedNil}
	}

	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "Funnel returning new accumulator=%v", c.accumulator)
	}
}

// emitErr surfaces an Accumulator error through the wave-owned error sink. The
// errSink's handler returns the error to the caller of Wave.SkimAll. Successful
// results are not surfaced this way — the Accumulator body is expected to Submit
// those to user-owned downstream sinks directly.
func (c *funnelInstance[T]) emitErr(ctx context.Context, accErr error) {
	traceRegion := "funnelInstance.emitErr"
	defer trace.StartRegion(ctx, traceRegion).End()

	if accErr == nil {
		return
	}
	ctx, meta := c.wave.ctxMeta(ctx)
	err := funnelErrSink.submit(
		ctx, meta, c.earliestGroup, struct{}{}, accErr)
	if err != nil && ctx.Err() == nil {
		panic(fmt.Sprintf("unexpected non-cancelation error: %v", err))
	}
}

func (c *funnelInstance[T]) accumulate(
	ctx context.Context,
	input T,
	inputErr error,
) {

	traceRegion := "funnelInstance.funnel"
	defer trace.StartRegion(ctx, traceRegion).End()

	// Fan-in transfer, collect side: fold this item's DAG-scoped riders into
	// the instance's union (one ref per distinct flow instance), so tag
	// presence and follow-up lifetimes survive to the flush regardless of when
	// the item itself completes. Runs under c.mu (the only accumulate path).
	c.flowTags = collectFlowTags(c.flowTags, ctx)

	didNotPanic := false
	defer func() {
		if !didNotPanic {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, ErrFunnelPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Accumulate on accumulator=%v", c.accumulator)
	newFlushDeadline, err := c.accumulator.Accumulate(ctx, input, inputErr)
	didNotPanic = true

	if err != nil {
		c.emitErr(ctx, err)
	}

	switch {
	case newFlushDeadline.IsZero():
		// No deadline: do NOT schedule. The instance stays live in the cache queue
		// and is flushed by the end-of-work sweep (which finds it via the wave's
		// funnel map, not the scheduled queue). No far-future placeholder.
	case time.Until(newFlushDeadline) <= 0:
		// Already-past deadline — flush inline, but only if no deadline-driven
		// Execute has already claimed this instance. ClaimForFlush removes any pending
		// scheduled entry and grants the flush; if it returns false the instance was
		// already drained, so its pending Execute will flush the data just accumulated
		// and we leave the accumulator live (rule R1).
		if defaultPool.ClaimForFlush(c) {
			c.flush(ctx)
		}
	default:
		// Future deadline: (re)schedule on the shared pool's queue, where a worker
		// runs the flush when due. Reschedule re-adds (or updates) the entry unless the
		// instance was already drained, in which case it returns false and we leave it
		// to the pending Execute (rule R1).
		defaultPool.Reschedule(c, newFlushDeadline)
	}
}

// flush must already hold c.mu. Returns whether this call performed the flush
// (false if the instance was already flushed). Drops the per-instance wave
// barrier reference, but NOT the pooled object — the caller does that after
// releasing c.mu (an owner reuse-pop, a detached Execute, or the end-of-work sweep).
func (c *funnelInstance[T]) flush(ctx context.Context) bool {
	traceRegion := "funnelInstance.flush"

	accumulator := c.accumulator
	if accumulator == nil {
		// already flushed, ignore
		return false
	}
	c.accumulator = nil

	// Flow fan-in (docs/decisions/flow-design.md): path-scoped riders SEVER —
	// the inline already-past-deadline flush arrives here on the TRIGGERING
	// accumulate body's ctx, whose meta carries that one item's riders, one of
	// many folded into this flush — while the DAG-scoped tags collected from
	// ALL accumulated items TAKE OVER as the flush body's rider set, their
	// funnel-held refs adopted by the flush ctx and released with it. The
	// executor-driven path (Run) borrows from the scheduler ctx and is
	// naturally rider-free on the sever side; doing both here makes the rule
	// structural for every drive. The extent is synchronous (the user Flush
	// and its dispatches complete within this call).
	//nolint:contextcheck // flushCtx holds the adopted fan-in ctx to defer its release after the barrier
	var flushCtx context.Context
	if fc, adopted := flowFanInContext(ctx, c.flowTags); adopted {
		c.flowTags = nil
		flushCtx = fc
		ctx = fc
	}

	// Release the per-instance flush barrier reference acquired at
	// allocation. Registered FIRST among the trailing defers so it runs
	// LAST — after the accumulator.Flush body (so any downstream Submit
	// takes its work reference before this reference drops) AND after the
	// tag-union release below (so a tag follow-up fired by that release
	// takes its wave reference while this barrier still holds the wave open;
	// otherwise the wave-rooted fire could IncrementReference a Done wave).
	// totalReferences cannot transiently reach zero across an emitting flush.
	// Deferred so a panicking Flush still releases it. (c.wave is read here,
	// while c.mu is held, so the deferred call captures the state pointer,
	// not the instance.)
	defer c.wave.state.DecrementReference()

	// Tag-union release: registered after the barrier so it runs BEFORE it —
	// firing the adopted tag follow-ups while the wave is still held. Registered
	// before the panicked defer below so it runs AFTER it (that defer reads
	// ctx=flushCtx, which this release frees).
	if flushCtx != nil {
		defer releaseBodyContext(flushCtx)
	}

	panicked := true // Assume the worst
	defer func() {
		if panicked {
			// Just in case the panic is otherwise suppressed
			c.emitErr(ctx, ErrFunnelFlushPanicked)
		}
	}()

	trace.Logf(ctx, traceRegion, "calling Flush on accumulator=%v", accumulator)
	err := accumulator.Flush(ctx)
	panicked = false
	if err != nil {
		c.emitErr(ctx, err)
	}
	return true
}

// boundFunnelWork is the type-erased funnel body as seen by funnelPostWork (the
// scheduler-side admission decorator) and the executor. It is NOT a workq.Work: the body is
// PushBack'd to the executor (Run), not Executed through the priority controller. gate /
// releasePermit are the hoisted limiter permit (was inside funnelWork.Execute); Waiting is
// the governor downstream-pressure registration (from the embedded DownstreamWork); Free is
// the post-work's cleanup of an un-handed-off body.
type boundFunnelWork interface {
	Run(ee *workerExEnv)
	gate(ctx context.Context, ex workq.Execution) (bool, error)
	releasePermit()
	Free()
	Funnel(ctx context.Context)
	Waiting(*workq.Governor)
}

type funnelWork[T any] struct {
	poolWork
	workq.DownstreamWork
	// fn is the Funnel value (config), copied at dispatch: wave, factory, limiter, id,
	// and the cached pools. All copies share identity via fn.id.
	fn       Funnel[T]
	input    T
	inputErr error
	// h is the native limiter handle this work's admission runs through; nil for
	// unlimited operations, created at dispatch (Init) and persisting across postponed
	// retries (stable identity). The funnelWork owns its lifecycle: released at body
	// end in Execute (or idempotently in Free for never-executed work), recycled in
	// Free.
	h *heldPermit
	// bodyCtx is the body context borrowed at dispatch (descended from the submit
	// ctx); the funnel body runs under it and Free returns it. bodyMeta is the meta it
	// carries (with held = h, stamped at borrow) — executeInner stamps the worker's E
	// onto it.
	bodyCtx  context.Context //nolint:containedctx // the borrowed body ctx, released in Free
	bodyMeta *ctxMeta
}

func (c *Funnel[T]) newFunnelWork(
	submitCtx context.Context, group workq.GroupID, value T, err error,
) *funnelWork[T] {
	wk := c.workPool.Get()
	wk.Init(submitCtx, group, *c, value, err)
	return wk
}

//nolint:contextcheck // submitCtx is the borrow source for the body ctx, not a propagated arg
func (wk *funnelWork[T]) Init(
	submitCtx context.Context, group workq.GroupID, fn Funnel[T], input T, inputErr error,
) {
	wk.poolWork.Init(group, fn.wave)
	wk.fn = fn
	wk.input = input
	wk.inputErr = inputErr
	// For a limited funnel, resolve the body's own wave cache (mkdir-p'ing the forest
	// along the dispatching ancestry) at dispatch, where that ancestry is available;
	// the permit is acquired from it at the gate in Execute. Stamp the handle on the
	// body meta now so currentHeldPermit finds it. Worker bodies are fresh permit-roots
	// (the worker's E is stamped at Execute, not known here).
	if fn.limiter.pool != nil {
		m, _ := metaFromContext(submitCtx)
		wk.h = heldPermitPool.Get()
		wk.h.ownCache = fn.wave.ensureCache(m, fn.limiter.pool)
	}
	wk.bodyCtx, wk.bodyMeta = borrowBodyContext(submitCtx, fn.wave, funnelContext, wk.h, nil)
}

// instanceQueue returns the wave's per-funnel instance cache for this work's funnel,
// creating it on first use. The map value is the typed *funnelInstanceQueue[T]; the
// only type erasure is the sync.Map's any boundary.
func (wk *funnelWork[T]) instanceQueue() *funnelInstanceQueue[T] {
	if v, ok := wk.fn.wave.funnelInstances.Load(wk.fn.id); ok {
		return v.(*funnelInstanceQueue[T])
	}
	q := &funnelInstanceQueue[T]{instancePool: wk.fn.instancePool}
	q.queue.Init()
	actual, _ := wk.fn.wave.funnelInstances.LoadOrStore(wk.fn.id, q)
	return actual.(*funnelInstanceQueue[T])
}

func (wk *funnelWork[T]) Funnel(ctx context.Context) {
	// This is the owner lineage ("A"/"C"): an instance is either cached in the
	// queue or being processed here, never both.
	q := wk.instanceQueue()
	var hbc *funnelInstance[T]
	for {
		hbc, _ = q.queue.TryPopFront()
		if hbc == nil {
			break
		}
		hbc.mu.Lock()
		if hbc.accumulator != nil {
			if wk.Group() < hbc.earliestGroup {
				hbc.earliestGroup = wk.Group()
			}
			break // still holding hbc.mu lock — reuse this live instance
		}
		// Spent shell: a deadline-driven Execute already flushed it (which dropped
		// the barrier) and left it cached. As the owner, recycle it and keep looking
		// for a live instance.
		hbc.mu.Unlock()
		q.instancePool.Put(hbc)
	}
	if hbc == nil {
		hbc = q.instancePool.Get()
		hbc.mu.Lock()
		// Per-instance flush barrier: hold one wave reference for the instance's whole
		// live lifetime (until flush() runs). This keeps the wave out of Done while the
		// accumulator is unflushed, regardless of which worker eventually flushes it,
		// and is what guarantees every outstanding instance is flushed before the wave
		// drains. Released in flush().
		wk.fn.wave.state.IncrementReference()
		hbc.wave = wk.fn.wave
		hbc.factory = wk.fn.factory
		hbc.detached = false
		// Reset and re-Init the embedded work item: a fresh work ID, the
		// flush group, and a zeroed (never-scheduled) heap position. The
		// reset matters for reuse — a pooled instance retains the negative
		// removed sentinel from its previous life, and clearing it keeps
		// the position tri-state honest so a stray Expedite of a fresh
		// instance is caught (see delayq.Item).
		hbc.ScheduledWorkItem = workq.ScheduledWorkItem{}
		hbc.Init(wk.Group())
		hbc.earliestGroup = wk.Group()
		hbc.allocate(ctx, wk.fn.factory)
	}
	defer func() {
		// If funnel() flushed inline, it did so via ClaimForFlush, which
		// guarantees no deadline-driven Execute also holds this instance —
		// so this lineage owns the spent shell outright: recycle it.
		// Otherwise the instance is still live; cache it.
		spent := hbc.accumulator == nil
		hbc.mu.Unlock()
		if spent {
			q.instancePool.Put(hbc)
		} else {
			q.queue.PushBack(hbc)
		}
	}()
	hbc.accumulate(ctx, wk.input, wk.inputErr)
}

// gate acquires the funnel's limiter permit, mirroring limiterScatterWork for tasks. It is
// the permit gate HOISTED out of the old funnelWork.Execute onto the scheduler side
// (funnelPostWork.Execute), so the body that crosses to the executor is permit-free. Returns
// (true, nil) for an unlimited funnel. The held permit scopes the body run and is released
// at body end (Free) or, if the body never starts, by releasePermit (retry re-acquires).
func (wk *funnelWork[T]) gate(ctx context.Context, ex workq.Execution) (bool, error) {
	if wk.h == nil {
		return true, nil
	}
	return gateAcquire(ctx, ex, wk.fn.wave, wk.h)
}

// releasePermit gives back a permit acquired by gate when the body could not start (the
// handoff postponed); the gate re-acquires on retry (Acquire is state-free). No-op when
// unlimited or already released. The handle's recycle stays in Free.
func (wk *funnelWork[T]) releasePermit() {
	if wk.h != nil {
		wk.h.release()
	}
}

// Run is the [execpool.Task] entry: the executor hands it the worker environment ee and Run
// owns ALL cleanup. The permit was acquired on the scheduler side (gate); Run runs the body
// and frees the work (Free releases the permit and recycles). No Execution, no permit gate —
// admission already happened.
func (wk *funnelWork[T]) Run(ee *workerExEnv) {
	wk.run(ee)
	wk.Free()
}

// run executes the funnel body against the per-worker environment ee. The body context was
// borrowed at dispatch (carrying held = h already); run stamps ee — the one piece only
// known once a worker picks the work up — onto the body meta and pushes the funnel's group
// so nil-wave dispatches from inside the Accumulate / Flush body resolve to it, then runs
// the body under the borrowed ctx. It takes ee directly (no Execution, no ctx dependency) —
// the shape the executor pool's Task.Run needs (C2c).
func (wk *funnelWork[T]) run(ee *workerExEnv) {
	wk.bodyMeta.executionEnvironment = ee
	wk.bodyMeta.PushGroup(wk.Group())
	defer wk.bodyMeta.PopGroup()
	//nolint:contextcheck // the body runs under the borrowed body ctx by design
	wk.Funnel(wk.bodyCtx)
}

func (wk *funnelWork[T]) Free() {
	traceRegion := "funnelWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "funnelWork(%p), %v", wk, wk)

	if wk.h != nil {
		// Normal completion already released at body end; this is the idempotent
		// backstop for work freed without executing (cancellation drain) — a held
		// permit is given back, a never-acquired handle no-ops. Then recycle.
		wk.h.release()
		heldPermitPool.Put(wk.h)
		wk.h = nil
	}

	// Return the body context borrowed at dispatch (whether or not the body ran).
	if wk.bodyCtx != nil {
		releaseBodyContext(wk.bodyCtx)
		wk.bodyCtx = nil
		wk.bodyMeta = nil
	}

	wave := wk.fn.wave
	wk.DownstreamWork.Close()
	wk.poolWork.Close(wave)

	pool := wk.fn.workPool
	var zero Funnel[T]
	wk.fn = zero
	pool.Put(wk)
}

// funnelPostWork is the producer that hands a funnelWork off to the shared pool's
// queue (mirrors taskPostWork). The governor still applies: onWait registers the
// funnel work's downstream saturation on the wave's governor — the same one top-level
// task admission gates on and skim registers on — so funnel backpressure is unified
// with the rest of the wave's sources.
type funnelPostWork struct {
	poolWork
	wave *Wave
	work boundFunnelWork
}

func (wk *funnelPostWork) Init(group workq.GroupID, wave *Wave, work boundFunnelWork) {
	wk.poolWork.Init(group, wave)
	wk.wave = wave
	wk.work = work
}

func (wk *funnelPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "funnelPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", wk)

	// Permit gate, HOISTED from the old funnelWork.Execute (Wrinkle 1) so the body crosses
	// to the executor permit-free — mirrors limiterScatterWork for tasks. Miss → postpone.
	held, err := wk.work.gate(ctx, ex)
	if err != nil || !held {
		return err
	}

	// Hand the admitted body to the EXECUTOR (mirrors taskPostWork). Try a non-blocking
	// direct handoff first; if no executor waits, register the funnel's downstream governor
	// pressure (the old Post onWait) before blocking so upstream sources back off, then
	// block-as-demand brings an executor up. The blocking PushBack runs only on a scheduler
	// worker or a top-level/skim producer — never an executor body goroutine — so it cannot
	// wedge waiting for an executor. On any non-start, give the permit back (retry
	// re-acquires); the downstream pressure, if registered, is released by the body's Free.
	if bodyExecutor.TryPushBack(wk.work) {
		ex.Starting()
		wk.work = nil // ownership transferred to the executor, which runs + Frees it
		return nil
	}
	if !ex.ShouldBlockOrPostpone() {
		wk.work.releasePermit()
		return nil
	}
	wk.work.Waiting(&wk.wave.governor)
	err = bodyExecutor.PushBack(ctx, wk.work)
	if err == nil {
		ex.Starting()
		wk.work = nil
	} else {
		wk.work.releasePermit()
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (wk *funnelPostWork) Free() {
	traceRegion := "funnelPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", wk)

	// Free the nested work item if we still own it
	if wk.work != nil {
		trace.Logf(context.Background(), traceRegion, "wk.work.Free()")
		wk.work.Free()
		wk.work = nil
	}

	wk.Close(wk.wave)
	funnelPostWorkPool.Put(wk)
}

var funnelPostWorkPool = omnipool.For[funnelPostWork]()

//nolint:contextcheck // background context used only for tracing
func newFunnelPostWork(group workq.GroupID, wave *Wave, bc boundFunnelWork) *funnelPostWork {
	traceRegion := "newFunnelPostWork"

	wk := funnelPostWorkPool.Get()
	wk.Init(group, wave, bc)

	trace.Logf(context.Background(), traceRegion, "created %v", wk)
	return wk
}

// errAccumulator is the framework's substitute Accumulator used when a user
// FunnelFactory misbehaves (returns nil or panics during construction).
// Every call simply surfaces the recorded error; nothing accumulates.
type errAccumulator[T any] struct {
	err error
}

func (c errAccumulator[T]) Accumulate(ctx context.Context, value T, err error) (time.Time, error) {
	return time.Now(), c.err
}

func (c errAccumulator[T]) Flush(ctx context.Context) error {
	return c.err
}
