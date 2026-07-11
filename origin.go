// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import "context"

// OriginFlow returns a read-only context positioned at the originating flow
// of the body ctx belongs to — the context of whatever made this body run —
// so the existing reads compose: key.From(origin), tag.InFlow(origin), and
// OriginFlow(origin) walks further up the chain, one branch point of the
// causal river network per application (docs/decisions/flow-design.md, "The
// river network"; docs/decisions/context-pinning-and-origin-access.md).
//
// What the origin is, per body:
//
//   - a task or accumulate body's origin is its dispatching body — the flow
//     it branched off from;
//   - a skim handler's origin is the DRIVE flow (the Skim/SkimAll caller) —
//     the layering confluence its item merged into, distinct from the item's
//     own chain, which the handler already runs under;
//   - a funnel flush's origin is THE LAST ACCUMULATE — the one whose returned
//     deadline (or finality before close) made the flush due, reachable while
//     the instance's rolling driver pin is held (the inline past-deadline,
//     tag-free flush runs directly ON that accumulate's ctx and resolves its
//     parent instead: the reader is already at the origin's position);
//   - a follow-up fire has NO origin (ok false): the fire IS its last
//     carrier's continuation — its own ctx already reads the origin's flows;
//   - top-level and framework-pumped contexts have none (ok false) — honest
//     absence.
//
// The relationship is passive and causal — the origin occasioned this body;
// nothing in it pumps this body's execution — which is why a scope-flavored
// or drive-flavored name would be wrong (see the record's naming trail).
//
// The returned context is READ-ONLY and valid within the current synchronous
// extent (the driver-contexts machinery guarantees exactly that lifetime; the
// flush origin specifically is valid within the flush body). To keep it, pin
// or hold it while still inside: PinFlow(origin) / HoldFlow(origin).
func OriginFlow(ctx context.Context) (origin context.Context, ok bool) {
	m, found := metaFromContext(ctx)
	if !found {
		return nil, false
	}
	m.vetNotExpiredPin()

	var o *ctxMeta
	switch {
	case m.origin.Load() != nil:
		// A flush body: the last accumulate, via the rolling driver pin.
		o = m.origin.Load()
	case m.permitRoot && m.pin.Load() == pinNone &&
		(m.ctxType == skimContext || m.ctxType == topLevelContext):
		// A follow-up fire meta — async fires borrow skim-typed, the inline
		// scope-exit fire top-level-typed; both are permit roots, and the pin
		// marker is what distinguishes a pinned ctx (also a top-level permit
		// root, whose origin IS its parent, the pinned source). The fire is
		// the last carrier's continuation; there is no origin behind it.
		return nil, false
	default:
		// Every ordinary body and scope: the causal parent. For a skim
		// handler's per-item child meta this is the drive; for a borrowed
		// task/accumulate body meta it is the dispatcher; for a pinned ctx it
		// is the pinned source. permitRoot does not stop this hop — origin is
		// exactly the cross-extent relationship the parent link was kept
		// refcounted for.
		o = m.parent
	}
	if o == nil || o.selfCtx == nil {
		return nil, false
	}
	return o.selfCtx, true
}
