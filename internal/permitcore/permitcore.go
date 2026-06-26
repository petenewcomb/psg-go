// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permitcore

// Store is the backing store for one limiter L: a capacity C and the running count
// of permits checked out of L (Σ held across every pool). It also roots the pool
// forest, which the steal search walks. See doc.go for the operational model.
type Store struct {
	capacity   int
	checkedOut int // Σ held across all pools; never exceeds capacity
	roots      []*Pool
}

// NewStore returns a backing store for a limiter of the given capacity.
func NewStore(capacity int) *Store {
	if capacity < 1 {
		panic("permitcore: capacity must be ≥ 1")
	}
	return &Store{capacity: capacity}
}

// Pool is one unit's permit holdings under a Store. Pools form a forest mirroring
// the unit→sub-wave nesting: a pool draws on its parent (the ancestor chain) and,
// failing that, on free L or a steal. See doc.go for held/inUse semantics.
type Pool struct {
	parent   *Pool
	children []*Pool

	held  int // permits this pool caches; lives in exactly one pool (conservation)
	inUse int // permits of held backing a running body (this unit or a descendant)
	refs  int // 1 for the live unit + one per live child sub-wave drawing on it
	alive bool

	s *Store

	// Sibling-list position encodes the steal telemetry structurally, with no logical
	// clock: an acquire up-walk moves each pool it passes to the back (most-recently-
	// ascended end) of its sibling list, so the front stays the least-recently-
	// ascended — the stalest, coldest, LRU steal victim. The steal search walks
	// siblings front-to-back and takes the first borrowable. (permit-core.md "The
	// steal search": the LRU victim, here by list order instead of a timestamp; the
	// spec's logical clock was only ever for this ordering.)
}

// NewRootPool creates a top-level unit's pool (no parent: it draws on free L or a
// steal, never an ancestor). It starts referenced by its own unit.
func (s *Store) NewRootPool() *Pool {
	p := &Pool{s: s, refs: 1, alive: true}
	s.roots = append(s.roots, p)
	return p
}

// NewChildPool creates a sub-wave's pool drawing on parent. It starts owning no
// permits (held == 0); its bodies inherit parent's idle permits via the ancestor
// step of Acquire, taking a delta only on a miss. The sub-wave takes a reference on
// parent (pools outlive units: parent must not return its held to L until every
// sub-wave drawing on it has drained).
func (parent *Pool) NewChildPool() *Pool {
	if !parent.alive {
		panic("permitcore: NewChildPool on a destroyed pool")
	}
	p := &Pool{s: parent.s, parent: parent, refs: 1, alive: true}
	parent.children = append(parent.children, p)
	parent.refs++ // the sub-wave draws on parent
	return p
}

// Acquire makes one permit available for a body in p to run and returns the pool
// whose inUse it bumped — the BACKING pool, to be passed to Release when the body
// completes or parks. ok is false if the body must wait (every cache, free L, and
// steal missed); the caller retries after the next Release frees a permit.
//
// This is the single locality-ordered primitive of permit-core.md "Acquisition":
// own pool → ancestor chain → free L → steal → wait.
func (p *Pool) Acquire() (backing *Pool, ok bool) {
	if !p.alive {
		panic("permitcore: Acquire on a destroyed pool")
	}
	s := p.s
	// Steps 1–2: own pool, then the ancestor chain. Occupy the nearest borrowable
	// (idle, cached) permit — a step-1 hit on p's own cache, or inheritance of a
	// parked ancestor's idle permit. The permit does not move; only inUse rises.
	for a := p; a != nil; a = a.parent {
		if a.held > a.inUse {
			a.inUse++
			return a, true
		}
		// Walked PAST a without being satisfied: a had nothing to lend, so its subtree
		// is actively demanding through it — mark it hot (move to the back of its
		// sibling list) so the steal search prefers quiescent pools. A satisfied
		// acquire (a hit, including the common step-1 own-cache hit) pays nothing and
		// is not marked: it had spare, so its remaining idle stays a fair steal target.
		// (permit-core.md "The steal search": the telemetry rides the pools the up-walk
		// passes, and "step-1 hits ... pay nothing".)
		a.touch()
	}
	// Step 3: free L capacity. Check a fresh permit out of L into p's own held —
	// the first step that raises Σ held.
	if s.checkedOut < s.capacity {
		s.checkedOut++
		p.held++
		p.inUse++
		return p, true
	}
	// Step 4: steal an idle permit from anywhere in the forest into p's own held.
	// A transfer (victim held−−, p held++), no return obligation; Σ held unchanged.
	if v := s.findStealVictim(); v != nil {
		v.held--
		p.held++
		p.inUse++
		return p, true
	}
	// Step 5: wait. Nothing free or borrowable anywhere.
	return nil, false
}

// Release marks the body backed by p (the pool Acquire returned) as no longer
// running — it completed, or parked to drive a sub-wave. p.inUse falls, but the
// permit STAYS cached in p.held (cache-don't-return), now borrowable by p's next
// acquisition or stealable by another pool.
func (p *Pool) Release() {
	if p.inUse <= 0 {
		panic("permitcore: Release underflow (no running body backed by this pool)")
	}
	p.inUse--
}

// ReleaseRef drops one reference on p — the unit exiting, or a child sub-wave that
// has drained. When the last reference goes (unit exited AND all sub-waves drained)
// the pool returns its cached held to L and detaches from the forest. Returns true
// if this call destroyed the pool.
func (p *Pool) ReleaseRef() bool {
	if p.refs <= 0 {
		panic("permitcore: ReleaseRef underflow")
	}
	p.refs--
	if p.refs > 0 {
		return false
	}
	p.destroy()
	return true
}

func (p *Pool) destroy() {
	if p.inUse != 0 {
		panic("permitcore: destroying a pool with a running body (inUse != 0)")
	}
	// Cache-don't-return ends here: the pool's last reference is gone, so its cached
	// permits return to L (Σ held falls by held).
	p.s.checkedOut -= p.held
	p.held = 0
	p.alive = false
	if p.parent != nil {
		p.parent.removeChild(p)
		p.parent.ReleaseRef() // the sub-wave's draw on the parent ends
	} else {
		p.s.removeRoot(p)
	}
}

func (p *Pool) removeChild(c *Pool) {
	for i, ch := range p.children {
		if ch == c {
			p.children = append(p.children[:i], p.children[i+1:]...)
			return
		}
	}
}

func (s *Store) removeRoot(p *Pool) {
	for i, r := range s.roots {
		if r == p {
			s.roots = append(s.roots[:i], s.roots[i+1:]...)
			return
		}
	}
}

// findStealVictim returns a borrowable pool to steal from, or nil if none is
// borrowable anywhere. It is a best-first descent guided by sibling-list order, which
// touch keeps coldest-first (front = least-recently-ascended, the LRU/least-likely-
// to-re-demand victim): at each level it walks front-to-back and returns the FIRST
// borrowable permit, so the common case terminates early rather than scanning the
// whole forest. It backtracks into the next branch only when one dead-ends, and walks
// the forest in full ONLY when nothing is borrowable anywhere — which means every
// permit is in use (saturation), the very case where the acquirer must wait
// regardless. So the exhaustive cost is paid only when it cannot help find a steal but
// must prove none exists: the liveness fallback (permit-core.md "The steal search").
func (s *Store) findStealVictim() *Pool {
	return stealSearch(s.roots)
}

func stealSearch(pools []*Pool) *Pool {
	// Siblings are kept in LRU order by touch (front = stalest), so a plain
	// front-to-back walk is already coldest-first — no sort, no clock.
	for _, p := range pools {
		if p.held > p.inUse {
			return p
		}
		if v := stealSearch(p.children); v != nil {
			return v
		}
	}
	return nil
}

// touch records that an acquire up-walk just passed through p by moving it to the
// most-recently-ascended (back) end of its sibling list. The front therefore stays
// the least-recently-ascended pool — the stalest, coldest LRU steal victim — so the
// list order is the steal telemetry, maintained structurally with no logical clock.
func (p *Pool) touch() {
	if p.parent != nil {
		moveToBack(p.parent.children, p)
	} else {
		moveToBack(p.s.roots, p)
	}
}

// moveToBack shifts p to the end of list in place (length unchanged). The caller's
// slice header is unaffected because only the backing array's order changes.
func moveToBack(list []*Pool, p *Pool) {
	for i, x := range list {
		if x == p {
			copy(list[i:], list[i+1:])
			list[len(list)-1] = p
			return
		}
	}
}
