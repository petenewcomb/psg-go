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

// borrowBodyContext returns a body context for running an op body of the given ctxType,
// bound to wv and descended from srcCtx, together with the *ctxMeta it carries. The
// meta is stamped:
//   - wave        = wv (the dispatch target)
//   - ctxType     = ctxType
//   - held        = h (the limiter handle, nil for unlimited ops)
//   - parentWaves = parentWavesForSource(srcCtx, wv)
//
// parent stays nil: a borrowed body is always a fresh permit-root, severing the
// permit-root chain for an async body that runs on a fungible worker (it must not
// inherit a dispatcher's permit across the goroutine boundary). The pooled meta is
// zeroed on release, so parent needs no explicit stamp.
//
// The caller runs the body under the returned ctx and then calls releaseBodyContext.
func borrowBodyContext(
	srcCtx context.Context, wv *Wave, ctxType contextType,
	h *heldPermit, exEnv executionEnvironment,
) (context.Context, *ctxMeta) {
	m := bodyMetaPool.Get()
	m.wave = wv
	m.ctxType = ctxType
	m.held = h
	m.executionEnvironment = exEnv
	srcMeta, srcOk := metaFromContext(srcCtx)
	m.parentWaves = parentWavesForSource(srcMeta, srcOk, wv)
	if srcOk {
		// Flow riders ride the DISPATCH chain: captured from the submit-time ctx
		// here at borrow (which body-creating call sites perform synchronously at
		// dispatch), unlike parent — the permit chain — which stays severed for a
		// body that runs on a fungible worker.
		m.riders = srcMeta.riders
	}
	return ctxpool.WithValue(srcCtx, m), m
}

// releaseBodyContext returns a borrowed body context's child ctx to ctxpool and its
// *ctxMeta to bodyMetaPool. The meta is read out before ctxpool.Free clears the child's
// value, then returned (and zeroed) — so neither pool retains a cross-borrow reference.
func releaseBodyContext(ctx context.Context) {
	m, _ := ctxpool.GetValue[*ctxMeta](ctx)
	ctxpool.Free(ctx)
	if m != nil {
		bodyMetaPool.Put(m)
	}
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
