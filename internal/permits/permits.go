// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import "sync"

// Concurrency model (Phase 2a-ii)
//
//   - Per-cache (held, inUse) live in an atomic128 word (see counts.go). Every
//     counter transition is a lock-free gated CAS, so the per-body hot path —
//     acquire steps 1–2 and Release — never takes a lock. That is what lets a pool
//     of managers admit in parallel.
//   - The per-Pool mutex protects only forest STRUCTURE: the root list, each cache's
//     children/refs/alive, and it serializes the steal walk against destroy. These
//     are per-wave/per-steal operations, off the per-body hot path.
//   - The acquire up-walk (steps 1–2) reads the ancestor chain without the lock,
//     safely: a live cache holds a reference on its parent (NewChild bumps it), so an
//     ancestor cannot be destroyed while a descendant might draw on it. The chain is
//     pinned by refcounts.
//   - The steal DOWN-walk (step 4) reads children, which destroy mutates, so it runs
//     under the lock; the actual held-- is a revalidating CAS (a concurrent lock-free
//     acquire may have consumed the victim's idle permit since the walk saw it).
//
// The move-to-back steal telemetry of the sequential sketch is dropped here: it is
// not lock-free, so it cannot ride the hot path. The steal is the exhaustive baseline
// (find any borrowable) — the liveness fallback permit-core.md designates as
// shippable, with LRU victim selection left as a measurement-gated optimization.

// Resource is the pluggable accounting object permits are drawn from — the open
// extension point (semaphore, memory, rate, weighted). It is the only thing that
// knows capacity; n carries the weight (always 1 in this weight-1 cut).
type Resource interface {
	TryAcquire(n int) bool
	Release(n int)
}

// Pool is the Resource boundary and the root of a forest of caches. It is the only
// place permits cross in or out of the Resource, and it owns the cross-subtree steal.
type Pool struct {
	resource Resource

	// mu guards forest structure (roots + each cache's children/refs/alive) and
	// serializes the steal walk against destroy. The per-body counter hot path does
	// NOT take it.
	mu    sync.Mutex
	roots []*Cache
}

// NewPool returns a Pool drawing permits from r.
func NewPool(r Resource) *Pool {
	if r == nil {
		panic("permits: nil Resource")
	}
	return &Pool{resource: r}
}

// NewCache creates a top-level cache (a tree root drawing on the Pool).
func (p *Pool) NewCache() *Cache {
	c := &Cache{pool: p, refs: 1, alive: true}
	p.mu.Lock()
	p.roots = append(p.roots, c)
	p.mu.Unlock()
	return c
}

// Cache is a per-unit node in the Pool's forest. Its (held, inUse) is the lock-free
// atomic counter; its structural fields (children/refs/alive) are guarded by pool.mu.
// parent is immutable after construction, so the up-walk reads it lock-free.
type Cache struct {
	pool   *Pool
	parent *Cache
	counts counts

	// guarded by pool.mu:
	children []*Cache
	refs     int
	alive    bool
}

// NewChild creates a sub-wave's cache drawing on parent. The sub-wave takes a
// reference on parent (caches outlive units), which also pins parent in the ancestor
// chain so this cache's acquire up-walk can read it without the lock.
func (parent *Cache) NewChild() *Cache {
	p := parent.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if !parent.alive {
		panic("permits: NewChild on a destroyed cache")
	}
	c := &Cache{pool: p, parent: parent, refs: 1, alive: true}
	parent.children = append(parent.children, c)
	parent.refs++
	return c
}

// Acquire makes one permit available for a body in c to run and returns a Permit
// recording the backing cache (Release it when the body completes or parks). ok is
// false if the body must wait (every cache, the Resource, and every steal missed).
func (c *Cache) Acquire() (Permit, bool) {
	// Steps 1–2: lock-free up-walk. Ancestors are pinned by refcounts, so the chain
	// is stable without the lock; each hop is a gated CAS.
	for a := c; a != nil; a = a.parent {
		if a.counts.acquireLocal() {
			return Permit{a}, true
		}
	}
	// Steps 3–4 (the Resource boundary, on the Pool): free Resource, then steal.
	if backing := c.pool.acquireInto(c); backing != nil {
		return Permit{backing}, true
	}
	// Step 5: wait.
	return Permit{}, false
}

// acquireInto runs steps 3–4 for c, landing the permit in c's own held. Returns c on
// success or nil if both missed (the caller waits).
func (p *Pool) acquireInto(c *Cache) *Cache {
	// Step 3: free Resource — lock-free (the Resource is its own atomic).
	if p.resource.TryAcquire(1) {
		c.counts.checkout()
		return c
	}
	// Step 4: steal, under the lock (the down-walk reads children that destroy
	// mutates). Recheck the Resource first: a concurrent destroy may have returned
	// capacity since the lock-free miss above.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resource.TryAcquire(1) {
		c.counts.checkout()
		return c
	}
	// Find a borrowable victim and transfer one permit. stealOut is a revalidating
	// CAS: a concurrent lock-free acquire may have taken the victim's idle permit
	// since the walk observed it, so on failure look again — each failure leaves that
	// victim non-borrowable until a release, so the search makes progress.
	for {
		v := findBorrowable(p.roots)
		if v == nil {
			return nil // step 5: nothing borrowable anywhere
		}
		if v.counts.stealOut() {
			c.counts.checkout()
			return c
		}
	}
}

// findBorrowable returns the first cache in the forest with an idle (borrowable)
// permit, or nil. Caller holds pool.mu (the structure must be stable during the
// walk); counts are read atomically.
func findBorrowable(caches []*Cache) *Cache {
	for _, c := range caches {
		if h, u := c.counts.load(); h > u {
			return c
		}
		if v := findBorrowable(c.children); v != nil {
			return v
		}
	}
	return nil
}

// Permit is the transient handle for a body occupying one permit, recording the
// backing cache whose inUse it raised.
type Permit struct {
	backing *Cache
}

// Release ends the run segment the Permit backed; the backing cache's inUse falls but
// the permit stays cached in its held (cache-don't-return), now borrowable.
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	pm.backing.counts.release()
	// 2a-iii will Notify the Pool's waiters on the borrowable 0→1 crossing that
	// release reports.
}

// ReleaseRef drops one reference on c — the unit exiting, or a child sub-wave that
// has drained. The last reference returns c's held to the Resource and detaches it.
func (c *Cache) ReleaseRef() bool {
	p := c.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	return c.releaseRefLocked()
}

func (c *Cache) releaseRefLocked() bool {
	if c.refs <= 0 {
		panic("permits: ReleaseRef underflow")
	}
	c.refs--
	if c.refs > 0 {
		return false
	}
	c.destroyLocked()
	return true
}

// destroyLocked detaches c and returns its held to the Resource. Holds pool.mu. By
// the time refs reaches zero the unit has exited and all sub-waves have drained, so c
// is quiescent (no live acquirer; the steal walk is serialized by pool.mu), and
// inUse must be zero.
func (c *Cache) destroyLocked() {
	held, inUse := c.counts.load()
	if inUse != 0 {
		panic("permits: destroying a cache with a running body (inUse != 0)")
	}
	if held > 0 {
		//nolint:gosec // G115: held is a permit count bounded by the Resource's capacity
		c.pool.resource.Release(int(held))
		c.counts.store(0, 0)
	}
	c.alive = false
	if c.parent != nil {
		c.parent.removeChildLocked(c)
		c.parent.releaseRefLocked() // the sub-wave's draw on the parent ends
	} else {
		c.pool.removeRootLocked(c)
	}
}

func (c *Cache) removeChildLocked(child *Cache) {
	for i, ch := range c.children {
		if ch == child {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

func (p *Pool) removeRootLocked(c *Cache) {
	for i, r := range p.roots {
		if r == c {
			p.roots = append(p.roots[:i], p.roots[i+1:]...)
			return
		}
	}
}
