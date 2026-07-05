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
// Mechanically, the rider set is one immutable snapshot pointer on the pooled
// ctxMeta: dispatches copy the pointer (borrowBodyContext, ensureCtxMeta), a
// registering [WithFlow] scope builds a fresh snapshot, and the funnel flush
// severs (funnelInstance.flush). The hot path pays one pointer copy.
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
	if !ok || m.riders == nil {
		return zero, false
	}
	for i := range m.riders.entries {
		if m.riders.entries[i].id == k.id {
			v, vok := m.riders.entries[i].val.(V)
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
	if !ok || m.riders == nil {
		return false
	}
	for i := range m.riders.entries {
		if m.riders.entries[i].id == t.id {
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
	var ambient *flowRiders
	if src != nil {
		ambient = src.riders
	}
	riders, created := buildFlowRiders(ambient, opts)
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

	// The scope meta clones the ambient meta (when there is one) so it is
	// transparent to everything but the rider set: wave resolution, the
	// permit chain (parent link; held stays nil so currentHeldPermit walks
	// through), reentrancy typing, and the execution environment all behave
	// exactly as they would on ctx itself. At top level (no ambient meta) the
	// remaining fields stay zero: wave nil (ambient dispatch still requires
	// op.In), ctxType top-level, exEnv nil (minted by the dispatch path).
	//
	// The meta and its ctxpool child are deliberately NOT pooled or freed at
	// return: a retained scope ctx read after a free would resolve a recycled
	// meta, and no event marks the last such read. GC owns both; the cost is
	// one small allocation per registering scope, never per dispatch.
	m := &ctxMeta{riders: riders}
	if src != nil {
		m.wave = src.wave
		m.parent = src
		m.parentWaves = src.parentWaves
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	err = body(ctxpool.WithValue(ctx, m))
	return err
}

// flowRiders is an immutable snapshot of the riders in scope. A registering
// WithFlow builds a fresh one; everything else shares it by pointer. The
// entries slice is small and scanned linearly; never mutate it after
// construction — it is read concurrently by every dispatch under the scope.
type flowRiders struct {
	entries []flowRiderEntry
}

// flowRiderEntry is one identity's bundle: its value (path keys; nil for
// tags) and the follow-up instances registered under it. The insts slice is
// as immutable as the entry — appending in a nested scope copy-appends, never
// mutating a backing array shared with the ambient snapshot.
type flowRiderEntry struct {
	id    *flowIdentity
	val   any
	insts []*flowInstance
}

// buildFlowRiders derives a fresh immutable snapshot: the ambient entries,
// with each option applied replace-or-append (a nested scope re-registering a
// key shadows the outer value — nearest scope wins). Values apply in a first
// pass and follow-up instances are created in a second, so a follow-up's fn
// ctx captures the bundle's final value regardless of option order
// (order-independence). Returns the instances THIS scope created — the caller
// holds one scope ref on each, released at scope exit; inherited instances
// take no scope ref (the enclosing carrier's ref covers this scope's extent).
func buildFlowRiders(ambient *flowRiders, opts []FlowOption) (*flowRiders, []*flowInstance) {
	// Pass 0: validation, and the fresh-root / suppression shape of the base.
	// Applying suppressions against the inherited set BEFORE any adds is what
	// makes same-call Suppress+Value/FollowUp order-independent.
	fresh := false
	for i := range opts {
		switch opts[i].kind {
		case flowOptValue, flowOptFollowUp, flowOptSuppress:
		case flowOptNewFlow:
			fresh = true
		default:
			panic("streampool: invalid (zero) FlowOption passed to WithFlow")
		}
	}
	var base []flowRiderEntry
	if ambient != nil && !fresh {
		base = ambient.entries
	}
	entries := make([]flowRiderEntry, len(base), len(base)+len(opts))
	copy(entries, base)

	entryIdx := func(id *flowIdentity) int {
		for j := range entries {
			if entries[j].id == id {
				return j
			}
		}
		return -1
	}

	for i := range opts {
		if opts[i].kind != flowOptSuppress {
			continue
		}
		if j := entryIdx(opts[i].id); j >= 0 {
			entries = append(entries[:j], entries[j+1:]...)
		}
	}

	// Pass 1: values.
	for i := range opts {
		o := &opts[i]
		if o.kind != flowOptValue {
			continue
		}
		if j := entryIdx(o.id); j >= 0 {
			entries[j].val = o.val
		} else {
			entries = append(entries, flowRiderEntry{id: o.id, val: o.val})
		}
	}

	// Pass 2: follow-up instances. Each fires ONCE at its own end. Its fnRiders is
	// the ENCLOSING snapshot (entries registered before it, its own identity
	// peeled — value delivered as the fn arg, instance omitted so it can't
	// re-fire); its extensions inherit that, holding the outer instances. It also
	// takes an inner-holds-outer ref on every already-registered instance,
	// released when it fires — so an outer waits for this whole subtree (LIFO).
	var created []*flowInstance
	for i := range opts {
		o := &opts[i]
		if o.kind != flowOptFollowUp {
			continue
		}
		in := &flowInstance{fn: o.fn}
		in.count.Store(1) // the registering scope's ref, released at scope exit

		// Enclosing snapshot: entries as they stand BEFORE this instance is added
		// (so it is naturally absent), copied so later appends don't mutate it,
		// with this identity's value peeled (From reads absent inside — the value
		// is the fn argument). The insts slices are shared read-only snapshots:
		// every future add copy-appends a fresh slice, never mutating these.
		encl := make([]flowRiderEntry, len(entries))
		copy(encl, entries)
		for e := range encl {
			if encl[e].id == o.id {
				encl[e].val = nil
			}
		}
		in.fnRiders = &flowRiders{entries: encl}

		// Inner-holds-outer: reference every instance registered before this one.
		for e := range entries {
			for _, out := range entries[e].insts {
				out.ref()
				in.holds = append(in.holds, out)
			}
		}

		// Settle this instance's own value and add it to the live entries.
		j := entryIdx(o.id)
		if j < 0 {
			entries = append(entries, flowRiderEntry{id: o.id})
			j = len(entries) - 1
		}
		in.val = entries[j].val // settled bundle value (nil for a tag / valueless key)
		// Copy-append: the copied entry's insts may share its backing array
		// with the ambient snapshot, which other goroutines read.
		entries[j].insts = append(append([]*flowInstance(nil), entries[j].insts...), in)
		created = append(created, in)
	}

	return &flowRiders{entries: entries}, created
}

// collectFlowTags folds the DAG-scoped (tag) riders of ctx's meta into union,
// taking one carrier ref on each instance newly added — the fan-in transfer.
// The funnel instance's ref covers the tag from this accumulate until the
// flush takeover adopts it, overlapping the accumulate item's own ref
// (ref-before-release: the instance never transits an unreferenced state).
// One ref per DISTINCT instance suffices — refs are fungible covers, not
// per-item tokens — so union is a set, not a multiset. Called under the
// funnel instance's mu; union's backing is owned by the funnel instance.
func collectFlowTags(union []flowRiderEntry, ctx context.Context) []flowRiderEntry {
	m, ok := metaFromContext(ctx)
	if !ok || m.riders == nil {
		return union
	}
	for i := range m.riders.entries {
		e := &m.riders.entries[i]
		if e.id.kind != flowTagIdent || len(e.insts) == 0 {
			continue
		}
		ui := -1
		for j := range union {
			if union[j].id == e.id {
				ui = j
				break
			}
		}
		if ui < 0 {
			union = append(union, flowRiderEntry{id: e.id})
			ui = len(union) - 1
		}
		for _, in := range e.insts {
			present := false
			for _, have := range union[ui].insts {
				if have == in {
					present = true
					break
				}
			}
			if !present {
				in.ref()
				union[ui].insts = append(union[ui].insts, in)
			}
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
func flowFanInContext(ctx context.Context, tags []flowRiderEntry) (context.Context, bool) {
	src, ok := metaFromContext(ctx)
	hasSrcRiders := ok && src.riders != nil
	if !hasSrcRiders && len(tags) == 0 {
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
	if len(tags) > 0 {
		m.riders = &flowRiders{entries: tags}
	}
	return ctxpool.WithValue(ctx, m), true
}
