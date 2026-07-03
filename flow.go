// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"

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
)

// FlowOption configures a [WithFlow] scope. Obtain options from the methods
// on [FlowKey] and [FlowTag] (Value, FollowUp, Suppress) or from [NewFlow];
// the zero FlowOption is invalid and panics when passed to WithFlow.
type FlowOption struct {
	kind flowOptionKind
	id   *flowIdentity
	val  any
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

// FollowUp returns a [FlowOption] that registers fn to run at the key's
// nominal end — when all work carrying k's bundle has completed. Not yet
// implemented; the registration currently panics. (Flow follow-ups land in a
// later checkpoint; see docs/decisions/flow-design.md.)
func (k FlowKey[V]) FollowUp(fn func(context.Context) error) FlowOption {
	if k.id == nil {
		panic("streampool: FollowUp called on a zero FlowKey; mint with NewFlowKey")
	}
	if fn == nil {
		panic("streampool: FollowUp called with a nil function")
	}
	panic("streampool: flow follow-ups are not yet implemented")
}

// FollowUp returns a [FlowOption] that registers fn to run at the tag's
// nominal end — when all work in the tagged flow, including work downstream
// of fan-ins, has completed. Not yet implemented; the registration currently
// panics. (Flow follow-ups land in a later checkpoint; see
// docs/decisions/flow-design.md.)
func (t FlowTag) FollowUp(fn func(context.Context) error) FlowOption {
	if t.id == nil {
		panic("streampool: FollowUp called on a zero FlowTag; mint with NewFlowTag")
	}
	if fn == nil {
		panic("streampool: FollowUp called with a nil function")
	}
	panic("streampool: flow follow-ups are not yet implemented")
}

// Suppress returns a [FlowOption] that stops k's inherited bundle at the
// scope boundary: work dispatched inside the scope reads the key as absent.
// Not yet implemented; currently panics. (Suppression lands in a later
// checkpoint; see docs/decisions/flow-design.md.)
func (k FlowKey[V]) Suppress() FlowOption {
	if k.id == nil {
		panic("streampool: Suppress called on a zero FlowKey; mint with NewFlowKey")
	}
	panic("streampool: flow suppression is not yet implemented")
}

// Suppress returns a [FlowOption] that stops t's inherited presence (and
// follow-up refs) at the scope boundary. Not yet implemented; currently
// panics. (Suppression lands in a later checkpoint; see
// docs/decisions/flow-design.md.)
func (t FlowTag) Suppress() FlowOption {
	if t.id == nil {
		panic("streampool: Suppress called on a zero FlowTag; mint with NewFlowTag")
	}
	panic("streampool: flow suppression is not yet implemented")
}

// NewFlow returns a [FlowOption] that roots a fresh flow: the scope starts
// from an empty rider set instead of inheriting the ambient one. Sibling
// options add to the fresh set, in any order. Not yet implemented; currently
// panics. (Lands with suppression in a later checkpoint; see
// docs/decisions/flow-design.md.)
func NewFlow() FlowOption {
	panic("streampool: NewFlow is not yet implemented")
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
func WithFlow(ctx context.Context, body func(context.Context) error, opts ...FlowOption) error {
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

	// The scope meta clones the ambient meta (when there is one) so it is
	// transparent to everything but the rider set: wave resolution, the
	// permit chain (parent link; held stays nil so currentHeldPermit walks
	// through), reentrancy typing, and the execution environment all behave
	// exactly as they would on ctx itself. At top level (no ambient meta) the
	// remaining fields stay zero: wave nil (ambient dispatch still requires
	// op.In), ctxType top-level, exEnv nil (minted by the dispatch path).
	//
	// The meta and its ctxpool child are deliberately NOT pooled or freed at
	// return: the scope has no completion event until follow-up refcounts
	// (a later checkpoint) provide one, and a retained scope ctx read after
	// a free would resolve a recycled meta. GC owns both; the cost is one
	// small allocation per registering scope, never per dispatch.
	m := &ctxMeta{riders: buildFlowRiders(ambient, opts)}
	if src != nil {
		m.wave = src.wave
		m.parent = src
		m.parentWaves = src.parentWaves
		m.ctxType = src.ctxType
		m.executionEnvironment = src.executionEnvironment
	}
	return body(ctxpool.WithValue(ctx, m))
}

// flowRiders is an immutable snapshot of the riders in scope. A registering
// WithFlow builds a fresh one; everything else shares it by pointer. The
// entries slice is small and scanned linearly; never mutate it after
// construction — it is read concurrently by every dispatch under the scope.
type flowRiders struct {
	entries []flowRiderEntry
}

type flowRiderEntry struct {
	id  *flowIdentity
	val any
}

// buildFlowRiders derives a fresh immutable snapshot: the ambient entries,
// with each option applied replace-or-append (a nested scope re-registering a
// key shadows the outer value — nearest scope wins).
func buildFlowRiders(ambient *flowRiders, opts []FlowOption) *flowRiders {
	var base []flowRiderEntry
	if ambient != nil {
		base = ambient.entries
	}
	entries := make([]flowRiderEntry, len(base), len(base)+len(opts))
	copy(entries, base)
	for i := range opts {
		o := &opts[i]
		switch o.kind {
		case flowOptValue:
			replaced := false
			for j := range entries {
				if entries[j].id == o.id {
					entries[j].val = o.val
					replaced = true
					break
				}
			}
			if !replaced {
				entries = append(entries, flowRiderEntry{id: o.id, val: o.val})
			}
		default:
			panic("streampool: invalid (zero) FlowOption passed to WithFlow")
		}
	}
	return &flowRiders{entries: entries}
}

// severFlowRiders returns a ctx whose nearest meta clones ctx's but carries
// no flow riders — the fan-in sever (path-scoped riders do not cross the
// funnel accumulate→flush edge). Reports false (ctx unchanged) when there is
// nothing to sever. The clone is a bodyMetaPool borrow stamped on a ctxpool
// child; the caller must releaseBodyContext the returned ctx when the severed
// extent — which must be synchronous — completes.
func severFlowRiders(ctx context.Context) (context.Context, bool) {
	src, ok := metaFromContext(ctx)
	if !ok || src.riders == nil {
		return ctx, false
	}
	m := bodyMetaPool.Get()
	m.wave = src.wave
	m.parent = src // preserve the permit chain; held stays nil on the clone
	m.parentWaves = src.parentWaves
	m.ctxType = src.ctxType
	m.executionEnvironment = src.executionEnvironment
	// riders stays nil — the sever.
	return ctxpool.WithValue(ctx, m), true
}
