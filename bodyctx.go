// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"maps"

	"github.com/petenewcomb/streampool/internal/nbcq"
)

// ─────────────────────────────────────────────────────────────────────────────
// bodyCtxPool — the source-ctx-keyed reuse pool of wave-stamped body contexts.
//
// This is the successor to execShellPool (see docs/decisions/body-context-pool.md).
// Where execShellPool is per-Wave and roots every body ctx at a wave-owned waveCtx
// (so Wave.Cancel can force-abort a running body), bodyCtxPool is keyed by the
// SOURCE (submit/drive) ctx and is wave-agnostic: each pooled ctxMeta carries its
// Wave as a per-borrow stamp, and its body ctx (ctxMeta.ctx) is a plain stdlib
// descendant of the source ctx. Cancellation, deadline, and value propagation
// therefore ride context ancestry — no waveCtx, no force-abort, no custom context
// type.
//
// The reusable unit IS the ctxMeta: it carries its own body ctx (ctxMeta.ctx) and a
// back-pointer to this pool (ctxMeta.pool), so a borrower (a work item, or an inline
// scope) can own a *ctxMeta outright and return it with release(). A pool is the
// value the body-context map caches per source ctx; it self-sizes to the peak
// concurrency under that ctx via the same nbcq reuse-cache pattern as
// execShellPool.free and the funnel instanceQueue: borrow = TryPopFront-or-mint,
// return = PushBack.
// ─────────────────────────────────────────────────────────────────────────────

// bodyCtxPool hands out reusable wave-stamped body contexts (as *ctxMeta) descended
// from one source ctx. A borrowed meta's transient fields (Wave, ctxType, parent,
// heldRequest, executionEnvironment, parentJobs) are stamped per borrow and cleared on
// return; its ctx and pool back-pointer persist across borrows. The source-ctx ancestry
// basis (srcJob/srcParentJobs) is fixed at Init and feeds parentJobsFor.
type bodyCtxPool struct {
	sourceCtx context.Context //nolint:containedctx // the ancestor every body ctx derives from
	// srcJob/srcParentJobs are the source ctx's own ancestry — the Wave it is bound to
	// (nil for a top-level user ctx with no meta) and that Wave's accumulated parentJobs.
	// parentJobsFor derives a borrow's parentJobs from this basis plus the target Wave.
	srcJob        *Wave
	srcParentJobs map[*Wave]struct{}
	free          nbcq.Queue[*ctxMeta]
}

// Init wires the pool to its source ctx and the ancestry basis derived from that ctx's
// meta (srcJob = sourceMeta.job, srcParentJobs = sourceMeta.parentJobs; both nil/empty
// for a top-level user ctx). Borrowed metas' body ctxs descend from sourceCtx by ancestry.
func (p *bodyCtxPool) Init(sourceCtx context.Context, srcJob *Wave, srcParentJobs map[*Wave]struct{}) {
	p.sourceCtx = sourceCtx
	p.srcJob = srcJob
	p.srcParentJobs = srcParentJobs
	p.free.Init()
}

// borrow returns a meta ready to run a body of the given context type, bound to wave
// and under the supplied execution environment. The meta is fully (re-)stamped:
//   - job/wave        = wave (the dispatch target; wave-agnostic metas stamp it per borrow)
//   - ctxType         = ctxType
//   - parent          = parent (nil severs the permit-root chain for an async body;
//     the enclosing meta for an inline/nested borrow)
//   - heldRequest     = req (the limiter handle, nil for unlimited ops)
//   - parentJobs      = parentJobsFor(wave)
//
// The caller runs the body under the returned meta's ctx and then calls release.
func (p *bodyCtxPool) borrow(
	wave *Wave, ctxType contextType, parent *ctxMeta, req request, exEnv executionEnvironment,
) *ctxMeta {
	m, ok := p.free.TryPopFront()
	if !ok {
		m = p.newMeta()
	}
	m.job = wave
	m.wave = wave
	m.ctxType = ctxType
	m.parent = parent
	m.heldRequest = req
	m.executionEnvironment = exEnv
	m.parentJobs = p.parentJobsFor(wave)
	return m
}

// parentJobsFor computes the cross-wave ancestry a borrow targeting wave should carry,
// mirroring ensureCtxMeta: when the target wave IS the source ctx's own wave, the
// source's parentJobs pass through unchanged; otherwise the source's wave joins its
// parentJobs (the body reaches across a wave boundary). The cross-wave branch allocates
// a fresh map per call — see docs/decisions/body-context-pool.md open-question on
// caching this when one source ctx repeatedly targets the same other wave.
func (p *bodyCtxPool) parentJobsFor(wave *Wave) map[*Wave]struct{} {
	if p.srcJob == nil || wave == p.srcJob {
		return p.srcParentJobs
	}
	pj := make(map[*Wave]struct{}, len(p.srcParentJobs)+1)
	maps.Copy(pj, p.srcParentJobs)
	pj[p.srcJob] = struct{}{}
	return pj
}

// release returns a borrowed meta to its pool after its body has finished. It clears
// the transient per-borrow fields so a spent meta never pins an execution environment,
// limiter request, or parent meta between borrows; the body ctx and the pool
// back-pointer persist for reuse. A meta with no pool (not pool-minted) is a no-op.
func (p *bodyCtxPool) release(m *ctxMeta) {
	m.executionEnvironment = nil
	m.heldRequest = nil
	m.parent = nil
	p.free.PushBack(m)
}

// newMeta builds a fresh meta whose body ctx descends from sourceCtx. The meta is
// created once and reused; its address is stamped onto its own ctx under the key the
// framework reads (ctxMetaValueKey), so ctxMeta(m.ctx) resolves it via ctx.Value without
// a map lookup. The pool back-pointer is set here so release needs no extra handle.
func (p *bodyCtxPool) newMeta() *ctxMeta {
	m := &ctxMeta{pool: p}
	m.ctx = context.WithValue(p.sourceCtx, ctxMetaValueKey{}, m)
	return m
}
