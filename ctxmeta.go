// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/workq"
)

type contextType int

//go:generate go run golang.org/x/tools/cmd/stringer@v0.35.0 -type=contextType -linecomment
const (
	topLevelContext contextType = iota // top-level
	taskContext                        // task
	skimContext                        // skim
	funnelContext                      // funnel
)

type ctxMeta struct {
	// wave is the Wave this meta belongs to: both the dispatch/ownership identity
	// (validated by Wave.ctxMeta) and the ambient wave a nil-wave op dispatched from
	// this context resolves to (resolveWave). Always set on a live meta; nil only on
	// a zero-value meta not derived through a Wave.
	wave *Wave
	// parent links to the ctxMeta this one was derived or borrowed from — the
	// context it descends from along the value chain, for sync derivations
	// (top-level→skim, body→subwave) AND async borrows (borrowBodyContext,
	// which used to sever the link). The link is refcounted (refs): a child
	// holds its parent alive until the child itself releases, so a
	// borrowed-from context outlives every body borrowed from it even when the
	// borrower runs async — the borrowSrcCtx use-after-free fix
	// (docs/decisions/ctxmeta-parent-refcount.md). The chain is therefore
	// walkable across async boundaries; walks that must stay within one
	// synchronous extent (permit inheritance, the skim-nesting vet, the flow
	// fan-in boundary, permit-forest construction) step via syncParent, which
	// stops at a permitRoot — the isolation the old severing provided: the
	// pool's base ctx may carry a foreign wave's meta, and inheriting e.g. a
	// permit link there would let a worker find its dispatcher's permit across
	// the goroutine boundary. See docs/limiter-suspend-resume.md,
	// "Serialization and scoping".
	parent *ctxMeta
	// refs counts the holders of this meta: its owner (the dispatch or work
	// item that created it, dropped via releaseBodyContext /
	// releaseTopLevelContext), each live child meta (taken at derivation or
	// borrow, dropped when the child's own release cascades in unrefMeta), and
	// transient pins across scheduler stash windows (funnelInstance /
	// flowFireWork Execute→Run). The meta — and the selfCtx ctxpool child that
	// carries it — recycles only at zero; that is what keeps a stashed borrow
	// source valid until its async reader is done.
	refs atomic.Int32
	// permitRoot marks a meta whose body runs on a fungible worker goroutine
	// (a borrowBodyContext borrow or a follow-up fire): syncParent — and so
	// every synchronous-extent walk — stops here. This is the isolation role
	// the nil-parent-at-borrow severing used to play, split out now that
	// parent stays linked for lifetime (and, later, driver-link tracing).
	permitRoot bool
	// held is the native limiter handle (heldPermit) stamped at body entry and found
	// via currentHeldPermit at framework parking points — the permit-core replacement
	// for the eager heldRequest. A stamped handle holds a permit while the body runs
	// its own code and is suspended (permit lent) across drive episodes. nil for an
	// unlimited op.
	held        *heldPermit
	parentWaves map[*Wave]struct{}
	ctxType     contextType
	// riders is the head of the flow rider chain in scope
	// (docs/decisions/flow-rider-chain.md): a linked chain of one-binding nodes
	// shared by pointer along the causal dispatch chain, walked on read. nil when
	// no enclosing WithFlow registered anything. Inherited verbatim by derived metas
	// (ensureCtxMeta) and by body borrows from the dispatch-time ctx
	// (borrowBodyContext); severed to the tag union at the funnel accumulate→flush
	// fan-in (funnelInstance.flush via flowFanInContext).
	riders *flowRiderNode
	executionEnvironment

	// Recycling bookkeeping. selfCtx is the ctxpool child carrying this meta —
	// every creation path sets it, and unrefMeta frees it when refs drains, so
	// a child ctx is never re-stamped while anything can still resolve this
	// meta through it. ownsExEnv is true when THIS meta allocated its
	// topLevelExEnv (a derived skim meta reuses its parent's, so it must not
	// double-free it).
	selfCtx   context.Context //nolint:containedctx // the ctxpool child this meta rides; freed at refs==0
	ownsExEnv bool

	// origin is the flush body's origin link (docs/decisions/
	// context-pinning-and-origin-access.md): the last accumulate's meta,
	// stamped from the funnel instance's rolling driver pin while that pin is
	// still held — the ONE origin that is not the meta's own parent (a flush
	// body's parent is the scheduler-side borrow source, framework plumbing).
	// Set under single-party custody (the flush's own borrow or fan-in clone,
	// before the user body runs), cleared by releaseBodyContext; atomic so a
	// composed OriginFlow walk racing the clear reads a coherent pointer. nil
	// on every non-flush meta.
	origin atomic.Pointer[ctxMeta]

	// pin marks a meta minted by PinFlow (docs/decisions/
	// context-pinning-and-origin-access.md): pinNone for every ordinary meta,
	// pinLive from mint until UnpinFlow, pinExpired after. The live→expired
	// flip is a CAS so a racing double-unpin loses loudly; expired is what the
	// cold entry paths (PinFlow, UnpinFlow, ensureCtxMeta, WithFlow) panic on
	// — best-effort use-after-unpin detection, impossible once the ctxpool
	// child is reused (the documented residual).
	pin atomic.Int32
}

const (
	pinNone int32 = iota
	pinLive
	pinExpired
)

// vetNotExpiredPin panics on a context whose pin has been released: past the
// pin window a framework ctx is invalid, the same rule as every extent.
func (cm *ctxMeta) vetNotExpiredPin() {
	if cm.pin.Load() == pinExpired {
		panic("streampool: use of an unpinned flow context (UnpinFlow already called)")
	}
}

// Reset implements omnipool.Resetter (refs is atomic.Int32, whose noCopy would
// trip vet copylocks under omnipool's plain-copy zero).
func (cm *ctxMeta) Reset() {
	cm.wave = nil
	cm.parent = nil
	cm.held = nil
	cm.parentWaves = nil
	cm.ctxType = topLevelContext
	cm.riders = nil
	cm.executionEnvironment = nil
	cm.selfCtx = nil
	cm.ownsExEnv = false
	cm.permitRoot = false
	cm.origin.Store(nil)
	cm.pin.Store(pinNone)
	cm.refs.Store(0)
}

// ctxMetaAllocHook, when set, receives +1 as a meta is drawn from bodyMetaPool
// and -1 as one is recycled — the seam the conservation test uses to prove no
// meta leaks or is double-freed across a drained workload (mirrors
// flowNodeAllocHook). Production leaves it nil.
var ctxMetaAllocHook atomic.Pointer[func(int)]

func ctxMetaAlloc(delta int) {
	if h := ctxMetaAllocHook.Load(); h != nil {
		(*h)(delta)
	}
}

// newCtxMeta draws a meta from the pool with the owner's reference: refs
// starts at 1, dropped by the owner's release path (releaseBodyContext /
// releaseTopLevelContext / WithFlow's scope exit). Children add their own via
// refMeta.
func newCtxMeta() *ctxMeta {
	m := bodyMetaPool.Get()
	ctxMetaAlloc(1)
	m.refs.Store(1)
	return m
}

// refMeta takes one reference on m (nil-safe): a child meta pinning its
// parent, or a transient stash pin (Execute→Run).
func refMeta(m *ctxMeta) {
	if m != nil {
		m.refs.Add(1)
	}
}

// unrefMeta drops one reference on m and, at zero, recycles it — returning the
// owned topLevelExEnv, freeing the selfCtx ctxpool child, and returning the
// meta to its pool — then drops the freed meta's ref on its parent, cascading
// up. The cascade is what subsumes the old releaseParent owned-chain walk: a
// still-owned ancestor (a reused ambient meta, or one with other live
// children) stops the walk above zero naturally.
func unrefMeta(m *ctxMeta) {
	for m != nil {
		if n := m.refs.Add(-1); n != 0 {
			if n < 0 {
				panic("streampool: ctxMeta reference count underflow (double release)")
			}
			return
		}
		parent := m.parent
		if m.ownsExEnv {
			if ee, ok := m.executionEnvironment.(*topLevelExEnv); ok {
				topLevelExEnvPool.Put(ee)
			}
		}
		if m.selfCtx != nil {
			ctxpool.Free(m.selfCtx)
		}
		ctxMetaAlloc(-1)
		bodyMetaPool.Put(m) // Reset zeroes, incl. refs
		m = parent
	}
}

// syncParent returns parent when it belongs to the same synchronous extent,
// and nil at an async boundary (a permitRoot meta runs on a fungible worker
// goroutine; what lies above it is its dispatcher's stack, not this one's).
// Reproduces the reach of the old severed-parent chain for the walks that must
// not cross goroutines; the full refcounted parent link remains for lifetime
// (and future driver-link tracing).
func (cm *ctxMeta) syncParent() *ctxMeta {
	if cm.permitRoot {
		return nil
	}
	return cm.parent
}

// vetNotNestedInSkim panics if a blocking gather (Skim/SkimAll, hence
// CloseAndSkimAll) is being driven from inside a skim handler — i.e. an
// enclosing context on this goroutine is a skim context. Driving a
// subwave from a skim handler monopolizes the wave's sole serial skim
// driver while the handler is parked in the gather, which deadlocks
// under shared limiters / nested subwaves (see
// docs/limiter-suspend-resume.md, "Intake vs drain"). The fix is to keep
// skimming serial and drive subwork elsewhere:
// populate a [Funnel] from the handler (the map-reduce primitive), or
// launch a task that drives the subwave. Tasks and funnels are
// demand-driven, so they never monopolize a sole driver.
//
// Walks the synchronous parent chain (excluding the gather's own skim
// context), stopping at the async boundary via syncParent — a task body
// launched FROM a skim handler is the documented remedy and must not see the
// handler's stack; a follow-up fire meta (skimContext, permitRoot) is itself
// still checked, so bodies nested under a fire are redirected like any skim
// continuation. Funnel/task/top-level enclosing contexts are fine — only an
// enclosing *skim* handler is disallowed.
func (cm *ctxMeta) vetNotNestedInSkim() {
	for m := cm.syncParent(); m != nil; m = m.syncParent() {
		if m.ctxType == skimContext {
			panic("psg: cannot drive a subwave (Skim/SkimAll/CloseAndSkimAll) from a skim handler; " +
				"populate a Funnel from the handler, or launch a task to drive the subwave")
		}
	}
}

// currentHeldPermit returns the limiter handle held by the body this context is
// synchronously nested under, walking syncParent links and stopping at the first
// stamped handle. The starting meta's own held is visible even when it is itself
// a permitRoot (a body's handle is stamped on its own borrow meta); the walk just
// never crosses INTO a dispatcher's goroutine. Structurally there is at most one
// per chain (every stamp site is a chain root — see the held field doc); under
// help-execution nesting the first handle found is the enclosing episode's,
// already suspended, so the suspend bracket's `h != nil && h.suspend()` contract
// needs no state checks here.
func (cm *ctxMeta) currentHeldPermit() *heldPermit {
	for m := cm; m != nil; m = m.syncParent() {
		if m.held != nil {
			return m.held
		}
	}
	return nil
}

func (cm *ctxMeta) String() string {
	return fmt.Sprintf("{%v Wave=%p exEnv=%p}", cm.ctxType, cm.wave, cm.executionEnvironment)
}

func (cm *ctxMeta) IsTopLevel() bool {
	return cm.ctxType == topLevelContext
}

func (cm *ctxMeta) ShouldBlock() bool {
	switch cm.ctxType {
	case topLevelContext, taskContext:
		return true
	default:
		return false
	}
}

func (cm *ctxMeta) Lock() {
	if cm.IsTopLevel() {
		cm.executionEnvironment.Lock()
	}
}

func (cm *ctxMeta) Unlock() {
	if cm.IsTopLevel() {
		cm.executionEnvironment.Unlock()
	}
}

var waitMu sync.Mutex

// Wait for the scheduler to be mostly idle
func wait() {
	for {
		waitMu.Lock()
		start := time.Now()
		runtime.Gosched()
		elapsed := time.Since(start)
		waitMu.Unlock()
		if elapsed < 100*time.Microsecond {
			break
		}
	}
}

func (cm *ctxMeta) TryExecuteNow(
	ctx context.Context,
	deadline time.Time,
	work workq.Work,
) (bool, error) {
	traceRegion := "ctxMeta.TryExecuteNow"
	defer trace.StartRegion(ctx, traceRegion).End()

	executor := executorPool.Get()
	defer executorPool.Put(executor)
	ex := executor.BaseEx()

	if cm.IsTopLevel() {
		// Make sure existing work has a chance to run before we add more.
		wait()

		// Apply backpressure at top level by processing some outstanding work first
		err := cm.wave.yield(ctx, deadline)
		if err != nil {
			return false, err
		}
	}

	// Deadline interpretation (as currently implemented):
	//   - past time → fail-fast (no attempt)
	//   - other     → attempt once
	// The "attempt once" semantic is enforced by ex.AddToListeners
	// being nil — the blocking layer treats nil AddToListeners as
	// "don't block." Forever and future deadlines do not currently
	// install genuine bounded-wait blocking at this level; that is
	// deferred Thread C work (see WORKING_NOTES). Naively enabling
	// AddToListeners here causes hangs because the timer/listener
	// plumbing through taskPostWork isn't fully wired (known open
	// issue: "Deadline propagation in taskPostWork").
	if !deadline.IsZero() && !isForever(deadline) && !time.Now().Before(deadline) {
		return false, nil
	}

	err := work.Execute(ctx, ex)
	if !ex.Started() {
		return false, err
	}
	work.Free()
	return true, err
}

func (cm *ctxMeta) ExecuteNowOrQueue(
	ctx context.Context,
	work workq.Work,
) error {
	traceRegion := "ctxMeta.ExecuteNowOrQueue"
	defer trace.StartRegion(ctx, traceRegion).End()

	executor := executorPool.Get()
	defer executorPool.Put(executor)
	ex := executor.BaseEx()

	if cm.ShouldBlock() {
		if cm.IsTopLevel() {
			// Suspend-class episode: the WHOLE blocking dispatch — the
			// backpressure yield, any governor/limiter block-and-help
			// waits, and the inner post — is one episode for an
			// enclosing body's held limiter permit (a subwave dispatch
			// runs on the body's goroutine). The reclaim must come
			// after the inner post: on self-acquisition (the dispatched
			// op shares the holder's limiter), reclaiming any earlier
			// waits on a task that hasn't been queued yet. Interior
			// brackets (Wave.block) no-op while the whole set stays
			// suspended, and reclaim exactly what they re-suspend once
			// this bracket's own reclaim is in flight (see reclaimJoint).
			if h := suspendHeldPermit(cm, cm.wave); h != nil {
				defer h.reclaimJoint(ctx, cm.wave)
			}

			// Make sure existing work has a chance to run before we add more.
			wait()

			// Apply backpressure at top level by processing some outstanding work first
			err := cm.wave.yield(ctx, time.Time{})
			if err != nil {
				work.Free()
				return err
			}
		}

		// Signal the work that it should block by making AddToListeners non-nil
		ex.AddToListeners = func(*workq.Listeners) {
			panic("unexpected call to ctxMeta.TryExecuteOrQueue's ex.AddToListeners")
		}
	}

	return cm.executionEnvironment.ExecuteNowOrQueue(ctx, ex, work)
}

var executorPool = omnipool.For[workq.Executor]()

type executionEnvironment interface {
	Lock()
	Unlock()

	Group() workq.GroupID
	PushGroup(workq.GroupID)
	PopGroup()

	QueueFunc() workq.QueueWorkFunc
	PushQueueFunc(queueFn workq.QueueWorkFunc)
	PopQueueFunc()
	ExecuteNowOrQueue(context.Context, workq.Execution, workq.Work) error
}

type baseExEnv struct {
}

// Release returns the exEnv's pooled resources to their shared pools. Should be
// called via defer when the goroutine that owns this exEnv is exiting.
func (ee *baseExEnv) Release() {
}

type integrationExEnv struct {
	baseExEnv
	groupStack   []workq.GroupID
	queueFnStack []workq.QueueWorkFunc
}

func (ee *integrationExEnv) Group() workq.GroupID {
	if len(ee.groupStack) == 0 {
		return workq.InvalidGroupID
	}
	return ee.groupStack[len(ee.groupStack)-1]
}

func (ee *integrationExEnv) PushGroup(group workq.GroupID) {
	ee.groupStack = append(ee.groupStack, group)
}

func (ee *integrationExEnv) PopGroup() {
	if len(ee.groupStack) == 0 {
		panic("group stack underflow")
	}
	ee.groupStack = ee.groupStack[:len(ee.groupStack)-1]
}

func (ee *integrationExEnv) QueueFunc() workq.QueueWorkFunc {
	if len(ee.queueFnStack) == 0 {
		return nil
	}
	return ee.queueFnStack[len(ee.queueFnStack)-1]
}

func (ee *integrationExEnv) PushQueueFunc(queueFn workq.QueueWorkFunc) {
	if queueFn == nil {
		panic("queueFn is nil")
	}
	ee.queueFnStack = append(ee.queueFnStack, queueFn)
}

func (ee *integrationExEnv) PopQueueFunc() {
	if len(ee.queueFnStack) == 0 {
		panic("queue function stack underflow")
	}
	ee.queueFnStack = ee.queueFnStack[:len(ee.queueFnStack)-1]
}

type topLevelExEnv struct {
	integrationExEnv
	mu        sync.Mutex
	workQueue *workq.Accepted
}

// topLevelExEnvPool recycles the per-top-level-dispatch execution environments. A
// fresh one was allocated on every top-level Submit (and never reused); pooling it —
// together with the meta and ctxpool child freed in [releaseTopLevelContext] —
// removes that per-dispatch allocation. Get/Put are paired by topLevelCtxMeta
// (owned) and releaseTopLevelContext.
var topLevelExEnvPool = omnipool.For[topLevelExEnv]()

// Reset implements omnipool.Resetter. It clears the reusable fields but deliberately
// does NOT touch mu: omnipool's default (zeroing via *ee = *new(T)) would copy the
// mutex, so a Reset that leaves the (released-while-unlocked) mutex in place keeps
// the type pool-safe and preserves the group/queue stack capacity.
func (ee *topLevelExEnv) Reset() {
	ee.workQueue = nil
	ee.groupStack = ee.groupStack[:0]
	ee.queueFnStack = ee.queueFnStack[:0]
}

func (ee *topLevelExEnv) Lock() {
	ee.mu.Lock()
}

func (ee *topLevelExEnv) Unlock() {
	ee.mu.Unlock()
}

func (ee *topLevelExEnv) ExecuteNowOrQueue(ctx context.Context, ex workq.Execution, work workq.Work) error {
	return ee.workQueue.ExecuteNowOrQueue(ctx, ex, work)
}

// metaFromContext returns the ctxMeta stamped on ctx (and whether one was found). It
// is the single READ seam for the meta-on-context lookup: every meta is carried as a
// ctxpool child's value (body borrows and the derivations in ensureCtxMeta alike), so
// the lookup is a single ctxpool.GetValue — the nearest child wins.
func metaFromContext(ctx context.Context) (*ctxMeta, bool) {
	return ctxpool.GetValue[*ctxMeta](ctx)
}

// ctxMeta returns the ctxMeta already stamped on ctx (for wave wv), validating
// ownership. It never creates a meta — callers use it where one must already be
// present (a body or driver ctx). The lookup is the unified read seam
// (metaFromContext, ctxpool-aware); no ctxMetaMap caching, which would alias a reused
// ctxpool body ctx. (Step toward retiring ctxMetaMap; see meta-context-migration.md.)
func (wv *Wave) ctxMeta(ctx context.Context) (context.Context, *ctxMeta) {
	traceRegion := "Wave.ctxMeta"

	meta, ok := metaFromContext(ctx)
	if !ok {
		panic("Context not associated with a wave")
	}
	if meta.wave != wv {
		if _, isParentWave := meta.parentWaves[wv]; isParentWave {
			panic("Context belongs to a child wave")
		}
		panic("Context belongs to a different wave")
	}

	trace.Logf(ctx, traceRegion, "ctxMeta=%v", meta)

	return ctx, meta
}

func (wv *Wave) ensureCtxMeta(
	ctx context.Context,
	updateFn func(context.Context, *ctxMeta) context.Context,
) (context.Context, *ctxMeta) {
	traceRegion := "Wave.ensureCtxMeta"

	// Source/parent meta via the read seam. The derived meta below is stamped onto a
	// fresh ctxpool child so a later metaFromContext resolves IT (nearest child wins).
	sourceMeta, _ := metaFromContext(ctx)
	if sourceMeta != nil {
		sourceMeta.vetNotExpiredPin()
	}

	ctxType := topLevelContext
	var parentWaves map[*Wave]struct{}
	var exEnv executionEnvironment
	if sourceMeta != nil {
		switch sourceMeta.wave {
		case wv:
			parentWaves = sourceMeta.parentWaves
			ctxType = sourceMeta.ctxType
			exEnv = sourceMeta.executionEnvironment
		case nil:
			// A wave-less meta (a top-level WithFlow scope): nothing to join
			// wave-wise — inherit its (nil) ancestry and derive as top-level.
			parentWaves = sourceMeta.parentWaves
		default:
			if _, isParentWave := sourceMeta.parentWaves[wv]; isParentWave {
				panic("Context belongs to a child wave")
			}
			parentWaves = make(map[*Wave]struct{}, len(sourceMeta.parentWaves)+1)
			maps.Copy(parentWaves, sourceMeta.parentWaves)
			parentWaves[sourceMeta.wave] = struct{}{}
		}
	}

	// wave is the owning/ambient wave for the derived meta; it equals wv on every
	// transition (same-wave keeps it, a cross-wave redirect re-roots it on wv and
	// records the source wave in parentWaves above). Drawn with the owner's ref
	// (newCtxMeta); the derivation pins its source, released when this meta's own
	// count drains (unrefMeta cascade).
	meta := newCtxMeta()
	meta.wave = wv
	meta.parent = sourceMeta
	refMeta(sourceMeta)
	meta.parentWaves = parentWaves
	meta.ctxType = ctxType
	meta.executionEnvironment = exEnv
	if sourceMeta != nil {
		meta.riders = sourceMeta.riders // flow riders inherit verbatim along derivations
	}

	// Stamp the derived meta onto a ctxpool child of ctx. The child descends from
	// the submit/drive ctx, so cancellation rides that ancestry — the Wave owns no
	// ctx (no AfterFunc(j.ctx) linkage; that was the wave-owned-ctx model we drop).
	ctx = ctxpool.WithValue(ctx, meta)
	// Record the child so unrefMeta can free it at refs==0. ownsExEnv defaults
	// false here and is set by the caller that knows the derivation shape
	// (topLevelCtxMeta's updateFn).
	meta.selfCtx = ctx

	if updateFn != nil {
		ctx = updateFn(ctx, meta)
	}

	trace.Logf(ctx, traceRegion, "ctxMeta=%v", meta)

	return ctx, meta
}

// checkCtxType should panic if the type is not allowed. The bool return reports
// whether topLevelCtxMeta MINTED a fresh meta (true) or reused one already on ctx
// (false). A caller that owns a freshly minted top-level meta — and whose body does
// not descend from the meta-stamped ctx (the Launcher path roots the body at the
// original caller ctx) — passes that ctx to [releaseTopLevelContext] once the
// synchronous dispatch completes, recycling the meta + exEnv + ctxpool child.
func (wv *Wave) topLevelCtxMeta(
	ctx context.Context, checkCtxType func(ctxType contextType),
) (context.Context, *ctxMeta, bool) {
	traceRegion := "Wave.topLevelCtxMeta"

	// Lazy-init chokepoint: every dispatch (Launcher via vetStart, Skimmer/Funnel
	// via their unified submit path) and every skim (via skimCtxMeta) lands here, so
	// a zero-value Wave is brought up — or re-armed after a prior drain — exactly
	// once before its substrate is touched.
	wv.ensureInit()

	// Reuse a same-wave meta already on ctx — a body's own meta (dispatching from
	// inside a Skim/Accumulate body), or this wave's top-level/skim meta — rather
	// than re-deriving a copy. Keeps the parent chain short and skips a needless
	// borrow. ensureCtxMeta below handles the no-meta (fresh top-level) and
	// cross-wave cases (where a topLevelExEnv must be stamped). A reused meta is owned
	// by its borrower, so the caller must NOT release it.
	if m, ok := metaFromContext(ctx); ok && m.wave == wv && m.executionEnvironment != nil {
		checkCtxType(m.ctxType)
		return ctx, m, false
	}

	ctx, meta := wv.ensureCtxMeta(ctx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			checkCtxType(meta.ctxType) // Avoid stamping if invalid
			if meta.executionEnvironment == nil {
				exEnv := topLevelExEnvPool.Get()
				exEnv.workQueue = &wv.workQueue
				meta.executionEnvironment = exEnv
				meta.ownsExEnv = true // this meta allocated it; it frees it on release
				trace.Logf(ctx, traceRegion, "created new topLevelExEnv=%p, ctxMeta=%v", exEnv, meta)
			}
			return ctx
		},
	)
	checkCtxType(meta.ctxType)
	return ctx, meta, true
}

// releaseTopLevelContext drops the dispatch's ownership of a meta that
// [Wave.topLevelCtxMeta] minted (reported owned==true). Recycling is deferred
// to unrefMeta's refcount: when bodies borrowed from the meta-stamped ctx are
// still in flight, the meta (and its ctxpool child, and the owned chain above
// it) survives on their parent refs and recycles when the last one completes —
// so, unlike its pre-refcount form, this is safe to call while async work
// still descends from ctx. The owned bare-ctx-skim chain (skim meta over a
// freshly minted top-level meta) releases through the same cascade: the skim
// meta's parent ref is the top-level meta's sole remaining count (see
// skimCtxMeta's ownership transfer). Mirrors [releaseBodyContext]. Safe to
// call with a ctx carrying no meta (no-op).
func releaseTopLevelContext(ctx context.Context) {
	if m, ok := metaFromContext(ctx); ok {
		unrefMeta(m)
	}
}

// The bool return is the owned signal (see [Wave.topLevelCtxMeta] /
// [releaseTopLevelContext]): true when this call minted the skim meta — and, for a
// bare-ctx skim, the underlying top-level meta too (linked via releaseParent) — so
// the caller releases the whole chain after the skim drive completes; false when an
// ambient skim meta was reused.
func (wv *Wave) skimCtxMeta(ctx context.Context) (context.Context, *ctxMeta, bool) {
	traceRegion := "Wave.skimCtxMeta"

	ctx, meta, ownedTop := wv.topLevelCtxMeta(ctx, func(ctxType contextType) {
		if ctxType != topLevelContext && ctxType != skimContext {
			panic(fmt.Sprintf("Skim called from %v context but allowed only by top-level or skim context", ctxType))
		}
	})
	if meta.ctxType == skimContext {
		// Reused an ambient skim meta (topLevelCtxMeta never mints skim), so ownedTop is
		// false here — nothing to release.
		return ctx, meta, ownedTop
	}

	// ensureCtxMeta mints a fresh ctxpool child for the skim meta, so it has a
	// distinct identity from the top-level meta automatically — the old
	// skimCtxMetaMap identity-fork is no longer needed. The skim meta reuses the
	// top-level meta's exEnv (ownsExEnv stays false).
	ctx, skimMeta := wv.ensureCtxMeta(ctx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			if meta.ctxType != topLevelContext {
				panic(fmt.Sprintf("context type %v is not valid for skim, expected top-level context", meta.ctxType))
			}
			if meta.executionEnvironment == nil {
				panic("top-level context missing executionEnvironment; required for skim")
			}
			meta.ctxType = skimContext
			trace.Logf(ctx, traceRegion, "registering new skim context, ctxMeta=%v", meta)
			return ctx
		},
	)
	if ownedTop {
		// The top-level meta was minted by THIS call, so it belongs to the same
		// owned chain: transfer the dispatch's ownership to the skim meta's
		// parent ref (taken in ensureCtxMeta) by dropping the mint ref. The
		// caller's single releaseTopLevelContext then frees both via the
		// unrefMeta cascade. Cannot reach zero here — the skim meta holds a ref.
		skimMeta.parent.refs.Add(-1)
	}

	if skimMeta.ctxType != skimContext {
		panic(fmt.Sprintf("context type %v is not valid for skim, expected skim context", skimMeta.ctxType))
	}
	return ctx, skimMeta, true
}
