// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"maps"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// ─────────────────────────────────────────────────────────────────────────────
// Body contexts — two decoupled pools (docs/decisions/body-context-pool.md).
//
// Running an op body needs a context.Context stamped with a wave-bound *ctxMeta,
// descended from the body's source (submit/drive) ctx so cancellation/deadline/value
// propagation ride ancestry. The two reusable parts are pooled INDEPENDENTLY:
//   - the child context.Context — by internal/ctxpool, keyed on the source ctx, with
//     AfterFunc eviction. ctxpool reuses the WithValue node; it is value-agnostic.
//   - the *ctxMeta value — by bodyMetaPool here. A borrow draws a meta, stamps it with
//     the wave + call-specific fields, and hands it to ctxpool as the child's value;
//     release returns it.
//
// This replaces the per-wave execShell (which rooted body ctxs at a wave-owned waveCtx
// for force-abort). Cancellation is now pure source-ctx ancestry — no waveCtx.
// ─────────────────────────────────────────────────────────────────────────────

// bodyMetaPool reuses *ctxMeta values independently of the child contexts that carry
// them. A pooled meta is fully (re-)stamped on every borrow and zeroed on Put, so it
// never pins a wave, execution environment, or limiter request between borrows.
var bodyMetaPool = omnipool.For[ctxMeta]()

// newBorrowedMeta is the refcount core shared by every async-body meta (op-body
// borrows, funnel-flush and follow-up-fire continuations): a fresh meta, owner
// ref included, whose parent is srcMeta — PINNED (refMeta) so the borrowed-from
// context outlives this body however long it runs — and which is a permitRoot
// (the body runs on a fungible worker goroutine; synchronous-extent walks stop
// here). srcMeta must be provably alive at the call: resolved synchronously at
// dispatch, or held by an Execute-stash pin. The caller stamps the
// call-specific fields (held, exEnv, parentWaves, riders) and eventually
// releases via releaseBodyContext.
func newBorrowedMeta(
	srcCtx context.Context, srcMeta *ctxMeta, wv *Wave, ctxType contextType,
) (context.Context, *ctxMeta) {
	m := newCtxMeta()
	m.wave = wv
	m.ctxType = ctxType
	m.parent = srcMeta
	refMeta(srcMeta)
	m.permitRoot = true
	ctx := ctxpool.WithValue(srcCtx, m)
	m.selfCtx = ctx
	return ctx, m
}

// borrowBodyContext returns a body context for running an op body of the given ctxType,
// bound to wv and descended from srcCtx, together with the *ctxMeta it carries. The
// meta is stamped:
//   - wave        = wv (the dispatch target)
//   - ctxType     = ctxType
//   - held        = h (the limiter handle, nil for unlimited ops)
//   - parent      = srcMeta, ref-pinned (the borrowed-from context stays alive
//     for the body's whole life); permitRoot is set, so synchronous-extent
//     walks still treat the body as a fresh root
//   - parentWaves = parentWavesForSource(srcMeta, wv)
//
// srcMeta is passed in explicitly rather than re-read from srcCtx: dispatch
// sites resolve it synchronously on the dispatcher's goroutine, and the
// stashed-continuation sites (flush/fire) resolve-and-pin it in Execute — a
// lazy read at Run was the borrowSrcCtx use-after-free
// (docs/decisions/ctxmeta-parent-refcount.md).
//
// The caller runs the body under the returned ctx and then calls releaseBodyContext.
func borrowBodyContext(
	srcCtx context.Context, srcMeta *ctxMeta, wv *Wave, ctxType contextType,
	h *heldPermit, exEnv executionEnvironment,
) (context.Context, *ctxMeta) {
	ctx, m := newBorrowedMeta(srcCtx, srcMeta, wv, ctxType)
	m.held = h
	m.executionEnvironment = exEnv
	m.parentWaves = parentWavesForSource(srcMeta, srcMeta != nil, wv)
	if srcMeta != nil {
		// Flow riders ride the DISPATCH chain: captured from the submit-time ctx
		// here at borrow, which every borrowBodyContext call site performs
		// synchronously at dispatch — so the chain's own refs are provably held
		// by the registering scope (parent-covers-children) and an instance live
		// at the submit call cannot reach zero first. The borrow takes one
		// carrier ref per follow-up instance plus a node ref on the head;
		// releaseBodyContext releases them. (The stashed-continuation paths do
		// NOT capture riders: the meta pin does not cover the dispatcher's rider
		// chain — that is the deferred driver-link rider pin.)
		m.riders = srcMeta.riders
		flowRefRiders(m.riders)
		nodeRef(m.riders) // this body's carrier ref on the chain head
	}
	return ctx, m
}

// releaseBodyContext is the owner's release of a borrowed body context: it
// releases the flow carrier refs the borrow took (a release that ends an
// instance's flow hands the firing to the executor — never inline, since this
// path runs inside completion/Free machinery, before the item's wave reference
// drops) and then drops the meta's owner ref. The meta, its ctxpool child, and
// its pins up the parent chain recycle in unrefMeta when the count drains —
// immediately in the common case, or when the last body borrowed FROM this
// context completes.
func releaseBodyContext(ctx context.Context) {
	m, _ := ctxpool.GetValue[*ctxMeta](ctx)
	if m == nil {
		ctxpool.Free(ctx)
		return
	}
	riders := m.riders
	wave := m.wave // the wave a fire dispatched by the rider release routes into
	// m is the carrier whose release may end a flow: a fire dispatched by this
	// walk runs as m's continuation. m's owner ref is still held here (dropped
	// by the unrefMeta below), which is what makes the fire's dispatch pin on
	// it sound — the doc's "count→0 dispatch is a synchronous safe point".
	//nolint:contextcheck // an async fire's body ctx roots at the scheduler ctx by design
	flowUnrefRiders(riders, wave, m) // walk (may fire) BEFORE the chain can reclaim
	nodeUnref(riders)                // release this body's head ref (cascades if last)
	unrefMeta(m)
}

// parentWavesForSource computes the cross-wave ancestry a body bound to wv should
// carry, derived from the source ctx's meta (mirrors ensureCtxMeta): a top-level
// source (no meta) carries none; a same-wave source passes its parentWaves through
// unchanged; a cross-wave source joins its own wave into its parentWaves (the body
// reaches across a wave boundary). The cross-wave branch allocates a fresh map per
// call — see docs/decisions/body-context-pool.md on caching this for a hot redirect.
// The meta is passed in (rather than looked up) so borrowBodyContext resolves it once.
func parentWavesForSource(srcMeta *ctxMeta, ok bool, wv *Wave) map[*Wave]struct{} {
	if !ok || srcMeta.wave == nil || srcMeta.wave == wv {
		if ok {
			return srcMeta.parentWaves
		}
		return nil
	}
	pw := make(map[*Wave]struct{}, len(srcMeta.parentWaves)+1)
	maps.Copy(pw, srcMeta.parentWaves)
	pw[srcMeta.wave] = struct{}{}
	return pw
}
