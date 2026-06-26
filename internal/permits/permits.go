// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

// Resource is the pluggable accounting object permits are drawn from — the open
// extension point (semaphore, memory, rate, weighted; each a small accounting
// object that knows its own capacity). A Pool checks permits out of it and returns
// them; the n parameter carries the weight (always 1 in this weight-1 sketch, but
// the signature leaves room for weighted resources without reworking the cache).
type Resource interface {
	// TryAcquire checks out n units if the resource can satisfy them now, returning
	// false without acquiring otherwise.
	TryAcquire(n int) bool
	// Release returns n previously-acquired units.
	Release(n int)
}

// Pool is the Resource boundary and the root of a forest of caches. It is the only
// place permits cross in or out of the Resource — a cache checks one OUT (step 3 of
// acquire) and a destroyed cache drains its held back THROUGH the Pool to the
// Resource — and it owns the cross-subtree steal search. The Pool itself never
// caches. See doc.go for the operational model.
type Pool struct {
	resource Resource
	roots    []*Cache
	// checkedOut mirrors Σheld across every cache, kept in lockstep with the
	// Resource so the conservation invariant is Pool-local.
	checkedOut int
}

// NewPool returns a Pool drawing permits from r.
func NewPool(r Resource) *Pool {
	if r == nil {
		panic("permits: nil Resource")
	}
	return &Pool{resource: r}
}

// NewCache creates a top-level cache (a tree root drawing on the Pool — typically a
// top-level wave). It starts referenced by its own unit and owning no permits.
func (p *Pool) NewCache() *Cache {
	c := &Cache{pool: p, refs: 1, alive: true}
	p.roots = append(p.roots, c)
	return c
}

// Cache is a per-unit node in the Pool's forest — one per wave/sub-wave. Caches
// cache-don't-return: a finished body's permit stays in held (borrowable) until the
// cache is destroyed or another cache steals it. See doc.go for held/inUse.
type Cache struct {
	pool     *Pool
	parent   *Cache
	children []*Cache

	held  int // permits this cache holds; lives in exactly one cache (conservation)
	inUse int // permits of held backing a running body (this unit or a descendant)
	refs  int // 1 for the live unit + one per live child sub-wave drawing on it
	alive bool

	// Sibling-list position is the steal telemetry, with no logical clock: an
	// acquire that passes through a cache WITHOUT being satisfied moves it to the
	// back (touch), so the front of each sibling list stays the least-recently-
	// passed — the stalest, coldest LRU steal victim. (permit-core.md "The steal
	// search": list order replaces the spec's logical clock, which was only ever for
	// this ordering.)
}

// NewChild creates a sub-wave's cache drawing on parent. It starts owning no permits
// (held == 0); its bodies inherit parent's idle permits via the ancestor step of
// Acquire, taking a delta only on a miss. The sub-wave takes a reference on parent
// (caches outlive units: parent must not return its held to the Resource until every
// sub-wave drawing on it has drained).
func (parent *Cache) NewChild() *Cache {
	if !parent.alive {
		panic("permits: NewChild on a destroyed cache")
	}
	c := &Cache{pool: parent.pool, parent: parent, refs: 1, alive: true}
	parent.children = append(parent.children, c)
	parent.refs++ // the sub-wave draws on parent
	return c
}

// Acquire makes one permit available for a body in c to run and returns a Permit
// recording the backing cache (to be Released when the body completes or parks). ok
// is false if the body must wait — every cache, the Resource, and every steal
// missed; the caller (a manager) postpones, or (an executor) blocks and retries
// after the next Release frees a permit.
//
// The single locality-ordered primitive of permit-core.md "Acquisition": own cache →
// ancestor chain → free Resource → steal → wait.
func (c *Cache) Acquire() (Permit, bool) {
	if !c.alive {
		panic("permits: Acquire on a destroyed cache")
	}
	// Steps 1–2: own cache, then the ancestor chain. Occupy the nearest borrowable
	// (idle, cached) permit — a step-1 hit on c's own cache, or inheritance of a
	// parked ancestor's idle permit. The permit does not move; only inUse rises.
	for a := c; a != nil; a = a.parent {
		if a.held > a.inUse {
			a.inUse++
			return Permit{a}, true
		}
		// Walked PAST a without being satisfied: a had nothing to lend, so its
		// subtree is actively demanding through it — mark it hot (move to the back of
		// its sibling list) so the steal search prefers quiescent caches. A satisfied
		// acquire (a hit, including the common step-1 own-cache hit) pays nothing and
		// is not marked: it had spare, so its remaining idle stays a fair steal
		// target. (permit-core.md "The steal search": "step-1 hits ... pay nothing".)
		a.touch()
	}
	// Steps 3–4 (delegated to the Pool, the Resource boundary): check a fresh permit
	// out of the Resource, or steal an idle one from the forest, into c's own held.
	if backing := c.pool.acquireInto(c); backing != nil {
		return Permit{backing}, true
	}
	// Step 5: wait. Nothing free or borrowable anywhere.
	return Permit{}, false
}

// acquireInto runs steps 3–4 for c — free Resource, then steal — checking the
// resulting permit into c's own held. Returns c on success (the backing cache) or
// nil if both missed.
func (p *Pool) acquireInto(c *Cache) *Cache {
	// Step 3: free Resource capacity — the first step that raises Σheld.
	if p.resource.TryAcquire(1) {
		p.checkedOut++
		c.held++
		c.inUse++
		return c
	}
	// Step 4: steal an idle permit from anywhere in the forest into c's held. A
	// transfer (victim held−−, c held++), no return obligation; Σheld unchanged.
	if v := p.findStealVictim(); v != nil {
		v.held--
		c.held++
		c.inUse++
		return c
	}
	return nil
}

// Permit is the transient handle for a body occupying one permit, recording the
// backing cache whose inUse it raised. It is a value (allocation-free); a body
// acquires one to run and Releases it on completion or park.
type Permit struct {
	backing *Cache
}

// Release ends the run segment the Permit backed — the body completed, or parked to
// drive a sub-wave. The backing cache's inUse falls, but the permit STAYS cached in
// its held (cache-don't-return), now borrowable by that cache's next acquisition or
// stealable by another.
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	if pm.backing.inUse <= 0 {
		panic("permits: Release underflow (no running body backed by this cache)")
	}
	pm.backing.inUse--
}

// ReleaseRef drops one reference on c — the unit exiting, or a child sub-wave that
// has drained. When the last reference goes (unit exited AND all sub-waves drained)
// the cache returns its held to the Resource and detaches from the forest. Returns
// true if this call destroyed the cache.
func (c *Cache) ReleaseRef() bool {
	if c.refs <= 0 {
		panic("permits: ReleaseRef underflow")
	}
	c.refs--
	if c.refs > 0 {
		return false
	}
	c.destroy()
	return true
}

func (c *Cache) destroy() {
	if c.inUse != 0 {
		panic("permits: destroying a cache with a running body (inUse != 0)")
	}
	// Cache-don't-return ends here: the cache's last reference is gone, so its held
	// permits cross back out through the Pool to the Resource (Σheld falls by held).
	if c.held > 0 {
		c.pool.resource.Release(c.held)
		c.pool.checkedOut -= c.held
		c.held = 0
	}
	c.alive = false
	if c.parent != nil {
		c.parent.removeChild(c)
		c.parent.ReleaseRef() // the sub-wave's draw on the parent ends
	} else {
		c.pool.removeRoot(c)
	}
}

// touch records that an acquire up-walk passed through c without being satisfied by
// moving it to the most-recently-passed (back) end of its sibling list. The front
// therefore stays the least-recently-passed cache — the stalest, coldest LRU steal
// victim — so the list order is the steal telemetry, maintained with no logical clock.
func (c *Cache) touch() {
	if c.parent != nil {
		moveToBack(c.parent.children, c)
	} else {
		moveToBack(c.pool.roots, c)
	}
}

// findStealVictim returns a borrowable cache to steal from, or nil if none is
// borrowable anywhere. It is a best-first descent guided by sibling-list order, which
// touch keeps coldest-first (front = least-recently-passed): at each level it walks
// front-to-back and returns the FIRST borrowable permit, terminating early. It walks
// the forest in full ONLY when nothing is borrowable anywhere — which means every
// permit is in use (saturation), the case where the acquirer must wait regardless. So
// the exhaustive cost is paid only to prove no steal exists: the liveness fallback
// (permit-core.md "The steal search").
func (p *Pool) findStealVictim() *Cache {
	return stealSearch(p.roots)
}

func stealSearch(caches []*Cache) *Cache {
	for _, c := range caches {
		if c.held > c.inUse {
			return c
		}
		if v := stealSearch(c.children); v != nil {
			return v
		}
	}
	return nil
}

func (c *Cache) removeChild(child *Cache) {
	for i, ch := range c.children {
		if ch == child {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

func (p *Pool) removeRoot(c *Cache) {
	for i, r := range p.roots {
		if r == c {
			p.roots = append(p.roots[:i], p.roots[i+1:]...)
			return
		}
	}
}

// moveToBack shifts c to the end of list in place (length unchanged); only the
// backing array's order changes, so the caller's slice header stays valid.
func moveToBack(list []*Cache, c *Cache) {
	for i, x := range list {
		if x == c {
			copy(list[i:], list[i+1:])
			list[len(list)-1] = c
			return
		}
	}
}
