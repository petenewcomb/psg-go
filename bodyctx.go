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
// bound to wave and descended from srcCtx, together with the *ctxMeta it carries. The
// meta is stamped:
//   - job/wave    = wave (the dispatch target)
//   - ctxType     = ctxType
//   - parent      = parent (nil severs the permit-root chain for an async body run on a
//     fungible worker; the enclosing meta for an inline/nested borrow)
//   - heldRequest = req (the limiter handle, nil for unlimited ops)
//   - parentJobs  = parentJobsForSource(srcCtx, wave)
//
// The caller runs the body under the returned ctx and then calls releaseBodyContext.
func borrowBodyContext(
	srcCtx context.Context, wave *Wave, ctxType contextType, parent *ctxMeta,
	req request, exEnv executionEnvironment,
) (context.Context, *ctxMeta) {
	m := bodyMetaPool.Get()
	m.job = wave
	m.wave = wave
	m.ctxType = ctxType
	m.parent = parent
	m.heldRequest = req
	m.executionEnvironment = exEnv
	m.parentJobs = parentJobsForSource(srcCtx, wave)
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

// parentJobsForSource computes the cross-wave ancestry a body bound to wave should
// carry, derived from srcCtx's own meta (mirrors ensureCtxMeta): a top-level source
// (no meta) carries none; a same-wave source passes its parentJobs through unchanged;
// a cross-wave source joins its own wave into its parentJobs (the body reaches across a
// wave boundary). The cross-wave branch allocates a fresh map per call — see
// docs/decisions/body-context-pool.md on caching this for a hot redirect.
func parentJobsForSource(srcCtx context.Context, wave *Wave) map[*Wave]struct{} {
	srcMeta, ok := metaFromContext(srcCtx)
	if !ok || srcMeta.job == nil || srcMeta.job == wave {
		if ok {
			return srcMeta.parentJobs
		}
		return nil
	}
	pj := make(map[*Wave]struct{}, len(srcMeta.parentJobs)+1)
	maps.Copy(pj, srcMeta.parentJobs)
	pj[srcMeta.job] = struct{}{}
	return pj
}
