// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// ─────────────────────────────────────────────────────────────────────────────
// Flows — riders on the causal DAG (docs/decisions/flow-design.md).
//
// A flow is the causal DAG of work the framework already maintains: nodes are
// work items, edges are submits (plus the funnel accumulate→flush fan-in).
// Flows always exist and are never constructed; the API below only shapes
// *riders* — values and completion hooks that propagate along the DAG's edges.
//
// Riders come in two kinds, split by whether a merge operator exists at
// fan-in. Values ([FlowKey]) are path-scoped: inherited verbatim along
// dispatch chains and severed at fan-in, where no merge is truthful. Follow-up
// lifetimes ([FlowTag]) are DAG-scoped: reference counts merge trivially, so
// they union through everything, including funnels.
//
// Mechanically, the rider set is a linked chain of one-binding nodes headed by
// one pointer on the pooled ctxMeta, walked head→next on read
// (docs/decisions/flow-rider-chain.md). Dispatches copy the head pointer
// (borrowBodyContext, ensureCtxMeta); a registering [WithFlow] scope allocates
// only the nodes for its own additions and links them ahead of the inherited
// head; the funnel flush severs to the tag union. The hot path pays one pointer
// copy. Nodes are immutable once published, so any number of goroutines walk a
// node concurrently without synchronization.
// ─────────────────────────────────────────────────────────────────────────────

type flowIdentKind int8

const (
	flowKeyIdent flowIdentKind = iota + 1 // path-scoped, carries a value
	flowTagIdent                          // DAG-scoped, valueless
)

// flowIdentity is the shared identity cell behind [FlowKey] and [FlowTag]:
// the *flowIdentity pointer is the identity. It deliberately has nonzero size
// — zero-size allocations share an address in Go, which would make every
// minted key identical.
type flowIdentity struct {
	kind flowIdentKind
	// definitionalFn is a tag's DEFINITIONAL follow-up, bound at declaration
	// (NewFlowTag(FlowFollowUp(h))): it fires ONCE per flow carrying the tag,
	// regardless of how many points infuse it. nil for a plain tag or a key. The
	// per-flow instance is minted at the first infusion and found-by-id in a shared
	// chain thereafter; across independent flows converging at a funnel it
	// coalesces (CP-R6b). Distinct from per-scope [FlowTag.FollowUp].
	definitionalFn func(ctx context.Context, value any) error
	// mergeMu serializes this tag's coalescing union-find (CP-R6b): every merge of
	// two independent definitional instances and every count→0 deref of one takes
	// it, so the shared-node tree is mutated single-threaded. Per-tag granularity —
	// unrelated tags never contend, and a plain tag/key never locks it. It covers
	// the residual the funnel mu cannot: two different funnels racing to union the
	// same still-unmerged roots, and sibling derefs happening outside any funnel.
	mergeMu sync.Mutex
}

// FlowKey identifies a path-scoped flow rider carrying a value of type V.
// Mint one with [NewFlowKey]; the zero FlowKey identifies nothing (reads
// return absent, registration panics).
//
// Path-scoped means the key's bundle — its value and any follow-ups — is
// inherited verbatim along dispatch chains and severed at fan-in (a funnel
// flush reads absent; the flush body re-asserts whatever is right).
//
// By convention, name the variable for the value it carries — requestCtx,
// tenant, txn — so use sites read as sentences about the value:
// txn.Value(t), txn.From(ctx). See docs/decisions/flow-design.md.
type FlowKey[V any] struct {
	id *flowIdentity
}

// NewFlowKey mints a path-scoped flow key whose bundle carries a value of
// type V. Minting is a cold-path allocation: declare keys at package level or
// once per unit of structure — one key shared across many flows or one per
// flow, at the user's discretion.
func NewFlowKey[V any]() FlowKey[V] {
	return FlowKey[V]{id: &flowIdentity{kind: flowKeyIdent}}
}

// FlowTag identifies a DAG-scoped flow rider. Mint one with [NewFlowTag]; the
// zero FlowTag identifies nothing.
//
// A tag is structurally valueless — it has no type parameter and no value
// slot, which is what makes a data-bearing DAG-scoped rider unrepresentable:
// data has no canonical merge at fan-in, while a tag's presence and its
// follow-up reference counts merge trivially and so cross funnels.
//
// By convention, name the variable for the flow it identifies — checkout,
// ingestion, audit — so use sites read naturally: checkout.InFlow(ctx),
// checkout.FollowUp(fn).
type FlowTag struct {
	id *flowIdentity
}

// NewFlowTag mints a DAG-scoped flow tag. See [FlowTag] and [NewFlowKey] on
// minting granularity.
//
// An optional [FlowFollowUp]/[FlowFollowUpFn] binds a DEFINITIONAL follow-up to
// the tag's identity: it fires ONCE per flow carrying the tag, no matter how many
// points infuse it (idempotent), and coalesces across independent flows that
// converge at a funnel. This differs from a per-scope [FlowTag.FollowUp] (a
// distinct lifetime per registration) and complements it. FlowFollowUp is just an
// option carrying the handler; here the tag binds it to its own identity.
func NewFlowTag(definitional ...FlowOption) FlowTag {
	id := &flowIdentity{kind: flowTagIdent}
	for i := range definitional {
		o := definitional[i]
		if o.kind != flowOptFollowUp || o.hasVal {
			panic("streampool: NewFlowTag accepts only a FlowFollowUp/FlowFollowUpFn option")
		}
		if id.definitionalFn != nil {
			panic("streampool: NewFlowTag accepts at most one definitional follow-up")
		}
		id.definitionalFn = o.fn
	}
	return FlowTag{id: id}
}

type flowOptionKind int8

const (
	flowOptValue flowOptionKind = iota + 1
	flowOptFollowUp
	flowOptInfuse
	flowOptSuppress
	flowOptDisconnect
)

// FlowOption configures a [WithFlow] scope. Obtain options from the methods on
// [FlowKey] and [FlowTag] (Value, FollowUp, Suppress) or from [Disconnect]; the zero
// FlowOption is invalid and panics when passed to WithFlow. It is a value type
// (no interface boxing), so a registering scope allocates nothing warm.
type FlowOption struct {
	kind flowOptionKind
	id   *flowIdentity
	val  any
	// hasVal marks that val is a real binding: true for a Value and for a key
	// follow-up (which carries its value), false for a tag follow-up, suppress or
	// new-flow. It distinguishes a bound nil from "no value".
	hasVal bool
	// fn is the type-erased follow-up body: the registering FlowKey/FlowTag method
	// wraps the user's typed handler into this shape (value delivered as `any`, the
	// key's bundle value or nil for a tag). nil unless kind == flowOptFollowUp.
	fn func(ctx context.Context, value any) error
}

// Value returns a [FlowOption] that attaches v under k for the extent of a
// [WithFlow] scope: every dispatch inside the scope inherits it, transitively
// along the causal chain, until a fan-in severs it or a nested scope shadows
// it. Read it back inside a body with [FlowKey.From]. To also run a hook at the
// flow's end with v in hand, use [FlowKey.FollowUp]/[FlowKey.FollowUpFn] instead.
func (k FlowKey[V]) Value(v V) FlowOption {
	if k.id == nil {
		panic("streampool: Value called on a zero FlowKey; mint with NewFlowKey")
	}
	return FlowOption{kind: flowOptValue, id: k.id, val: v, hasVal: true}
}

// From returns the value attached under k on ctx's flow, if any. The comma-ok
// form never panics: it reports (zero, false) on a ctx the framework has
// never stamped, outside any registering scope, below a fan-in, or for the
// zero FlowKey.
func (k FlowKey[V]) From(ctx context.Context) (V, bool) {
	var zero V
	if k.id == nil {
		return zero, false
	}
	m, ok := metaFromContext(ctx)
	if !ok {
		return zero, false
	}
	// Nearest value node wins; a follow-up-only node (hasVal false) under the same
	// id is transparent — walk past it to the value it inherits.
	for n := m.riders; n != nil; n = n.next {
		if n.id == k.id && n.hasVal {
			v, vok := n.val.(V)
			return v, vok
		}
	}
	return zero, false
}

// InFlow reports whether the work associated with ctx is part of a flow
// carrying t. Presence is DAG-scoped: it ORs through fan-ins, so it remains
// true in work downstream of a funnel flush that folded tagged items. Never
// panics; false on a never-stamped ctx or for the zero FlowTag.
func (t FlowTag) InFlow(ctx context.Context) bool {
	if t.id == nil {
		return false
	}
	m, ok := metaFromContext(ctx)
	if !ok {
		return false
	}
	for n := m.riders; n != nil; n = n.next {
		if n.id == t.id {
			return true
		}
	}
	return false
}

// FlowKeyFollowUp is the body registered by [FlowKey.FollowUp]; its Do runs once
// at the flow's end and receives the key's bundle value directly (the key's own
// rider is peeled before the call, so [FlowKey.From] would read absent inside —
// the argument is the value's channel). Its error propagates like a body's:
// joined into [WithFlow]'s return for an end reached before the scope exits, or
// through the finishing wave's drain otherwise.
type FlowKeyFollowUp[T any] interface {
	Do(ctx context.Context, value T) error
}

// FlowKeyFollowUpFunc adapts a plain function to [FlowKeyFollowUp];
// [FlowKey.FollowUpFn] wraps one for you.
type FlowKeyFollowUpFunc[T any] func(ctx context.Context, value T) error

// Do calls f.
func (f FlowKeyFollowUpFunc[T]) Do(ctx context.Context, value T) error { return f(ctx, value) }

// FlowTagFollowUp is the body registered by [FlowTag.FollowUp]; its Do runs once
// at the flow's end. A tag carries no value, so it takes only a ctx. Error
// propagation as in [FlowKeyFollowUp].
type FlowTagFollowUp interface {
	Do(ctx context.Context) error
}

// FlowTagFollowUpFunc adapts a plain function to [FlowTagFollowUp];
// [FlowTag.FollowUpFn] wraps one for you.
type FlowTagFollowUpFunc func(ctx context.Context) error

// Do calls f.
func (f FlowTagFollowUpFunc) Do(ctx context.Context) error { return f(ctx) }

// FollowUp returns a [FlowOption] that binds v under k AND registers h to run
// once at the flow's end — when the registering scope has exited and all work
// carrying k's bundle has completed. h receives v directly (the value is an
// explicit argument, captured here at registration, so it is unambiguous — no
// ambient lookup, no order dependence). The follow-up runs under the enclosing
// rider set with k's own rider peeled, so [FlowKey.From] reads absent inside h
// and h's own dispatches do not re-fire it (re-extending the flow under k is an
// explicit re-stamp inside h). A valueless follow-up is a tag's; see
// [FlowTag.FollowUp]. See docs/decisions/flow-design.md.
func (k FlowKey[V]) FollowUp(v V, h FlowKeyFollowUp[V]) FlowOption {
	if k.id == nil {
		panic("streampool: FollowUp called on a zero FlowKey; mint with NewFlowKey")
	}
	if h == nil {
		panic("streampool: FollowUp called with a nil handler")
	}
	return FlowOption{kind: flowOptFollowUp, id: k.id, val: v, hasVal: true,
		fn: func(ctx context.Context, value any) error {
			vv, _ := value.(V)
			return h.Do(ctx, vv)
		}}
}

// FollowUpFn is [FlowKey.FollowUp] sugar over a plain function.
func (k FlowKey[V]) FollowUpFn(v V, fn func(ctx context.Context, value V) error) FlowOption {
	if fn == nil {
		panic("streampool: FollowUpFn called with a nil function")
	}
	return k.FollowUp(v, FlowKeyFollowUpFunc[V](fn))
}

// FollowUp returns a [FlowOption] that registers h to run once at the
// registration's end — when the registering scope has exited and all work in
// the tagged flow has completed. Semantics as in [FlowKey.FollowUp].
func (t FlowTag) FollowUp(h FlowTagFollowUp) FlowOption {
	if t.id == nil {
		panic("streampool: FollowUp called on a zero FlowTag; mint with NewFlowTag")
	}
	if h == nil {
		panic("streampool: FollowUp called with a nil handler")
	}
	return FlowOption{kind: flowOptFollowUp, id: t.id, fn: func(ctx context.Context, _ any) error {
		return h.Do(ctx)
	}}
}

// FollowUpFn is [FlowTag.FollowUp] sugar over a plain function.
func (t FlowTag) FollowUpFn(fn func(ctx context.Context) error) FlowOption {
	if fn == nil {
		panic("streampool: FollowUpFn called with a nil function")
	}
	return t.FollowUp(FlowTagFollowUpFunc(fn))
}

// Infuse returns a [FlowOption] that marks the flow with t as bare presence: no
// value, no follow-up lifetime. Inside the scope and everywhere downstream —
// across fan-ins, since presence is DAG-scoped — [FlowTag.InFlow] reports t, so a
// body can ask "am I part of this flow?" without anyone registering a hook. It
// complements [FlowTag.FollowUp] (presence plus a lifetime); a flow may carry
// both, and [FlowTag.Suppress] clears either from a subtree.
func (t FlowTag) Infuse() FlowOption {
	if t.id == nil {
		panic("streampool: Infuse called on a zero FlowTag; mint with NewFlowTag")
	}
	return FlowOption{kind: flowOptInfuse, id: t.id}
}

// FlowFollowUp returns a [FlowOption] registering h to run once at the flow's true
// end — after the registering scope exits and all work in the flow, across any
// aggregation, has completed. It is the anonymous, DAG-scoped follow-up: it mints
// a fresh unnamed identity per call, so — unlike a [FlowTag] follow-up — it can be
// neither queried with InFlow nor cleared with Suppress; reach for it when you
// just want "run this at the end" with no name to bind. Being DAG-scoped it
// crosses funnel fan-ins, like a tag's follow-up. See docs/decisions/flow-design.md.
func FlowFollowUp(h FlowTagFollowUp) FlowOption {
	if h == nil {
		panic("streampool: FlowFollowUp called with a nil handler")
	}
	return FlowOption{kind: flowOptFollowUp, id: &flowIdentity{kind: flowTagIdent},
		fn: func(ctx context.Context, _ any) error {
			return h.Do(ctx)
		}}
}

// FlowFollowUpFn is [FlowFollowUp] sugar over a plain function.
func FlowFollowUpFn(fn func(ctx context.Context) error) FlowOption {
	if fn == nil {
		panic("streampool: FlowFollowUpFn called with a nil function")
	}
	return FlowFollowUp(FlowTagFollowUpFunc(fn))
}

// Suppress returns a [FlowOption] that stops k's INHERITED bundle at the
// scope boundary: inside the scope the key reads absent, and work dispatched
// there takes no refs on the bundle's follow-ups — so a suppressed subtree
// cannot delay their nominal end. Suppression applies to the inherited set
// only; a Value or FollowUp for the same key in the same call registers
// fresh, regardless of option order.
func (k FlowKey[V]) Suppress() FlowOption {
	if k.id == nil {
		panic("streampool: Suppress called on a zero FlowKey; mint with NewFlowKey")
	}
	return FlowOption{kind: flowOptSuppress, id: k.id}
}

// Suppress returns a [FlowOption] that stops t's inherited presence and
// follow-up refs at the scope boundary. Semantics as in [FlowKey.Suppress].
func (t FlowTag) Suppress() FlowOption {
	if t.id == nil {
		panic("streampool: Suppress called on a zero FlowTag; mint with NewFlowTag")
	}
	return FlowOption{kind: flowOptSuppress, id: t.id}
}

// Disconnect returns a [FlowOption] that disconnects the scope from
// everything registered so far: the working rider set — the inherited chain
// plus any options listed before it — is dropped, values AND tags, a stricter
// cut than a funnel fan-in (which severs values but unions tags through). The
// causal flow itself continues (work dispatched inside still descends from
// this scope; cancellation still rides ctx ancestry); only riders are
// dropped. Options apply left to right, each a nested layer (see [WithFlow]),
// so Disconnect is normally listed FIRST: options after it add to the fresh
// set; a Value before it is shadowed, and a follow-up before it still
// registers and fires at scope exit as an empty flow. Per-identity
// [FlowKey.Suppress]/[FlowTag.Suppress] are its targeted counterparts.
func Disconnect() FlowOption {
	return FlowOption{kind: flowOptDisconnect}
}

// WithFlow runs body inline on the calling goroutine with a context whose
// flow rider set is the ambient one modified by opts. Options apply LEFT TO
// RIGHT, one nested layer each — an option list is sugar for nested WithFlow
// scopes, the first option outermost: a later Value shadows an earlier
// sibling, Suppress filters the set as built so far, and Disconnect drops it
// (so Disconnect is normally listed first). It is a plain function call, not
// a dispatched work item: no wave membership, no permits, no backpressure; a
// panic in body propagates (the framework never recovers); body's error is
// returned verbatim. With no options the call degenerates to body(ctx).
//
// The ctx parameter is execution ancestry — cancellation for work dispatched
// inside rides it, and ambient riders are inherited from it. A request ctx
// belongs in a rider instead (requestCtx.Value(r.Context())): a flow value is
// consultative data, never a parent of framework context derivation.
//
// The ctx passed to body is valid for the duration of the call, like any
// body ctx. Work dispatched inside the scope captures the rider set at
// dispatch, so it is unaffected by the scope ending.
//
// WithFlow is optional: flows always exist, and every bare Submit extends one
// with the ambient (possibly empty) rider set. Most programs never call it.
// See docs/decisions/flow-design.md.
func WithFlow(ctx context.Context, body func(context.Context) error, opts ...FlowOption) (err error) {
	if body == nil {
		panic("streampool: WithFlow called with a nil body")
	}
	if len(opts) == 0 {
		return body(ctx)
	}

	src, _ := metaFromContext(ctx)
	var ambient *flowRiderNode
	if src != nil {
		src.vetNotExpiredPin()
		ambient = src.riders
	}
	riders, created := buildFlowRiders(ambient, opts)

	// The scope meta clones the ambient meta (when there is one) so it is
	// transparent to everything but the rider chain: wave resolution, the
	// permit chain (parent link; held stays nil so currentHeldPermit walks
	// through), reentrancy typing, and the execution environment all behave
	// exactly as they would on ctx itself. At top level (no ambient meta) the
	// remaining fields stay zero: wave nil (ambient dispatch still requires
	// op.In), ctxType top-level, exEnv nil (minted by the dispatch path).
	//
	// The meta and its ctxpool child are POOLED and released at return
	// (docs/decisions/flow-rider-chain.md, "Pooling summary"): a scope ctx is
	// call-scoped like every framework-provided ctx, so retaining one past
	// WithFlow's return is undefined — dispatched work carries its own body meta
	// and never resolves the scope meta THROUGH the ctx. Bodies dispatched in the
	// scope do ref-pin the meta as their parent, so recycling waits on the
	// unrefMeta cascade when they outlive the scope. The release is registered
	// FIRST among the trailing defers so it runs LAST — after the inline
	// follow-up fires below, which root their own metas and never read the
	// scope ctx.
	m := newCtxMeta()
	m.riders = riders
	nodeRef(riders) // the scope meta's carrier ref on the chain head
	if src != nil {
		m.wave = src.wave
		m.parent = src
		refMeta(src)
		m.parentWaves = retainParentWaveSet(src.parentWaves)
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	scopeCtx := ctxpool.WithValue(ctx, m)
	m.selfCtx = scopeCtx
	defer func() {
		unrefMeta(m)
		nodeUnref(riders) // release the scope meta's head ref (cascades if last)
	}()

	if len(created) > 0 {
		// Release the scope's ref on each instance this scope registered, in
		// REVERSE registration order (LIFO — innermost first, defer-like). The
		// release that reaches zero fires the follow-up INLINE here at scope exit
		// (semantically the user's own call site; also what makes "an empty scope
		// fires at return" hold deterministically). Reverse order gives the LIFO
		// firing sequence directly when the flow is already quiescent; the
		// inner-holds-outer refs enforce it when work is still outstanding.
		// Deferred so a panicking body still releases. Inherited instances hold no
		// scope ref: the enclosing carrier's ref covers this extent.
		//
		// An inline firing's error joins WithFlow's return, body error FIRST (err
		// already holds it), then follow-ups in this LIFO order (innermost first).
		//nolint:contextcheck // inline scope-exit fire runs on the caller's own frame
		defer func() {
			for i := len(created) - 1; i >= 0; i-- {
				// The scope meta is the fire's carrier (its own release is the
				// outermost defer, so it is still alive here); the inline fire
				// COWs from it.
				if fireErr := created[i].unref(true, nil, m); fireErr != nil {
					err = errors.Join(err, fireErr)
				}
			}
		}()
	}

	err = body(scopeCtx)
	return err
}

// flowRiderNode is one binding on the flow rider chain: a single identity's
// value and/or its follow-up instance, inlined (no backing slice). The chain is
// walked head→next on read; the head is shared by pointer along the causal
// dispatch chain, so a registering WithFlow allocates only the nodes for its own
// additions and links them ahead of the inherited head. A node is immutable once
// published — reads need no synchronization — so a modification (register /
// suppress / sever) produces fresh nodes and leaves the old ones intact for
// everything still pointing at them.
type flowRiderNode struct {
	id     *flowIdentity  // the key or tag this binding is under
	val    any            // the key's value, when hasVal
	hasVal bool           // distinguishes a value binding from a follow-up-only node
	inst   *flowInstance  // the follow-up instance, when this binding registered one
	next   *flowRiderNode // the enclosing chain (toward the root); nil at a flow root
	// refs counts the holders of THIS node: its child nodes' downlinks, the carrier
	// metas whose head is this node, and a follow-up instance's ref on its enclosing
	// head (docs/decisions/flow-rider-chain.md). At zero the node returns to the
	// pool and drops its own downlink ref on next, cascading. Distinct from
	// flowInstance.count, which drives firing: a deep node sees only ONE downlink per
	// child scope regardless of that scope's carrier count, so node.refs cannot
	// detect an instance's quiescence.
	refs atomic.Int64
}

// flowRiderNodePool recycles rider nodes. A node is immutable but for refs, so a
// recycled node is fully re-stamped by newRiderNode on its next borrow.
var flowRiderNodePool = omnipool.For[flowRiderNode]()

// flowNodeAllocHook, when set, receives +1 as a node is drawn from the pool and
// -1 as one is returned — the seam the conservation test uses to prove no node
// leaks or is double-freed across a drained flow. Production leaves it nil; the
// cost is one relaxed atomic load per node borrow/reclaim, uncontended.
var flowNodeAllocHook atomic.Pointer[func(int)]

func flowNodeAlloc(delta int) {
	if h := flowNodeAllocHook.Load(); h != nil {
		(*h)(delta)
	}
}

// Reset is the omnipool recycle hook (refs is atomic.Int64, whose noCopy would
// trip vet copylocks under omnipool's plain-copy zero).
func (n *flowRiderNode) Reset() {
	n.id = nil
	n.val = nil
	n.hasVal = false
	n.inst = nil
	n.next = nil
	n.refs.Store(0)
}

// newRiderNode draws a node from the pool, stamps its binding, links it onto
// next, and takes next's downlink ref (released when this node reclaims). The
// returned node has refs == 0; the caller publishes it by taking the first ref —
// a child's downlink (a later newRiderNode) or a carrier's nodeRef.
func newRiderNode(
	id *flowIdentity, val any, hasVal bool, inst *flowInstance, next *flowRiderNode,
) *flowRiderNode {
	n := flowRiderNodePool.Get()
	flowNodeAlloc(1)
	n.id = id
	n.val = val
	n.hasVal = hasVal
	n.inst = inst
	n.next = next
	nodeRef(next) // downlink ref
	return n
}

// nodeRef takes one reference on n (nil-safe).
func nodeRef(n *flowRiderNode) {
	if n != nil {
		n.refs.Add(1)
	}
}

// nodeUnref releases one reference on n; the release that reaches zero reclaims n
// to the pool and cascades the drop down its next chain (each reclaimed node drops
// the downlink ref it held on its successor).
func nodeUnref(n *flowRiderNode) {
	for n != nil {
		r := n.refs.Add(-1)
		if r > 0 {
			return
		}
		if r < 0 {
			panic("streampool: flow rider node refs underflow (double release)")
		}
		next := n.next
		flowNodeAlloc(-1)
		flowRiderNodePool.Release(n)
		n = next
	}
}

// rebuild walks head down to stop (exclusive), keeping each node keep accepts
// (with next rewired past the dropped nodes) and skipping the rest, then links
// the kept prefix onto stop (shared, untouched). stop == nil walks to the root.
// Because a node is one binding, keep is a whole-node predicate — no in-node
// filtering, no splitting. Cold path (suppression; later the funnel sever); the
// kept nodes are copied so the shared originals are never mutated.
func rebuild(head, stop *flowRiderNode, keep func(*flowRiderNode) bool) *flowRiderNode {
	var kept []*flowRiderNode
	for n := head; n != stop; n = n.next {
		if keep(n) {
			kept = append(kept, n)
		}
	}
	out := stop
	for i := len(kept) - 1; i >= 0; i-- {
		k := kept[i]
		out = newRiderNode(k.id, k.val, k.hasVal, k.inst, out)
	}
	return out
}

// buildFlowRiders derives a fresh chain head by folding the options over the
// inherited chain LEFT TO RIGHT — an option list is sugar for nested scopes,
// one layer per option, the first option outermost (PN, 2026-07-10; replaces
// the earlier order-independent build). Additive options (Value, FollowUp,
// Infuse) prepend a node, so a later option under the same identity shadows an
// earlier one exactly as an inner scope shadows an outer (nearest-wins walk).
// Subtractive options act on the chain AS BUILT SO FAR: Suppress filters its
// identity out (inherited or earlier-sibling alike), Disconnect drops the
// whole working chain. Options before a Disconnect are therefore shadowed —
// well-defined nonsense for a Value, while an earlier follow-up still
// registers: its instance keeps the scope ref, gains no carriers from the
// body (which runs under the post-Disconnect layer), and fires at scope exit
// as an empty flow — the nesting equivalence's answer, conservation-sound.
//
// A follow-up's node bundles its own captured value (the explicit argument —
// no ambient settling), and its fire's enclosing set is node.next: the
// bindings layered before it, its own binding peeled by construction (the
// value arrives as the follow-up's argument instead). Returns the instances
// THIS scope created — the caller holds one scope ref on each, released at
// scope exit; inherited instances take no scope ref (the enclosing carrier's
// ref covers this scope's extent).
func buildFlowRiders(ambient *flowRiderNode, opts []FlowOption) (*flowRiderNode, []*flowInstance) {
	// Validate before building — a mid-build panic must not leak nodes.
	for i := range opts {
		switch opts[i].kind {
		case flowOptValue, flowOptFollowUp, flowOptInfuse, flowOptSuppress, flowOptDisconnect:
		default:
			panic("streampool: invalid (zero) FlowOption passed to WithFlow")
		}
	}

	head := ambient
	var created []*flowInstance

	// replace swaps the working chain for a rebuilt (or empty) one, disposing
	// of the previous structure: the transient ref covers a refs-0 fresh top,
	// and the reclaim cascade stops at the first node someone else still holds
	// — an ambient carrier\'s ref, or an earlier follow-up\'s enclosing pin
	// (whose snapshot legitimately outlives the swap, freed at its fire).
	// Net zero on a purely inherited chain.
	replace := func(nh *flowRiderNode) {
		old := head
		head = nh
		nodeRef(old)
		nodeUnref(old)
	}

	// mint creates a follow-up instance layered on the chain so far: enclosing
	// = head (node.next once its own node links ahead), an inner-holds-outer
	// ref on every instance already on that chain (released when its single
	// fire completes, so an outer waits for this whole subtree — LIFO), and
	// one scope ref released at scope exit.
	mint := func(o *FlowOption, fn func(context.Context, any) error, definitional bool) {
		in := flowInstancePool.Get()
		in.fn = fn
		in.definitional = definitional
		if definitional {
			in.id = o.id // reaches the tag\'s mergeMu for coalescing at count→0 (CP-R6b)
		}
		in.count.Store(1) // the registering scope\'s ref, released at scope exit
		in.val = o.val    // the option\'s own captured value (nil for a tag / valueless key)
		in.enclosing = head
		nodeRef(in.enclosing) // hold the enclosing chain alive for the fire (freed at fire-complete)
		for n := head; n != nil; n = n.next {
			if n.inst != nil {
				n.inst.ref()
				in.holds = append(in.holds, n.inst)
			}
		}
		head = newRiderNode(o.id, o.val, o.hasVal, in, head)
		created = append(created, in)
	}

	for i := range opts {
		o := &opts[i]
		switch o.kind {
		case flowOptValue:
			head = newRiderNode(o.id, o.val, true, nil, head)
		case flowOptFollowUp:
			mint(o, o.fn, false)
		case flowOptInfuse:
			if o.id.definitionalFn == nil {
				// Presence-only marker so InFlow reports the tag.
				head = newRiderNode(o.id, nil, false, nil, head)
				continue
			}
			// A DEFINITIONAL tag\'s infusion mints its identity-bound follow-up —
			// once per flow: if an instance for this id is already on the chain
			// (an enclosing infusion, or an earlier one this scope), infusion is
			// idempotent — reuse it, mint nothing.
			already := false
			for n := head; n != nil; n = n.next {
				if n.id == o.id && n.inst != nil && n.inst.definitional {
					already = true
					break
				}
			}
			if !already {
				mint(o, o.id.definitionalFn, true)
			}
		case flowOptSuppress:
			replace(rebuild(head, nil, func(n *flowRiderNode) bool { return n.id != o.id }))
		case flowOptDisconnect:
			replace(nil)
		}
	}

	return head, created
}

// flowBoundaryAboveWave resolves the fan-in boundary for a funnel on wave: the
// rider chain head of the nearest meta ABOVE wave in the dispatching meta m's
// synchronous derivation chain — the enclosing (driving) flow's head that the
// funnel's items descend from and share by pointer. It MUST be called at
// dispatch, on the dispatcher's goroutine, with the meta resolved there (m may
// be nil for a submit from a bare ctx). Returns nil when there is no meta or
// nothing encloses the wave.
func flowBoundaryAboveWave(m *ctxMeta, wave *waveImpl) *flowRiderNode {
	if m == nil {
		return nil
	}
	// Walk up past metas belonging to the wave, within the synchronous extent
	// (syncParent stops at an async body meta — a permitRoot): when the chain
	// ends inside the wave, that last body meta's riders ARE the enclosing
	// chain — the driver's head it captured at its own dispatch — so stop
	// there rather than walking off to a nil boundary.
	for m.wave == wave {
		p := m.syncParent()
		if p == nil {
			break
		}
		m = p
	}
	return m.riders
}

// collectFlowTags folds an accumulate item's DAG-scoped (tag) riders ABOVE the
// boundary into union — the fan-in transfer (F7). It walks the item's chain from
// the head and STOPS at stop (== the funnel's captured boundary, by pointer): the
// nodes above it are the item's per-item additions, so only their tags cross
// (values drop — the sever, F8), while everything at and below the boundary is the
// enclosing flow, already shared as union's tail. Follow-up nodes contribute one
// carrier ref per DISTINCT instance (refs are fungible covers — union is a set,
// not a multiset; the funnel's ref covers the tag from accumulate until the flush
// takeover adopts it, overlapping the item's own ref); bare-presence (Infuse)
// nodes carry no instance and contribute presence once per DISTINCT id with no ref
// (membership has nothing to keep alive). Dedup scans only the folded prefix
// (union down to stop). Called under the funnel instance's mu; union's nodes are
// owned by the funnel instance.
func collectFlowTags(union *flowRiderNode, ctx context.Context, stop *flowRiderNode) *flowRiderNode {
	m, ok := metaFromContext(ctx)
	if !ok {
		return union
	}
	// prepend attaches a pooled union node and moves the funnel's carrier ref from
	// the old head to the new one (the old head survives via the new node's
	// downlink). The whole union is adopted by the flush meta at flowFanInContext.
	prepend := func(id *flowIdentity, inst *flowInstance) {
		newHead := newRiderNode(id, nil, false, inst, union)
		nodeRef(newHead)
		nodeUnref(union)
		union = newHead
	}
	for n := m.riders; n != nil && n != stop; n = n.next {
		if n.id.kind != flowTagIdent {
			continue
		}
		if n.inst != nil {
			seen := false
			for u := union; u != nil && u != stop; u = u.next {
				if u.inst == n.inst {
					seen = true
					break
				}
			}
			if !seen {
				n.inst.ref()
				prepend(n.id, n.inst)
				// Coalesce independent flows (CP-R6b): if another definitional instance
				// for this same tag already lives on the union, this item descends from a
				// different flow root that infused the tag independently — merge the two
				// into one lifetime so the definitional follow-up fires once. The scan
				// walks the WHOLE union to nil, PAST stop: the driver flow's own
				// definitional instance rides the shared boundary tail (below stop, F8),
				// never folded, so it is only reachable there. Every candidate is live
				// (funnel-ref'd until flush — the boundary via the seed flowRefRiders, a
				// folded instance via its own ref above), so the merge is death-race-free.
				// The first fold merges; union-find makes every later meeting a no-op.
				if n.inst.definitional {
					for u := union.next; u != nil; u = u.next {
						if u.inst != nil && u.inst.definitional && u.id == n.id {
							n.id.mergeMu.Lock()
							mergeDefinitional(n.inst, u.inst)
							n.id.mergeMu.Unlock()
							break
						}
					}
				}
			}
			continue
		}
		// Presence-only: ensure the id is represented once in the folded prefix.
		seen := false
		for u := union; u != nil && u != stop; u = u.next {
			if u.id == n.id {
				seen = true
				break
			}
		}
		if !seen {
			prepend(n.id, nil)
		}
	}
	return union
}

// flowFanInContext is the funnel accumulate→flush fan-in applied to the flush
// ctx: path-scoped riders sever (whatever the drive ctx carried — notably the
// triggering item's riders on the inline past-deadline path — is dropped),
// while the DAG-scoped tags collected from ALL accumulated items take over as
// the flush body's rider set. The clone ADOPTS the funnel's collected refs
// outright: releaseBodyContext at the flush's end releases exactly one ref
// per distinct instance — the one collectFlowTags took — so the handoff never
// transits an unreferenced state and never churns the counts. Reports false
// (ctx unchanged, nothing to release) when there is nothing to sever or
// adopt. The severed/adopted extent must be synchronous, like any body ctx.
func flowFanInContext(ctx context.Context, tags *flowRiderNode) (context.Context, bool) {
	src, ok := metaFromContext(ctx)
	hasSrcRiders := ok && src.riders != nil
	if !hasSrcRiders && tags == nil {
		return ctx, false
	}
	m := newCtxMeta()
	if ok {
		m.wave = src.wave
		m.parent = src // preserve the permit chain; held stays nil on the clone
		refMeta(src)
		m.parentWaves = retainParentWaveSet(src.parentWaves)
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	// The tag union takes over; path riders sever (nil when no tags). The flush meta
	// ADOPTS the funnel's carrier ref on the union head (no new nodeRef — the funnel
	// hands it off, nil'ing c.flowTags), released by releaseBodyContext at flush end.
	m.riders = tags
	fc := ctxpool.WithValue(ctx, m)
	m.selfCtx = fc
	return fc, true
}
