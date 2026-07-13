// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import "github.com/petenewcomb/streampool/internal/permits"

// ─────────────────────────────────────────────────────────────────────────────
// Permit forest construction (docs/plan/dispatch-execution-split-phase2b.md,
// "Forest construction" + "C1 implementation mapping").
//
// A permits.Cache is created per (wave, Limiter) — C_W^L. The L-forest mirrors the
// WAVE nesting: C_V^L.parent = C_W^L when wave V was driven by a body in wave W. That
// nesting is exactly the synchronous ctxMeta chain currentHeldPermit walks
// (syncParent — stopping at permitRoot async-dispatch boundaries, the same isolation
// that scopes eager inheritance), so the forest and the eager inheritance answer the
// same question. The stop is also what keeps the liveness argument below sound: only
// a synchronous ancestor wave is guaranteed still Open by the dispatching goroutine's
// enclosing scope.
//
// At dispatch — on the driving goroutine, where the dispatching meta and its parent
// chain are available — ensureCache resolves C_T^L for the op's target wave T,
// lazily mkdir-p'ing the ancestor chain (held=0 pass-throughs for ancestor waves that
// carry no L-permit, up to the nearest existing C_^L or the Pool root). The resulting
// cache is acquired from at the gate and recorded on the body meta for reacquire.
//
// Inheritance the chain misses (an intermediate non-carrying ancestor beyond an async
// boundary) degrades to a forest-wide steal, which is still correct — the Resource
// enforces the global limit either way. The chain maximizes the cheap
// occupy-in-place inherit; the steal is the liveness backstop.
// ─────────────────────────────────────────────────────────────────────────────

// ensureCache resolves C_T^p for op-target wave T (== wv) dispatched from meta m (the
// dispatching body's meta, or nil for a fresh top-level dispatch with no enclosing
// wave), creating the forest chain as needed. The returned cache is wv's own node for
// Pool p; the body acquires its permit from it.
func (wv *waveImpl) ensureCache(m *ctxMeta, p *permits.Pool) *permits.Cache {
	// A wave-less meta (a top-level WithFlow scope) is transparent for permit
	// ancestry — resolve through its synchronous chain to the nearest
	// wave-bearing meta (nil when the scope is truly top-level).
	for m != nil && m.wave == nil {
		m = m.syncParent()
	}
	// Same-wave dispatch (ambient submit, or wv is the dispatching wave): wv's own
	// position in the forest is whatever m's chain gives — resolve it directly.
	if m != nil && m.wave == wv {
		return ensureCacheChain(m, p)
	}
	// Cross-wave dispatch (op.In(wv) drives wv as a sub-wave): wv's forest parent is
	// the dispatching wave's cache.
	if c, ok := wv.cacheFor(p); ok {
		return c
	}
	var parent *permits.Cache
	if m != nil {
		parent = ensureCacheChain(m, p)
	}
	return wv.createCache(p, parent)
}

// ensureCacheChain returns C_{m.wave}^p, mkdir-p'ing the held=0 pass-through chain up
// the synchronous meta chain (syncParent) for any ancestor waves that lack a node,
// bottoming out at the nearest existing C_^p or (when the synchronous chain ends —
// nil parent or a permitRoot async boundary) the Pool root. It holds at most
// one wave's cachesMu at a time — cacheFor and createCache each lock-and-release, and
// the recursion happens between them — so there is no cross-wave lock nesting and no
// deadlock. Each ancestor wave in the chain stays alive across the gap because the
// dispatching goroutine runs within its still-Open driving scope — the guarantee that
// holds only within one synchronous extent, which is why the walk must not cross a
// permitRoot even though the refcounted parent link continues past it.
func ensureCacheChain(m *ctxMeta, p *permits.Pool) *permits.Cache {
	wv := m.wave
	if c, ok := wv.cacheFor(p); ok {
		return c
	}
	// Find the nearest distinct ancestor wave in the synchronous chain,
	// skipping wave-less flow-scope metas (transparent, as above).
	am := m.syncParent()
	for am != nil && (am.wave == wv || am.wave == nil) {
		am = am.syncParent()
	}
	var parent *permits.Cache
	if am != nil {
		parent = ensureCacheChain(am, p)
	}
	return wv.createCache(p, parent)
}

// cacheFor returns wv's existing cache for Pool p, if any.
func (wv *waveImpl) cacheFor(p *permits.Pool) (*permits.Cache, bool) {
	wv.cachesMu.Lock()
	defer wv.cachesMu.Unlock()
	c, ok := wv.caches[p]
	return c, ok
}

// createCache creates and registers wv's cache for Pool p under parent (a root when
// parent is nil), double-checking under cachesMu so a concurrent creator wins at most
// once. The new node carries wv's self-ref (NewChild/NewCache start at refs==1),
// released at wave-Done; a NewChild also adds the descendant edge on parent.
func (wv *waveImpl) createCache(p *permits.Pool, parent *permits.Cache) *permits.Cache {
	wv.cachesMu.Lock()
	defer wv.cachesMu.Unlock()
	if wv.caches == nil {
		wv.caches = make(map[*permits.Pool]*permits.Cache)
	}
	if c, ok := wv.caches[p]; ok {
		return c // lost the race — discard the (idempotently-shared) parent
	}
	var c *permits.Cache
	if parent != nil {
		c = parent.NewChild()
	} else {
		c = p.NewCache()
	}
	wv.caches[p] = c
	return c
}

// releaseCaches drops wv's self-ref on every cache it created, run once when the wave
// reaches Done (wired as wavestate's onDone hook). A node whose subtree has fully
// drained is destroyed here (returning its held permits to the Resource); one with live
// descendant caches survives on their refs until they drain. The map is cleared so a
// reused/pooled *waveImpl starts its next cycle with a fresh forest.
func (wv *waveImpl) releaseCaches() {
	wv.cachesMu.Lock()
	caches := wv.caches
	wv.caches = nil
	wv.cachesMu.Unlock()
	for _, c := range caches {
		c.ReleaseRef()
	}
}
