// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"

	"github.com/petenewcomb/streampool/internal/ctxpool"
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
func NewFlowTag() FlowTag {
	return FlowTag{id: &flowIdentity{kind: flowTagIdent}}
}

type flowOptionKind int8

const (
	flowOptValue flowOptionKind = iota + 1
	flowOptFollowUp
	flowOptSuppress
	flowOptNewFlow
)

// FlowOption configures a [WithFlow] scope. Obtain options from the methods
// on [FlowKey] and [FlowTag] (Value, FollowUp, Suppress) or from [NewFlow];
// the zero FlowOption is invalid and panics when passed to WithFlow.
type FlowOption struct {
	kind flowOptionKind
	id   *flowIdentity
	val  any
	// fn is the type-erased follow-up body: the registering FlowKey/FlowTag method
	// wraps the user's typed handler into this shape (value delivered as `any`, the
	// key's bundle value or nil for a tag). nil unless kind == flowOptFollowUp.
	fn func(ctx context.Context, value any) error
}

// Value returns a [FlowOption] that attaches v under k for the extent of a
// [WithFlow] scope: every dispatch inside the scope inherits it, transitively
// along the causal chain, until a fan-in severs it or a nested scope shadows
// it. Read it back inside a body with [FlowKey.From].
func (k FlowKey[V]) Value(v V) FlowOption {
	if k.id == nil {
		panic("streampool: Value called on a zero FlowKey; mint with NewFlowKey")
	}
	return FlowOption{kind: flowOptValue, id: k.id, val: v}
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

// FollowUp returns a [FlowOption] that registers h to run once at the
// registration's end — when the registering scope has exited and all work
// carrying k's bundle has completed. h receives k's value and runs under the
// enclosing rider set (k's own rider peeled, so h's own dispatches do not
// re-fire it; re-extending the flow under k is an explicit re-stamp inside h).
// See docs/decisions/flow-design.md.
func (k FlowKey[V]) FollowUp(h FlowKeyFollowUp[V]) FlowOption {
	if k.id == nil {
		panic("streampool: FollowUp called on a zero FlowKey; mint with NewFlowKey")
	}
	if h == nil {
		panic("streampool: FollowUp called with a nil handler")
	}
	return FlowOption{kind: flowOptFollowUp, id: k.id, fn: func(ctx context.Context, value any) error {
		v, _ := value.(V) // zero V when the key carries no value
		return h.Do(ctx, v)
	}}
}

// FollowUpFn is [FlowKey.FollowUp] sugar over a plain function.
func (k FlowKey[V]) FollowUpFn(fn func(ctx context.Context, value V) error) FlowOption {
	if fn == nil {
		panic("streampool: FollowUpFn called with a nil function")
	}
	return k.FollowUp(FlowKeyFollowUpFunc[V](fn))
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

// NewFlow returns a [FlowOption] that roots a fresh flow: the scope starts
// from an EMPTY rider set instead of inheriting the ambient one — the
// explicit form of what a funnel fan-in does implicitly for path-scoped
// riders. Sibling options add to the fresh set, in any order; per-identity
// [FlowKey.Suppress]/[FlowTag.Suppress] are its targeted counterparts.
func NewFlow() FlowOption {
	return FlowOption{kind: flowOptNewFlow}
}

// WithFlow runs body inline on the calling goroutine with a context whose
// flow rider set is the ambient one modified by opts. It is a plain function
// call, not a dispatched work item: no wave membership, no permits, no
// backpressure; a panic in body propagates (the framework never recovers);
// body's error is returned verbatim. With no options the call degenerates to
// body(ctx).
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
	// The meta and its ctxpool child are POOLED and freed at return
	// (docs/decisions/flow-rider-chain.md, "Pooling summary"): a scope ctx is
	// call-scoped like every framework-provided ctx, so retaining one past
	// WithFlow's return is undefined — dispatched work carries its own body meta
	// and never resolves the scope meta. The release is registered FIRST among the
	// trailing defers so it runs LAST — after the inline follow-up fires below,
	// which root their own metas and never read the scope ctx.
	m := bodyMetaPool.Get()
	m.riders = riders
	if src != nil {
		m.wave = src.wave
		m.parent = src
		m.parentWaves = src.parentWaves
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	scopeCtx := ctxpool.WithValue(ctx, m)
	defer func() {
		ctxpool.Free(scopeCtx)
		bodyMetaPool.Put(m)
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
				if fireErr := created[i].unref(true, nil); fireErr != nil {
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
		out = &flowRiderNode{id: k.id, val: k.val, hasVal: k.hasVal, inst: k.inst, next: out}
	}
	return out
}

// buildFlowRiders derives a fresh chain head: the inherited chain (dropped for
// NewFlow, filtered for Suppress) with this scope's addition nodes linked ahead
// of it. A nested scope re-registering a key prepends a fresh node, so the walk
// finds it first — nearest scope wins, exactly as the flat snapshot's
// replace-or-append did. Each identity's value is settled from all its Value
// options BEFORE any node is built, so a follow-up receives its settled value
// regardless of option order (order-independence). A key's value and its
// follow-up share ONE node; the follow-up's own binding is therefore peeled from
// its fire's enclosing set by construction (it lives on the node, and the fire
// carries node.next — the value arrives as the follow-up's argument instead).
// Returns the instances THIS scope created — the caller holds one scope ref on
// each, released at scope exit; inherited instances take no scope ref (the
// enclosing carrier's ref covers this scope's extent).
func buildFlowRiders(ambient *flowRiderNode, opts []FlowOption) (*flowRiderNode, []*flowInstance) {
	// Pass 0: validate and detect a fresh root. Identity-scoped lookups (settled
	// value, whether a follow-up bundles the id, whether a value node was already
	// emitted) are linear scans of opts rather than maps — opts is tiny and the
	// registering path must stay allocation-lean.
	fresh := false
	anySuppress := false
	for i := range opts {
		switch opts[i].kind {
		case flowOptValue, flowOptFollowUp:
		case flowOptSuppress:
			anySuppress = true
		case flowOptNewFlow:
			fresh = true
		default:
			panic("streampool: invalid (zero) FlowOption passed to WithFlow")
		}
	}

	// settledVal returns id's value from the last Value option under it (order-
	// independence: settled before any node is built). hasFollowUp reports whether
	// a follow-up under id will bundle its value onto that follow-up's node.
	settledVal := func(id *flowIdentity) (any, bool) {
		var v any
		found := false
		for i := range opts {
			if opts[i].kind == flowOptValue && opts[i].id == id {
				v, found = opts[i].val, true
			}
		}
		return v, found
	}
	hasFollowUp := func(id *flowIdentity) bool {
		for i := range opts {
			if opts[i].kind == flowOptFollowUp && opts[i].id == id {
				return true
			}
		}
		return false
	}

	head := ambient
	if fresh {
		head = nil
	}
	if anySuppress {
		// Suppressing against the inherited chain BEFORE any add is what makes
		// same-call Suppress+Value/FollowUp order-independent.
		head = rebuild(head, nil, func(n *flowRiderNode) bool {
			for i := range opts {
				if opts[i].kind == flowOptSuppress && opts[i].id == n.id {
					return false
				}
			}
			return true
		})
	}

	// Value-only nodes first (deepest of this scope's adds), so a later follow-up
	// under a different key sees the value on its enclosing walk. A value bundled
	// with a follow-up under the SAME id is emitted with that follow-up instead
	// (one node), so it is skipped here, as is a repeated Value for an id already
	// emitted (the last-wins value was resolved by settledVal).
	for i := range opts {
		o := &opts[i]
		if o.kind != flowOptValue || hasFollowUp(o.id) {
			continue
		}
		earlier := false
		for j := 0; j < i; j++ {
			if opts[j].kind == flowOptValue && opts[j].id == o.id {
				earlier = true
				break
			}
		}
		if earlier {
			continue
		}
		v, _ := settledVal(o.id)
		head = &flowRiderNode{id: o.id, val: v, hasVal: true, next: head}
	}

	// Then follow-up nodes in option order (later nearer the head → LIFO peel).
	// Each fires ONCE at its own end. Its enclosing set is node.next (all bindings
	// registered before it), and it takes an inner-holds-outer ref on every
	// instance already on that chain, released when its single fire completes — so
	// an outer waits for this whole subtree (LIFO). The value bundled under its id
	// rides its own node (peeled from node.next) and is delivered as the fn arg.
	var created []*flowInstance
	for i := range opts {
		o := &opts[i]
		if o.kind != flowOptFollowUp {
			continue
		}
		in := flowInstancePool.Get()
		in.fn = o.fn
		in.count.Store(1) // the registering scope's ref, released at scope exit
		val, hasVal := settledVal(o.id)
		in.val = val // settled bundle value (nil for a tag / valueless key)
		in.enclosing = head
		for n := head; n != nil; n = n.next {
			if n.inst != nil {
				n.inst.ref()
				in.holds = append(in.holds, n.inst)
			}
		}
		head = &flowRiderNode{id: o.id, val: val, hasVal: hasVal, inst: in, next: head}
		created = append(created, in)
	}

	return head, created
}

// collectFlowTags folds the DAG-scoped (tag) riders of ctx's chain into union,
// taking one carrier ref on each instance newly added — the fan-in transfer.
// The funnel instance's ref covers the tag from this accumulate until the
// flush takeover adopts it, overlapping the accumulate item's own ref
// (ref-before-release: the instance never transits an unreferenced state).
// One ref per DISTINCT instance suffices — refs are fungible covers, not
// per-item tokens — so union is a set, not a multiset. Called under the
// funnel instance's mu; union's nodes are owned by the funnel instance.
func collectFlowTags(union *flowRiderNode, ctx context.Context) *flowRiderNode {
	m, ok := metaFromContext(ctx)
	if !ok {
		return union
	}
	for n := m.riders; n != nil; n = n.next {
		if n.id.kind != flowTagIdent || n.inst == nil {
			continue
		}
		present := false
		for u := union; u != nil; u = u.next {
			if u.inst == n.inst {
				present = true
				break
			}
		}
		if !present {
			n.inst.ref()
			union = &flowRiderNode{id: n.id, inst: n.inst, next: union}
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
	m := bodyMetaPool.Get()
	if ok {
		m.wave = src.wave
		m.parent = src // preserve the permit chain; held stays nil on the clone
		m.parentWaves = src.parentWaves
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	m.riders = tags // the tag union takes over; path riders severed (nil when no tags)
	return ctxpool.WithValue(ctx, m), true
}
