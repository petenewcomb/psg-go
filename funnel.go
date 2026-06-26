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
	ctx, meta := c.wave.topLevelCtxMeta(ctx, func(contextType) {})
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
	ctx, meta := c.wave.topLevelCtxMeta(ctx, func(contextType) {})
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
	// and enqueued as flush work: its Execute must recycle it (it is no longer cached,
	// so the owner lineage won't). A deadline-driven Execute leaves detached false and
	// leaves the spent shell cached for the owner/sweep to recycle. Set under mu by the
	// sweep before ForceFresh; read under mu by Execute.
	detached bool
}

// Execute implements [workq.Work]: it runs the scheduled flush once the
// instance's deadline has come due and the shared queue has surfaced it as fresh
// work — either a deadline drained by a worker, or the end-of-work sweep's
// ForceFresh. Any pool worker may run it. It flushes (dropping the per-instance
// barrier in flush) and then, only if the sweep detached it from the cache,
// recycles the spent shell — never touching the instance after releasing c.mu
// otherwise (rule R2), so an owner reuse-pop is free to recycle a non-detached
// shell the instant Execute releases c.mu.
//
//nolint:contextcheck // ctx is the borrow source for the flush body ctx, not a propagated arg
func (c *funnelInstance[T]) Execute(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	// The flush runs on a fungible pool worker whose ctx carries no ctxMeta, so borrow
	// a funnel-body ctx for it (mirroring accumulate-body dispatch): stamp the wave as
	// ambient (so a Flush body's downstream Submit resolves it), the worker's E, and the
	// funnel ctxType. Cancellation rides the worker ctx by ancestry; the wave owns none.
	ee := workerEnvFromContext(ctx)
	bodyCtx, _ := borrowBodyContext(ctx, c.wave, funnelContext, nil, ee)
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
	return nil
}

// Free implements [workq.Work] as a no-op. A flush completes everything it needs —
// the user flush, the wave barrier, and (if detached) recycling — inside
// [funnelInstance.Execute] before releasing c.mu and never touches the instance
// again. Because an owner reuse-pop may recycle a non-detached spent shell the
// instant c.mu is released (before the controller gets here to call Free), Free must
// not read any instance field.
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

	// Release the per-instance flush barrier reference acquired at
	// allocation. Deferred so a panicking Flush still releases it, and
	// ordered after the accumulator.Flush body below so that any
	// downstream Submit performed by Flush takes its work reference
	// before this reference drops — totalReferences cannot transiently
	// reach zero across an emitting flush. (c.wave is read here, while
	// c.mu is held, so the deferred call captures the state pointer, not
	// the instance.)
	defer c.wave.state.DecrementReference()

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

// boundFunnelWork interface allows type erasure for funnelWork instances
type boundFunnelWork interface {
	workq.Work
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
	// req is the Limiter request handle this work's admission runs
	// through; nil for unlimited operations, created lazily at the first gate
	// attempt and persisting across postponed retries (stable identity).
	// The funnelWork owns its lifecycle: released at body end in Execute
	// (or idempotently in Free for never-executed work), recycled in Free.
	req request
	// bodyCtx is the body context borrowed at dispatch (descended from the submit
	// ctx); the funnel body runs under it and Free returns it. bodyMeta is the
	// meta it carries — executeInner stamps the worker's E and the held request
	// (acquired on the worker, not at dispatch) onto it.
	bodyCtx  context.Context //nolint:containedctx // the borrowed body ctx, released in Free
	bodyMeta *ctxMeta
}

// funnelWork is the applicant its Limiter request is opened for:
// accessors box lazily, only when a sizing limiter actually reads them.
func (wk *funnelWork[T]) Processor() any {
	return wk.fn.factory
}

func (wk *funnelWork[T]) Value() any {
	return wk.input
}

func (wk *funnelWork[T]) Err() error {
	return wk.inputErr
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
	// Borrow the body context at dispatch (descended from the submit ctx). The
	// permit (heldRequest) is acquired on the worker in executeInner, the worker's
	// E stamped there too; both nil here. Worker bodies are fresh permit-roots.
	wk.bodyCtx, wk.bodyMeta = borrowBodyContext(submitCtx, fn.wave, funnelContext, nil, nil)
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

func (wk *funnelWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "funnelWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "funnelWork(%p), %v", wk, wk)

	if wk.fn.limiter.impl == nil {
		return wk.executeInner(ctx, ex)
	}

	if wk.req == nil {
		wk.req = wk.fn.limiter.impl.newRequest(wk)
	}
	held, err := acquireOrWait(ctx, ex, time.Time{}, wk.fn.wave.protoBB, wk.req)
	if err != nil || !held {
		return err
	}
	// The funnel permit scopes exactly the body run: executeInner always
	// starts, so the postpone-after-grant case doesn't arise here.
	// Released on return (panic-inclusive); Free's release is then an
	// idempotent no-op before the recycle.
	defer wk.req.release()
	return wk.executeInner(ctx, ex)
}

func (wk *funnelWork[T]) executeInner(ctx context.Context, ex workq.Execution) error {
	ex.Starting()
	// The body context was borrowed at dispatch; stamp the pieces only known on
	// the worker — the held limiter request (acquired in Execute) and this worker's
	// E — and push the funnel's group so nil-wave dispatches from inside the
	// Accumulate / Flush body resolve to it. Run the body under the borrowed ctx.
	wk.bodyMeta.heldRequest = wk.req
	wk.bodyMeta.executionEnvironment = workerEnvFromContext(ctx)
	wk.bodyMeta.PushGroup(wk.Group())
	defer wk.bodyMeta.PopGroup()
	//nolint:contextcheck // the body runs under the borrowed body ctx by design
	wk.Funnel(wk.bodyCtx)
	return nil
}

func (wk *funnelWork[T]) Free() {
	traceRegion := "funnelWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "funnelWork(%p), %v", wk, wk)

	if wk.req != nil {
		// Normal completion already released at body end; this is the
		// idempotent backstop for work freed without executing
		// (cancellation drain) — by-state: abandon PENDING / give back
		// HELD.
		wk.req.release()
		freeRequest(wk.req)
		wk.req = nil
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

	// Handoff to the GLOBAL pool's shared work queue (mirrors taskPostWork).
	// shouldBlock is read directly from the ctx (not via wave.ctxMeta, which panics on
	// a missing meta): a producer only postpones onto the global queue in LISTEN
	// mode (shouldBlock=false), and a global worker re-running it has no ctxMeta.
	meta, _ := metaFromContext(ctx)
	shouldBlock := meta != nil && meta.wave == wk.wave && meta.ShouldBlock()
	onWait := func() { wk.work.Waiting(&wk.wave.governor) }
	posted, err := defaultPool.Post(ctx, ex, shouldBlock, wk.work, onWait)
	if posted {
		wk.work = nil // ownership transferred to the queue
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
