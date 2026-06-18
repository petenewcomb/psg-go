// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

// ─────────────────────────────────────────────────────────────────────────────
// execShell — the per-Wave borrowed execution context (wave-5b).
//
// A fungible global-pool worker holds only its per-worker execution environment E
// (the rdvq buffering substrate). To run a body it borrows an execShell from the
// body's Wave: the shell supplies the per-wave EXECUTION CONTEXT (cancellation +
// ctxMeta), the worker supplies E. The body runs under shell.ctx with E stamped
// into shell.meta, then the shell returns to its pool.
//
// Why per-wave, reused, not cancelled between borrows:
//   - The execCtx is WithCancel(waveCtx), so per-wave cancellation and global pool
//     teardown both reach the running body by stdlib context ancestry — no custom
//     hook. (waveCtx = WithCancel(poolCtx); see worker.Pool.PoolCtx.)
//   - Each shell has its OWN derived context, so its done channel is distinct.
//     Bodies don't all park on a single shared waveCtx.Done(), which would make
//     that one channel a park-lock contention point.
//   - The shell (context + ctxMeta) is reused across many bodies rather than
//     rebuilt per execution (prior art: funnelInstanceQueue). reuse-not-cancel: a
//     borrow re-stamps the transient meta fields; the context is only cancelled
//     when waveCtx tears the whole wave down.
//
// The shell REPLACES the legacy per-worker ctx creation (WithCancel(j.ctx) +
// ensureCtxMeta in runTasks / FunnelPool.goroutine) and the ctxMetaMap caching for
// worker-executed bodies: the shell IS the reused, cached meta. j.ctxMeta(shell.ctx)
// still resolves the meta — ctxmap.Map.WithValue finds it via ctx.Value (the meta
// is stamped under ctxMetaValueKey), so the rest of the framework is unchanged.
// ─────────────────────────────────────────────────────────────────────────────

// execShell is one reusable per-Wave execution context + metadata. ctx is
// WithValue(WithCancel(waveCtx), ctxMetaValueKey{}, meta); meta is the same pointer
// the ctx carries, exposed so a borrow can re-stamp it without a ctx.Value lookup.
type execShell struct {
	ctx    context.Context //nolint:containedctx // the per-wave exec ctx a body runs under
	cancel context.CancelFunc
	meta   *ctxMeta
}

// execShellPool hands out execShells for one Wave. It is owned by that Wave and
// constructed once its waveCtx + lifecycle Pool are known. Borrows come from a
// lock-free free list (nbcq); when empty a fresh shell is built under waveCtx, so
// the live shell count self-sizes to the wave's peak concurrency and shrinks back
// as borrows return. Shells live for the wave's lifetime and are released when
// waveCtx cancels (which cancels each shell's derived context by ancestry).
type execShellPool struct {
	waveCtx context.Context //nolint:containedctx // the ancestor every shell ctx derives from
	job     *Pool           // the per-wave lifecycle object stamped as meta.job
	wave    *Wave           // stamped as meta.wave (the shells are this wave's)
	free    nbcq.Queue[*execShell]
}

// Init wires the pool to its wave. waveCtx is the ancestor for every shell context
// (so wave cancel / pool teardown propagate); job and wave are the fixed
// per-execution identity stamped into every shell's meta.
func (p *execShellPool) Init(waveCtx context.Context, job *Pool, wave *Wave) {
	p.waveCtx = waveCtx
	p.job = job
	p.wave = wave
	p.free.Init()
}

// borrow returns a shell ready to run a body of the given context type under the
// supplied execution environment E (the borrowing worker's). The shell's meta is
// re-stamped: ctxType + executionEnvironment for this body; parent and heldRequest
// reset to the permit-root state (the body stamps its own heldRequest at entry).
// The caller runs the body under the returned shell.ctx and then calls giveBack.
func (p *execShellPool) borrow(ctxType contextType, exEnv executionEnvironment) *execShell {
	s, ok := p.free.TryPopFront()
	if !ok {
		s = p.newShell()
	}
	m := s.meta
	m.ctxType = ctxType
	m.executionEnvironment = exEnv
	// Worker-run bodies are fresh permit-roots: parent == nil severs the
	// dispatcher's held-permit chain across the goroutine boundary (a worker may
	// run a subjob body whose waveCtx carries a foreign meta). heldRequest is
	// stamped by the body at entry and restored on exit.
	m.parent = nil
	m.heldRequest = nil
	return s
}

// giveBack returns a borrowed shell to the free list after its body has finished.
// It clears the transient per-borrow meta fields so a spent shell never pins an
// execution environment or limiter request between borrows; the context (and the
// job/wave identity) persist for reuse.
func (p *execShellPool) giveBack(s *execShell) {
	s.meta.executionEnvironment = nil
	s.meta.heldRequest = nil
	p.free.PushBack(s)
}

// newShell builds a fresh shell under waveCtx. The meta is created once and reused;
// its fixed identity (job, wave) is set here, its transient fields by borrow.
func (p *execShellPool) newShell() *execShell {
	execCtx, cancel := context.WithCancel(p.waveCtx)
	meta := &ctxMeta{
		job:    p.job,
		wave:   p.wave,
		parent: nil,
	}
	// Stamp the meta onto the context under the same key the framework reads
	// (ctxMetaValueKey); ctxmap.Map.WithValue resolves it via ctx.Value, so
	// j.ctxMeta(shell.ctx) returns this meta without going through the cache.
	ctx := context.WithValue(execCtx, ctxMetaValueKey{}, meta)
	return &execShell{ctx: ctx, cancel: cancel, meta: meta}
}

// release cancels the shells currently in the free list. Normally unnecessary —
// waveCtx cancellation already cancels each shell's derived context by ancestry —
// but draining the free list and cancelling explicitly lets a wave promptly
// release the AfterFunc links to waveCtx without waiting for GC. Shells in flight
// (borrowed, not yet returned) are not in the free list; their bodies are already
// being cancelled via waveCtx and their contexts are reaped when those bodies
// return the shell or are GC'd.
func (p *execShellPool) release() {
	for {
		s, ok := p.free.TryPopFront()
		if !ok {
			return
		}
		s.cancel()
	}
}
