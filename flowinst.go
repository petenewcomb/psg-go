// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/workq"
)

// flowInstance is the lifetime identity behind one FollowUp registration
// (docs/decisions/flow-design.md, "Lifetime semantics"). Instances are fully
// internal — no user handle exists; the minted key/tag is only the shaping
// identity. One instance is created per FollowUp option per WithFlow scope,
// and its reference count tracks the carriers of that registration:
//
//   - +1 held by the registering scope from entry to exit (the lexical cover
//     that makes the attach window race-free — parent-covers-children);
//   - +1 per work item whose body ctx was borrowed under a rider set
//     containing the instance (taken at dispatch inside borrowBodyContext,
//     released at completion inside releaseBodyContext);
//   - +1 per ENCLOSING instance held by each inner (later-registered) instance
//     from registration until the inner's single fire completes (holds below) —
//     the peel that makes an outer follow-up wait for the whole nested subtree.
//
// The follow-up fires EXACTLY ONCE, when the count reaches zero (a single
// atomic transition — one winner). Its own rider is PEELED by construction — its
// binding lives on its own chain node and the fire carries node.next (enclosing
// below) — so its dispatches cannot re-reference it: nothing re-fires it, and
// re-extending the flow under its identity is an explicit re-stamp inside the
// body. The fire carries the ENCLOSING chain (enclosing), so its extensions hold
// the outer instances — which is why an outer cannot reach zero (cannot fire)
// until this instance's fire and everything it spawned have drained (LIFO nesting).
//
// Instances are currently GC-owned; pooling arrives with a later allocation
// pass (a firing is cold — once per flow end — so the alloc is off the hot path).
type flowInstance struct {
	// fn is the type-erased user follow-up (FlowKey/FlowTag.FollowUp wrap the
	// typed handler into this shape). It receives the bundle value (val below)
	// and returns the follow-up's error.
	fn func(ctx context.Context, value any) error
	// val is the bundle value passed to fn — the key's value, or nil for a tag
	// or a valueless key. Captured at registration (buildFlowRiders).
	val any
	// enclosing is the ENCLOSING rider chain the fire ctx carries: the node.next
	// below this instance's own node — the values and follow-up instances
	// registered before it (this instance PEELED by construction, its value
	// delivered as fn's argument instead). fn's dispatches inherit it, so they hold
	// the outer instances — the nested-lifetime coupling — while never
	// re-referencing this instance.
	enclosing *flowRiderNode
	// holds is the enclosing instances this (inner) instance references from
	// registration until its fire completes, released in fire(). Bridges the gap
	// before the fire's own fnRiders ref takes over, so an outer never reaches
	// zero out from under a not-yet-fired inner regardless of unref order.
	holds []*flowInstance
	// definitional marks an instance minted for a tag's DEFINITIONAL follow-up
	// (bound at NewFlowTag): one per flow, found-by-id on the chain walk so repeated
	// infusion is idempotent, and (CP-R6b) the coalescing target at a fan-in.
	definitional bool
	// id is the tag identity behind a definitional instance — nil for every other
	// instance. It reaches id.mergeMu (the per-tag coalescing lock) at count→0, and
	// id.definitionalFn is what fn already wraps. Set at buildFlowRiders.
	id *flowIdentity
	// shared is this definitional instance's node in the coalescing union-find
	// hierarchy (CP-R6b), nil until it first merges with an independent flow's
	// instance at a funnel. While nil the instance fires on its own count→0 (the
	// common case); once set, count→0 instead derefs the shared tree and the fire
	// happens once, at the component root. Read/written only under id.mergeMu.
	shared *sharedNode
	count  atomic.Int64
}

// flowInstancePool recycles flowInstance values. A follow-up fires exactly once
// at count→0 (CP-F6), and retain-of-a-scope-ctx is undefined, so after the fire
// no live chain can reference the instance again — it is recycled
// generation-free at fire completion, needing no ABA guard
// (docs/decisions/flow-rider-chain.md, "Pooling summary").
var flowInstancePool = omnipool.For[flowInstance]()

// Reset zeroes the instance for reuse. It is the omnipool recycle hook, used
// instead of a plain struct copy because count (atomic.Int64) carries noCopy.
func (in *flowInstance) Reset() {
	in.fn = nil
	in.val = nil
	in.enclosing = nil
	in.holds = nil
	in.definitional = false
	in.id = nil
	in.shared = nil
	in.count.Store(0)
}

func (in *flowInstance) ref() {
	in.count.Add(1)
}

// buildFireChain assembles, AT THE COUNT→0 DISPATCH, the rider chain the fire
// runs under as the carrier's continuation: the carrier chain minus the fired
// binding (F6: no re-fire, no self-presence), with the fire's own refs already
// taken. It MUST run at the dispatch site, not at fire-run, because cover for
// instance refs is positional and momentary: every release walk
// (flowUnrefRiders, the scope-exit LIFO loop, the holds cascade) drops its
// instance refs in chain order head→tail, so when the fired binding's node
// triggers this call, the walker's refs on the SUFFIX (below the fired node)
// are still held — those instances are provably alive and safely re-ref'd —
// while PREFIX instances (above it: bindings the carrier acquired after this
// registration) may already have fired and recycled in the same walk. Prefix
// nodes are therefore copied VALUE-ONLY (id + val kept for reads, inst
// dropped): the fire still sees post-registration values and tag presence,
// but neither pins nor re-fires prefix follow-ups.
//
// Fired-binding matching is by instance, or by tag identity for a
// definitional instance — and ALL matching nodes are peeled, not just one: a
// fan-in union chain carries one node per coalesced leaf instance of the same
// tag (R6b), all of them one fired component, and any of them other than the
// one that closed the component may already be a stepped-aside, recycled
// instance. The partition point is therefore the DEEPEST matching node: the
// component's closing ref cannot be released later than it in the walk, so
// everything below it is still walk-covered and safely shared+ref'd, while
// everything above it — including live non-matching bindings between the
// closing node and the deepest match — is conservatively value-only. (The
// conservatism only costs lifetime pinning: a fire-dispatched body does not
// extend such a sibling's flow. Observability must not delay fires — same
// trade as the flush pin.) No match (not reachable from today's sites, which
// all release the fired binding through the carrier's own chain) degrades to
// the same value-only treatment for the whole chain.
//
// The returned head carries one node ref (the fire meta's) and one instance
// ref per suffix follow-up — exactly what releaseBodyContext releases at fire
// end (value-only prefix nodes have no inst and are skipped by
// flowUnrefRiders).
func buildFireChain(chain *flowRiderNode, fired *flowInstance) *flowRiderNode {
	matches := func(n *flowRiderNode) bool {
		if fired.definitional {
			return n.id == fired.id
		}
		return n.inst == fired
	}
	var target *flowRiderNode // deepest matching node
	for n := chain; n != nil; n = n.next {
		if matches(n) {
			target = n
		}
	}
	var suffix *flowRiderNode
	if target != nil {
		suffix = target.next
	}
	flowRefRiders(suffix) // covered: the walker's suffix refs are still held here
	// Rebuild above target: peel every matching node, value-only copies for the
	// rest (recursion depth = chain length, short by construction); each copy
	// takes its own downlink ref via newRiderNode.
	var rebuild func(n *flowRiderNode) *flowRiderNode
	rebuild = func(n *flowRiderNode) *flowRiderNode {
		if n == target {
			return suffix
		}
		if matches(n) {
			return rebuild(n.next) // a coalesced sibling node of the fired binding
		}
		return newRiderNode(n.id, n.val, n.hasVal, nil, rebuild(n.next))
	}
	head := rebuild(chain)
	nodeRef(head) // the fire meta's node ref (released by releaseBodyContext)
	return head
}

// unref releases one carrier; the release that reaches zero fires the follow-up
// EXACTLY ONCE. inline selects how fn runs:
//   - true (WithFlow scope exit only): directly on the caller's own frame; fn's
//     error is RETURNED, up to WithFlow's join. wave is nil.
//   - false (work-item or fire completion): a wave-rooted fire dispatched to the
//     executor — those paths run inside Free/release machinery where user code
//     must not run (an fn draining its wave would deadlock). wave is the finishing
//     item's wave; it is kept alive across the hop (IncrementReference, sound
//     because the triggering item's own work reference has not yet dropped) and
//     the fire's error routes to its errSink. Returns nil (the fire is async).
//
// carrier is the meta whose release dropped this ref — the fire, if this is
// the final ref, runs as that carrier's CONTINUATION (driver-contexts.md,
// "Fire: the last carrier's continuation"): its riders are the carrier's chain
// minus the fired binding. Every count→0 site passes it from a synchronous
// safe point where the carrier's owner ref is still held (rider release
// precedes the meta release at every site), so the dispatch pin below is
// sound. nil when no carrier context exists (work freed without executing on
// a teardown path) — the fire then falls back to the instance's
// enclosing-at-registration set.
func (in *flowInstance) unref(inline bool, wave *Wave, carrier *ctxMeta) error {
	if in.count.Add(-1) != 0 {
		return nil
	}
	if in.definitional {
		// A definitional instance may have coalesced with independent flows'
		// instances at a funnel (CP-R6b). If so, its count→0 is not its own fire: it
		// derefs the shared component and only the branch that closes the component
		// fires — once. coalesceAtZero returns false when this branch merely stepped
		// aside (it already released its enclosing chain and recycled itself); true
		// when this branch runs the single fire (with the component's accumulated
		// holds adopted onto in.holds) or when it never merged at all.
		if !in.coalesceAtZero() {
			return nil
		}
	}
	if inline {
		// The inline scope-exit fire always takes the COW path (the scope meta —
		// the carrier — is alive but still referenced; its own release runs
		// after the fires). The fire ctx roots at Background: a fire is
		// end-of-flow work, shielded from cancellation by design (see
		// flowFireWork.Run).
		var riders *flowRiderNode
		var parent *ctxMeta
		if carrier != nil {
			riders = buildFireChain(carrier.riders, in) // refs pre-taken
			parent = carrier.parent
		} else {
			riders = in.enclosing
			flowRefRiders(riders)
			nodeRef(riders)
		}
		//nolint:contextcheck // scope-exit fire runs on the caller's own frame
		ctx, m := newBorrowedMeta(context.Background(), parent, nil, topLevelContext)
		m.riders = riders // refs arrive with the chain (released by runFire's releaseBodyContext)
		err := in.runFire(ctx, true, nil, nil)
		nodeUnref(in.enclosing)  // release the instance's own enclosing ref (fire done)
		flowInstancePool.Put(in) // fire complete; count is 0 forever, no reader remains
		return err
	}
	// Keep the wave alive across the hop with the CONDITIONAL pin: the triggering
	// item's own work reference has not yet dropped (every count→0 site releases
	// riders before its owner ref), so the count is nonzero and no Flushing→Done
	// claim (wavestate.ClaimZero) can be in flight — the pin must succeed. A plain
	// IncrementReference would resurrect a claimed count silently if that ordering
	// invariant were ever broken; failing loudly here is the tripwire that keeps
	// it honest.
	if !wave.state.TryIncrementReference() {
		panic("streampool: flow fire dispatched against a wave already committed to Done " +
			"(a count→0 site dropped its owner reference before its riders)")
	}
	wk := flowFireWorkPool.Get()
	wk.Init(workq.NewGroupID())
	wk.inst = in
	wk.wave = wave
	if carrier != nil && carrier.wave == wave {
		// Pin the carrier meta (and, via the cascade, its whole parent chain)
		// until the fire completes — the driver chain stays walkable DURING the
		// fire, by design — and build the fire's rider chain NOW, at the
		// synchronous safe point where suffix cover still holds (see
		// buildFireChain). The chain crosses to the fire with its refs already
		// taken.
		refMeta(carrier)
		wk.carrierMeta = carrier
		wk.fireRiders = buildFireChain(carrier.riders, in)
		wk.fireRidersSet = true
	}
	defaultPool.ForceFresh(wk)
	return nil
}

// runFire runs fn once under bodyCtx (which carries fnRiders — the enclosing set —
// so fn's dispatches hold the outer instances), delivers fn's error (returned when
// onErr is nil, else handed to onErr while bodyCtx is still live), then releases
// bodyCtx and finally this instance's holds on the enclosing instances (which may
// cascade to fire an outer). The holds release AFTER releaseBodyContext (defer
// ordering), so each outer's count is covered until this fire is fully done — no
// outer fires early regardless of unref order. inline/wave describe how a cascaded
// outer fires; an inline outer's error joins here (own error first). A panic in fn
// propagates, like every user body.
func (in *flowInstance) runFire(
	bodyCtx context.Context, inline bool, wave *Wave, onErr func(error),
) (err error) {
	// A cascaded outer fire's last carrier is THIS fire: pin the fire meta AND
	// its rider chain nodes across the cascade, which runs after
	// releaseBodyContext has dropped the meta's owner ref and the chain's
	// node/instance refs — without the node pin, the outer's buildFireChain
	// would walk freed nodes. Instance-ref cover for the outer's suffix needs
	// no pin here: those are exactly the instances the outer's own holds (its
	// registration refs) keep alive until ITS fire completes.
	fireMeta, _ := metaFromContext(bodyCtx)
	fireRiders := fireMeta.riders
	refMeta(fireMeta)
	nodeRef(fireRiders)
	// Deferred first → runs last: release the enclosing holds only after
	// releaseBodyContext.
	//nolint:contextcheck // a cascaded async fire roots at the scheduler ctx by design
	defer func() {
		holds := in.holds
		in.holds = nil
		for _, out := range holds {
			if e := out.unref(inline, wave, fireMeta); e != nil {
				err = errors.Join(err, e)
			}
		}
		nodeUnref(fireRiders)
		unrefMeta(fireMeta)
	}()
	defer releaseBodyContext(bodyCtx)
	fnErr := in.fn(bodyCtx, in.val)
	if onErr != nil {
		if fnErr != nil {
			onErr(fnErr) // route to the wave errSink while bodyCtx is still live
		}
	} else {
		err = fnErr
	}
	return err
}

// ─────────────────────────────────────────────────────────────────────────────
// Coalescing (CP-R6b): definitional-tag follow-ups across INDEPENDENT flows.
//
// A definitional follow-up fires once per flow (R6a) — trivially, within a shared
// chain, because every infusion dedups to one instance. But when flows with no
// common ancestor each infuse the same tag, they arrive as SEPARATE instances; left
// alone each fires. The unit of aggregation is a funnel INSTANCE: a flow is defined
// by its data, not its operations, so the set of items one funnel instance
// accumulates IS one aggregated flow, and its definitional follow-up must fire once.
// Independent flows that co-accumulate in the same instance therefore coalesce into
// one lifetime; flows that land in DIFFERENT instances are different flows and fire
// separately (correctly — whether two independent submits meet in one instance is a
// runtime property, since submit runs inline or async, not a merge we force).
//
// Coalescing merges the co-accumulated instances via a serial union-find under the
// tag's mergeMu: the instances are the leaves (an explicit leaf would be 1:1 with
// its instance, so there is none — inst.shared points straight at a merge parent),
// every merge links two roots under a fresh parent, and the follow-up fires once
// when the component root's refs reach zero. The merge happens in collectFlowTags —
// the funnel is the ONLY merge site (skim is a continuation, a nested subwave shares
// an ancestor — docs/decisions/flow-rider-chain.md); the day another op introduces a
// fan-in, mergeDefinitional's "only merge site" assumption reopens.
// ─────────────────────────────────────────────────────────────────────────────

// sharedNode is a node in the coalescing union-find hierarchy. refs counts the
// live things pointing here: definitional instances whose shared field references
// this node, plus child nodes whose parent references it. Every field is
// read/written only under the owning tag's mergeMu (reached via a member
// instance's id), so refs is a plain int, not an atomic.
type sharedNode struct {
	parent *sharedNode
	refs   int
	// holds accumulates the enclosing-instance holds of every merged branch,
	// migrated up the tree as branches complete (derefShared) and released once at
	// the component fire, so each branch's outer follow-ups unblock — inner-holds-
	// outer carried across the fan-in.
	holds []*flowInstance
}

// sharedNodePool recycles shared nodes; freeSharedNode returns them as components
// dissolve. flowSharedAllocHook is the conservation seam (mirrors flowNodeAllocHook).
var sharedNodePool = omnipool.For[sharedNode]()

// Reset is the omnipool recycle hook: drop the slice backing promptly and zero the
// node for reuse.
func (s *sharedNode) Reset() {
	s.parent = nil
	s.refs = 0
	s.holds = nil
}

var flowSharedAllocHook atomic.Pointer[func(int)]

func flowSharedAlloc(delta int) {
	if h := flowSharedAllocHook.Load(); h != nil {
		(*h)(delta)
	}
}

func newSharedNode() *sharedNode {
	s := sharedNodePool.Get()
	flowSharedAlloc(1)
	return s
}

func freeSharedNode(s *sharedNode) {
	flowSharedAlloc(-1)
	sharedNodePool.Put(s)
}

// findRoot walks parent pointers to the component root (no path compression — a
// deref is O(component depth) and merges are rare). Caller holds the tag's mergeMu.
func findRoot(s *sharedNode) *sharedNode {
	for s.parent != nil {
		s = s.parent
	}
	return s
}

// mergeDefinitional coalesces two live definitional instances of the same tag into
// one component. Caller holds a.id.mergeMu (== b.id.mergeMu; same tag). Both
// operands are provably LIVE — the accumulate item and the funnel each hold a ref,
// so count>0 and neither can be reaching count→0 concurrently (ref-before-release,
// so there is no merge-vs-death race). Idempotent: already-shared roots are a
// no-op, which is the common case (a funnel re-meets the same instances every
// item). refs invariant: a node counts its direct instances plus its child nodes.
func mergeDefinitional(a, b *flowInstance) {
	switch {
	case a.shared == nil && b.shared == nil:
		p := newSharedNode()
		p.refs = 2 // a and b both anchor here
		a.shared = p
		b.shared = p
	case a.shared == nil:
		r := findRoot(b.shared)
		r.refs++ // a joins the component as a direct instance
		a.shared = r
	case b.shared == nil:
		r := findRoot(a.shared)
		r.refs++
		b.shared = r
	default:
		ra := findRoot(a.shared)
		rb := findRoot(b.shared)
		if ra == rb {
			return // already one component
		}
		p := newSharedNode()
		p.refs = 2 // ra and rb become its children
		ra.parent = p
		rb.parent = p
	}
}

// derefShared drops one ref on s and cascades: a node reaching zero migrates its
// accumulated holds up to its parent and is freed, then the parent is dereffed. It
// returns (root, true) when the cascade reaches a root (no parent) at zero — the
// whole aggregated flow is done, and the caller fires once and frees root — or
// (nil, false) when a node stops above zero (the component is still live). Caller
// holds the tag's mergeMu.
func derefShared(s *sharedNode) (*sharedNode, bool) {
	for {
		s.refs--
		if s.refs > 0 {
			return nil, false
		}
		if s.refs < 0 {
			panic("streampool: shared coalesce node refs underflow")
		}
		if s.parent == nil {
			return s, true // component root reached zero → fire once
		}
		p := s.parent
		p.holds = append(p.holds, s.holds...)
		s.holds = nil
		freeSharedNode(s)
		s = p
	}
}

// coalesceAtZero runs at a definitional instance's count→0, under its tag's
// mergeMu. It decides whether THIS instance runs the follow-up's single fire:
//
//   - never merged (shared == nil, the common case) → true: fire directly, on its
//     own holds and enclosing, exactly as a non-coalesced definitional instance;
//   - merged, but the component is still live after this branch's deref → false:
//     the branch has stepped aside — its holds are migrated into the tree (released
//     later at the component fire), its enclosing chain released, and it is
//     recycled here; the caller just returns;
//   - merged, and this branch closed the component → true: it adopts the whole
//     component's accumulated holds onto in.holds and fires once (under its own
//     enclosing — a fan-in has no single canonical context; the last-standing
//     branch, typically the downstream output flow, is the principled "true end").
//
// The fire itself runs in the caller AFTER mergeMu is released — user code never
// runs under the lock.
func (in *flowInstance) coalesceAtZero() (fire bool) {
	mu := &in.id.mergeMu
	mu.Lock()
	if in.shared == nil {
		mu.Unlock()
		return true
	}
	s := in.shared
	s.holds = append(s.holds, in.holds...)
	in.holds = nil
	root, done := derefShared(s)
	if !done {
		mu.Unlock()
		nodeUnref(in.enclosing) // this branch will not fire; drop its enclosing chain
		flowInstancePool.Put(in)
		return false
	}
	in.holds = root.holds // every branch's holds, released by this single fire
	root.holds = nil
	freeSharedNode(root)
	mu.Unlock()
	return true
}

// flowRefRiders / flowUnrefRiders take and release one carrier reference on
// every follow-up instance in a rider chain (each instance appears on at most one
// node per chain, so this is one ref per instance) — the instance-firing count,
// distinct from the node-pooling refs a carrier takes via nodeRef/nodeUnref at the
// same sites. Paired by construction: borrowBodyContext and fire ref;
// releaseBodyContext unrefs. Derived metas (ensureCtxMeta) inherit chains WITHOUT
// either ref (synchronous extents covered by their enclosing carrier); the flush
// fan-in clone ADOPTS both refs from the funnel (flowFanInContext) and releases
// them at flush end via releaseBodyContext.
func flowRefRiders(r *flowRiderNode) {
	for n := r; n != nil; n = n.next {
		if n.inst != nil {
			n.inst.ref()
		}
	}
}

// flowUnrefRiders releases the carrier refs of r against wave — the wave of the
// context being released (releaseBodyContext reads it from the meta before
// teardown). A release that ends an instance's flow dispatches a wave-rooted
// fire; the unref return is always nil on this async path (the fire routes its own
// error to wave's errSink). carrier is the meta being released — the last
// carrier whose continuation a fire dispatched here runs as; callers pass it
// while its owner ref is still held (nil only on teardown paths with no
// context, e.g. work freed without executing).
func flowUnrefRiders(r *flowRiderNode, wave *Wave, carrier *ctxMeta) {
	for n := r; n != nil; n = n.next {
		if n.inst != nil {
			_ = n.inst.unref(false, wave, carrier)
		}
	}
}

// flowErrSink is the framework-owned, wave-agnostic error sink for follow-up
// firings (the funnelErrSink shape): its handler returns the error as-is so it
// surfaces via the target wave's SkimAll path. A single package-level sink serves
// every follow-up on every wave — the firing supplies the target wave.
var flowErrSink = newInternalSkimmer[struct{}](NewErrHandler(func(_ context.Context, err error) error {
	return err
}))

// flowFireWork carries a wave-rooted follow-up firing to the executor (the
// funnelInstance pattern): the fire runs user code, so it gets a pool worker and
// never runs inside completion/release machinery. It embeds a bare workq.WorkItem
// (not poolWork) — the wave is kept alive by the IncrementReference the dispatch
// took, dropped in Run — and rides borrowSrcCtx, the stable scheduler ctx stashed
// by Execute, so the fire body ctx roots at the wave/scheduler, not a recycled
// per-item ctx.
type flowFireWork struct {
	workq.WorkItem
	inst         *flowInstance
	wave         *Wave
	borrowSrcCtx context.Context //nolint:containedctx // borrow source for the fire body ctx
	// borrowSrcMeta is the meta on borrowSrcCtx, resolved and ref-pinned in
	// Execute (the synchronous safe point) for Run to borrow from — same
	// discipline as funnelInstance.borrowSrcMeta.
	borrowSrcMeta *ctxMeta
	// carrierMeta is the LAST CARRIER — the meta whose release dropped the
	// final carrier ref — pinned at the count→0 dispatch (refMeta; see unref).
	// The fire runs as its continuation: same tree position, riders =
	// fireRiders, the chain buildFireChain assembled at the dispatch (the
	// carrier chain minus the fired binding, refs pre-taken —
	// driver-contexts.md, "Fire"). fireRidersSet distinguishes a legitimately
	// empty fire chain from the no-carrier teardown case (carrierMeta nil),
	// where Run falls back to the instance's enclosing-at-registration set.
	carrierMeta   *ctxMeta
	fireRiders    *flowRiderNode
	fireRidersSet bool
}

// Execute is the scheduler side: stash the borrow source for Run — ctx plus its
// meta, pinned here at the synchronous safe point (before the publishing
// handoff, mirroring funnelInstance.Execute) — then hand the fire to the
// executor. TryPushBack first; when the scheduler worker is prepared to park, a
// blocking PushBack. A path that does not hand off drops the pin; the retry
// re-pins.
func (wk *flowFireWork) Execute(ctx context.Context, ex workq.Execution) error {
	srcMeta, _ := metaFromContext(ctx)
	refMeta(srcMeta)
	wk.borrowSrcCtx = ctx
	wk.borrowSrcMeta = srcMeta
	if bodyExecutor.TryPushBack(wk) {
		ex.Starting()
		return nil
	}
	if !ex.ShouldBlockOrPostpone() {
		unrefMeta(srcMeta)
		return nil // postpone; retried (and blocked) when the scheduler worker parks
	}
	err := bodyExecutor.PushBack(ctx, wk)
	if err == nil {
		ex.Starting()
	} else {
		unrefMeta(srcMeta)
	}
	return err
}

// Run is the execpool.Task entry: recycle the shell first, then run the fire on
// this executor worker under a wave-rooted body ctx, routing fn's error to the
// wave's errSink, and finally drop the keep-alive reference the dispatch took.
//
//nolint:contextcheck // src is the borrow source for the fire body ctx, not a propagated arg
func (wk *flowFireWork) Run(ee *workerExEnv) {
	inst := wk.inst
	wave := wk.wave
	src := wk.borrowSrcCtx
	srcMeta := wk.borrowSrcMeta
	carrier := wk.carrierMeta
	fireRiders := wk.fireRiders
	fireRidersSet := wk.fireRidersSet
	wk.inst = nil
	wk.wave = nil
	wk.borrowSrcCtx = nil
	wk.borrowSrcMeta = nil
	wk.carrierMeta = nil
	wk.fireRiders = nil
	wk.fireRidersSet = false
	flowFireWorkPool.Put(wk)

	// The fire body ctx is the LAST CARRIER's continuation
	// (driver-contexts.md, "Fire"): the carrier's tree position (same parent),
	// riders = fireRiders (the carrier's chain minus the fired binding, built
	// and ref'd at the dispatch) — so the fire sees riders the carrier
	// acquired after registration. Its ctx ANCESTRY stays rooted at the stable
	// scheduler ctx (src) in every arm: a fire is end-of-flow work with its
	// own error routing, deliberately SHIELDED from a long-gone submitter's
	// cancellation (the resolution of the design record's open point —
	// cleanup semantics: a fire, e.g. an otel span end, must run even when
	// the request that spawned the flow was canceled).
	var bodyCtx context.Context
	var m *ctxMeta
	switch {
	case !fireRidersSet:
		// No carrier context (teardown-freed work): the instance's
		// enclosing-at-registration set, borrowed from the pinned scheduler meta.
		bodyCtx, m = newBorrowedMeta(src, srcMeta, wave, skimContext)
		m.riders = inst.enclosing
		flowRefRiders(m.riders)
		nodeRef(m.riders) // the fire meta's carrier ref (released by runFire's releaseBodyContext)
	case carrier.refs.Load() == 1:
		// Sole holder is our dispatch pin: the carrier's owner release has
		// completed and no async children survive — custody has RETURNED, so
		// ADOPTING and mutating in place satisfies the immutability invariant
		// rather than excepting it. Position fields (parent + its ref,
		// parentWaves, wave) stay; execution fields are re-stamped for the
		// fire extent: worker ee (exEnv is NEVER carried across extents), no
		// held handle (the carrier's was released with its body), permitRoot
		// (the fire runs on a fungible worker), skim ctxType (fires are
		// skim-class for the nesting vet). The selfCtx is re-homed onto the
		// scheduler ctx for the shielded ancestry above (sole custody: nothing
		// can still resolve the old child). Our dispatch pin becomes the
		// meta's owner ref, dropped by runFire's releaseBodyContext.
		m = carrier
		m.riders = fireRiders // refs arrived with the chain
		m.held = nil
		m.permitRoot = true
		m.ctxType = skimContext
		ctxpool.Free(m.selfCtx)
		bodyCtx = ctxpool.WithValue(src, m)
		m.selfCtx = bodyCtx
	default:
		// Other holders remain (async children of the carrier are still
		// running): COPY-ON-WRITE sibling — a fresh pooled meta at the SAME
		// tree position (carrier's parent, ref'd by the borrow), the fire
		// chain's refs adopted, a fresh execution stamp, never the carrier's
		// exEnv.
		bodyCtx, m = newBorrowedMeta(src, carrier.parent, wave, skimContext)
		m.parentWaves = carrier.parentWaves
		m.riders = fireRiders // refs arrived with the chain
		unrefMeta(carrier)    // drop the dispatch pin; the sibling holds its own parent ref
	}
	m.executionEnvironment = ee
	unrefMeta(srcMeta) // the borrow holds its own parent ref now; drop the Execute pin

	// runFire returns nil here (onErr routes the error); the async fire owns it.
	_ = inst.runFire(bodyCtx, false, wave, func(fnErr error) {
		ctx2, meta := wave.ctxMeta(bodyCtx)
		if e := flowErrSink.submit(ctx2, meta, workq.NewGroupID(), struct{}{}, fnErr); e != nil &&
			ctx2.Err() == nil {
			panic(fmt.Sprintf("streampool: unexpected error routing follow-up error: %v", e))
		}
	})
	nodeUnref(inst.enclosing)  // release the instance's own enclosing ref (fire done)
	flowInstancePool.Put(inst) // fire complete; count is 0 forever, no reader remains
	wave.state.DecrementReference()
}

// Free is a no-op: the controller calls it right after Execute's successful
// handoff, possibly concurrently with Run, so it must not touch the shell
// (the funnelInstance precedent).
func (wk *flowFireWork) Free() {}

var flowFireWorkPool = omnipool.For[flowFireWork]()
