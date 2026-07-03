// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/rdvq"
)

// Concurrency model (Phase 2a-ii, hybrid: lock-free hot path, locked forest)
//
// The hot acquire path is lock-free; the forest structure is guarded by per-list
// mutexes. The two are independently synchronized and meet only through the atomic
// counter.
//
//   - Per-cache (held, inUse) is the atomic128 counter (counts.go). Acquire steps 1–2
//     (own cache, then the ancestor chain) are a lock-free gated CAS up-walk; ancestors
//     are pinned by refcounts (a live cache refs its parent), so the walk reads parent
//     pointers without a lock. Step 3 is the Resource's own atomic. Release is one CAS.
//   - Each cache's children, and the Pool's roots, are an intrusive doubly-linked
//     cacheList guarded by a per-list mutex. A cache's prev/next links are guarded by
//     the mutex of the list that contains it (its parent's children, or the roots).
//   - touch (move-to-back) is an O(1) interior relink under that one lock: an acquire
//     up-walk that passes a cache WITHOUT being satisfied marks it hot by moving it to
//     the back of its sibling list, so the list stays coldest-first and the steal's
//     front-to-back walk is least-recently-active-first. A satisfied hit pays nothing.
//   - The steal (step 4) is the exhaustive forest walk liveness rests on: a front-to-
//     back DFS taking the FIRST borrowable cache (the coldest, by touch order; left in
//     place, so a still-borrowable victim stays at the front and is re-picked — order-
//     based camping, no churn). The take is a revalidating atomic CAS (a concurrent
//     acquire may consume the idle permit between the walk and the take). It holds each
//     level's lock while scanning and descends holding the parent's lock too — nested,
//     but ALWAYS root→leaf, and it is the only operation that holds two list locks at
//     once, so no lock-order cycle can form (every other op takes a single list lock).
//   - destroy runs only at refs==0 (unit exited AND all sub-waves drained → quiescent).
//     It unlinks the cache from its list (exact removal under the list lock), CAS-drains
//     held back to the Resource (counts.drain, coordinating with a concurrent stealOut
//     so conservation holds), and decrements the parent's refs (cascade).

// Resource is the pluggable accounting object permits are drawn from — the open
// extension point (semaphore, memory, rate, weighted); n carries the amount being
// checked out: a whole weight on the single-grant fast path, or the gather's current
// shortfall (all-or-nothing until the TryAcquireUpTo capability lands,
// weighted-acquisition.md sequencing step 3).
type Resource interface {
	TryAcquire(n int) bool
	Release(n int)
}

// cacheList is an intrusive doubly-linked list of caches guarded by its own mutex,
// kept in coldest-first (front) → hottest-last (back) order by touch. The members'
// prev/next links are guarded by this mutex. The unexported helpers (linkTail,
// unlink) assume mu is held; the exported-shape methods take it.
type cacheList struct {
	mu   sync.Mutex
	head *Cache // front: least-recently-touched (coldest, preferred steal victim)
	tail *Cache // back: most-recently-touched (hottest)
}

// linkTail appends c at the back; mu must be held and c must not be in any list.
func (l *cacheList) linkTail(c *Cache) {
	c.prev = l.tail
	c.next = nil
	if l.tail != nil {
		l.tail.next = c
	} else {
		l.head = c
	}
	l.tail = c
}

// unlink removes c from this list; mu must be held and c must be a member.
func (l *cacheList) unlink(c *Cache) {
	if c.prev != nil {
		c.prev.next = c.next
	} else {
		l.head = c.next
	}
	if c.next != nil {
		c.next.prev = c.prev
	} else {
		l.tail = c.prev
	}
	c.prev = nil
	c.next = nil
}

func (l *cacheList) pushBack(c *Cache) {
	l.mu.Lock()
	l.linkTail(c)
	l.mu.Unlock()
}

func (l *cacheList) remove(c *Cache) {
	l.mu.Lock()
	l.unlink(c)
	l.mu.Unlock()
}

// moveToBack marks c hot by moving it to the back, an O(1) interior relink. A no-op if
// c is already the tail.
func (l *cacheList) moveToBack(c *Cache) {
	l.mu.Lock()
	if l.tail != c {
		l.unlink(c)
		l.linkTail(c)
	}
	l.mu.Unlock()
}

// Pool is the Resource boundary and the root of a forest of caches.
type Pool struct {
	resource Resource
	roots    cacheList

	// notify routes a freed permit to one waiting consumer with renotify conservation.
	// Its embedded Listeners are non-blocking manager postpones (register a callback via
	// ListenersFor, re-run admission when fired); its embedded Waiters are blocked
	// executors — AcquireWait and the top-level block-and-help gate (which waits on
	// Waiters() while help-draining). On a freed permit, Notify(nil) wakes ONE consumer
	// (listeners before waiters) but hands it a renotify so a consumer that cannot use
	// the wake re-delivers it to the next — the conservation that prevents a stale
	// postpone listener from swallowing a wake a real waiter needed.
	notify rdvq.Notifier

	// cachePool recycles Cache nodes (the process-wide shared pool for the type; caches
	// are fungible across Pools — newCache re-stamps pool/parent). Safe to recycle on
	// destroy only because the steal ref-pins its victim, so a Cache is never reclaimed
	// while another goroutine still references it (see tryPin / searchList).
	cachePool *omnipool.Pool[Cache]
}

// NewPool returns a Pool drawing permits from r.
func NewPool(r Resource) *Pool {
	if r == nil {
		panic("permits: nil Resource")
	}
	p := &Pool{resource: r, cachePool: omnipool.For[Cache]()}
	p.notify.Init()
	return p
}

// ListenersFor returns the Pool's manager-retry listener set. A manager that misses on
// Acquire registers a callback here (via workq's AddToListeners) and returns; the next
// freed permit invokes it to re-run admission. Pool-level (not per-cache) because a free
// anywhere in the Pool can satisfy the miss through inherit/free/steal.
func (p *Pool) ListenersFor() *rdvq.Listeners {
	return &p.notify.Listeners
}

// Waiters returns the Pool's executor waiter set — the blocking park target a top-level
// block-and-help gate waits on while help-draining its wave. A freed permit (Release /
// destroy) wakes one. (AcquireWait uses the same set internally for the mid-body park.)
func (p *Pool) Waiters() *rdvq.Waiters {
	return &p.notify.Waiters
}

// Resource returns the Pool's backing Resource — the accounting object permits are drawn
// from. Callers that constructed the Resource use it to reach Resource-specific controls
// (e.g. a semaphore's dynamic capacity), type-asserting back to the concrete type.
func (p *Pool) Resource() Resource {
	return p.resource
}

// NewCache creates a top-level cache (a tree root drawing on the Pool).
func (p *Pool) NewCache() *Cache {
	c := newCache(p, nil)
	p.roots.pushBack(c)
	return c
}

// Cache is a per-unit node in the Pool's forest. Its (held, inUse) is the lock-free
// atomic counter; its membership in a list (prev/next) and its own children list are
// guarded by mutexes (see cacheList). parent is immutable after construction, so the
// acquire up-walk reads it lock-free.
type Cache struct {
	pool   *Pool
	parent *Cache
	counts counts

	// prev, next are this cache's links in the list that contains it (parent.children,
	// or pool.roots for a root); guarded by THAT list's mutex.
	prev, next *Cache

	// children is this cache's own sub-wave caches.
	children cacheList

	refs  atomic.Int64
	alive atomic.Bool
}

// Reset clears a Cache for recycling through the Pool's omnipool (Resetter). It nils
// the pointer fields so a pooled node holds nothing live; counts is already (0,0) from
// destroy's drain, refs is 0 (destroy ran at refs==0), alive is false, the list links
// are nil (unlink), and children is empty (refs==0 ⟹ all sub-waves drained) — newCache
// re-stamps the live fields.
func (c *Cache) Reset() {
	c.pool = nil
	c.parent = nil
	c.prev = nil
	c.next = nil
}

func newCache(p *Pool, parent *Cache) *Cache {
	c := p.cachePool.Get()
	c.pool = p
	c.parent = parent
	c.refs.Store(1)
	c.alive.Store(true)
	return c
}

// Pool returns the Pool c draws permits from — the boundary that owns the manager
// listener set (ListenersFor) and the executor waiters a body parks on.
func (c *Cache) Pool() *Pool {
	return c.pool
}

// list returns the cacheList that contains c (its parent's children, or the roots).
func (c *Cache) list() *cacheList {
	if c.parent != nil {
		return &c.parent.children
	}
	return &c.pool.roots
}

// NewChild creates a sub-wave's cache drawing on parent. The sub-wave takes a
// reference on parent (caches outlive units), which also pins parent in the ancestor
// chain so this cache's acquire up-walk reads it without a lock.
func (parent *Cache) NewChild() *Cache {
	if !parent.alive.Load() {
		panic("permits: NewChild on a destroyed cache")
	}
	c := newCache(parent.pool, parent)
	parent.refs.Add(1) // the sub-wave draws on parent
	parent.children.pushBack(c)
	return c
}

// touch marks c hot — an acquire up-walk passed it without being satisfied, so its
// subtree is actively demanding through it — by moving it to the back of its sibling
// list, keeping the list coldest-first for the steal. c is an ancestor of the
// acquirer (or the acquirer itself), hence pinned by refcounts and live in its list.
func (c *Cache) touch() {
	c.list().moveToBack(c)
}

// Demand is the caller-held identity of one acquisition demand — the conservation
// token of weighted-acquisition.md Decision 4: a registered demand is satisfied or
// explicitly invalidated, never dropped, and the CALLER holds the identity so a
// postponed manager's retries re-present the SAME demand rather than registering a
// fresh one per retry. The zero value is ready. Registration itself (the w ≥ 2
// demand FIFO and head-of-line barrier) lands with the barrier checkpoint of
// sequencing step 2; until then the identity is carried through Acquire unused.
type Demand struct {
	// gen guards pooled/recycled identities against ABA once the FIFO holds demand
	// references (the captured-generation discipline of rdvq-inbox-reclamation.md):
	// Invalidate bumps it, retiring every outstanding reference to this identity. It
	// also gives Demand nonzero size, keeping distinct *Demand pointers distinct.
	gen atomic.Uint64
}

// Invalidate withdraws the demand — the caller-side edge for a dropped or cancelled
// postpone, wave teardown, or a demand deadline. Idempotent, and safe on a demand
// that was never registered. Invalidating a registered head passes the barrier to
// the next demand in FIFO order (once the barrier lands); a partial hoard stays
// borrowable in its cache — conservation needs no give-back (Decision 1).
func (d *Demand) Invalidate() {
	d.gen.Add(1)
}

// Acquire makes w permits available for a body in c to run and returns a Permit
// recording the backing cache and weight. ok is false if the body must wait. d is
// the caller-held demand identity (see [Demand]). A weight no single source covers
// is assembled by a multi-source gather into c's own held (weighted-acquisition.md
// Decision 1): the partial hoard stays borrowable throughout — a blocked weighted
// acquire is not hold-and-wait — and a miss leaves it cached, step-1 fodder for the
// retry (cache-don't-return is the rollback).
func (c *Cache) Acquire(d *Demand, w int) (Permit, bool) {
	if d == nil {
		panic("permits: Acquire with nil Demand")
	}
	if w < 1 {
		panic("permits: Acquire weight < 1")
	}
	if !c.alive.Load() {
		panic("permits: Acquire on a destroyed cache")
	}
	uw := uint64(w)
	// Steps 1–2: lock-free up-walk; ancestors pinned by refcounts.
	for a := c; a != nil; a = a.parent {
		if a.counts.acquireLocal(uw) {
			return Permit{backing: a, weight: uw}, true
		}
		// Walked past a without being satisfied: a had too little to lend, so it is
		// hot — move it to the back of its sibling list so the steal prefers quiescent
		// caches. A satisfied hit (above) pays nothing: its remaining idle stays a
		// fair victim.
		a.touch()
	}
	// Steps 3–4: free Resource, then steal.
	if backing := c.pool.acquireInto(c, w); backing != nil {
		return Permit{backing: backing, weight: uw}, true
	}
	// Step 5: wait.
	return Permit{}, false
}

// AcquireWait is the blocking acquire — for an executor reacquiring mid-body. It does
// the non-blocking Acquire and, on a miss, parks on the Pool's waiters until a permit
// frees (Release or destroy wakes it), re-searching each time, until it succeeds or
// ctx is cancelled. Cancellation invalidates d (the demand is withdrawn, not
// dropped). The confirm callback re-runs Acquire AFTER registering as a waiter, so a
// permit freed between the miss and the park is taken immediately rather than lost —
// and if it succeeds there, that is the one acquisition (no double-take). (The
// non-blocking Acquire stays the manager's admit path, which postpones on a miss;
// that postpone hook lands with the manager/executor split.)
func (c *Cache) AcquireWait(ctx context.Context, d *Demand, w int) (Permit, error) {
	for {
		if pm, ok := c.Acquire(d, w); ok {
			return pm, nil
		}
		p := c.pool
		var pm Permit
		var ok bool
		_, err := p.notify.Wait(ctx, func() bool {
			pm, ok = c.Acquire(d, w)
			return !ok // park only if still no permit
		})
		if ok {
			return pm, nil
		}
		if err != nil {
			d.Invalidate()
			return Permit{}, err
		}
		// Woken by a freed permit; loop and retry.
	}
}

// acquireInto runs steps 3–4 for c, landing w permits in c's own counts. Returns c
// on success or nil if the pool cannot cover w right now (the caller waits). The
// whole weight in one Resource grant is the fast path; otherwise a multi-source
// gather assembles the weight into c's own held from partial steals (coldest victims
// first) and the Resource's remainder, then occupies atomically once covered
// (weighted-acquisition.md Decision 1). The hoard stays borrowable the entire time,
// and a miss returns with it in place — anyone may take it meanwhile, and the retry
// finds what remains at step 1; cache-don't-return is the rollback. The Resource arm
// is all-or-nothing at the current shortfall until the TryAcquireUpTo capability
// lands (sequencing step 3), so free capacity smaller than the shortfall stays
// unharvested. NOTE: concurrent w ≥ 2 gatherers can contest each other's hoards
// (freelance gathering) until head-only gathering lands with the demand-FIFO
// barrier checkpoint; sequential correctness and conservation are complete here.
func (p *Pool) acquireInto(c *Cache, w int) *Cache {
	//nolint:gosec // G115: w >= 1, validated by Acquire (the only caller)
	uw := uint64(w)
	if p.resource.TryAcquire(w) {
		c.counts.checkout(uw)
		return c
	}
	for {
		if c.counts.acquireLocal(uw) {
			return c // the hoard (plus c's own residue) covers w — occupied
		}
		held, inUse := c.counts.load()
		if held >= inUse+uw {
			continue // raced coverable between the occupy attempt and the load; retry
		}
		need := inUse + uw - held
		//nolint:gosec // G115: need ≤ uw, which came from the int w
		if p.resource.TryAcquire(int(need)) {
			if c.counts.depositOccupy(need, uw) {
				return c
			}
			continue // the hoard shrank since the load; the grant stays as hoard
		}
		v := searchList(&p.roots, 1, c) // any borrowable victim but c itself (ref-pinned)
		if v == nil {
			return nil // step 5: nothing free, nothing borrowable — the hoard stays
		}
		n := v.counts.stealOutUpTo(need)
		v.ReleaseRef() // unpin the candidate (may be the call that destroys it)
		if n > 0 && c.counts.depositOccupy(n, uw) {
			return c
		}
		// A zero take means the candidate's idle permits were consumed (a lock-free
		// acquire, or another steal) between the search and the take; a partial take
		// stays as hoard. Either way, re-assess and re-search.
	}
}

// searchList finds a cache with at least w borrowable to steal from in the forest
// rooted at l, or nil if none is borrowable anywhere. exclude (may be nil) is never
// returned as a victim, though its children remain candidates — a gatherer must not
// steal from itself, since its own borrowable already counts toward its occupy. It
// is a front-to-back DFS returning the FIRST sufficiently-borrowable cache —
// coldest-first by touch order, so the common case both picks the
// least-recently-active victim and terminates early; the victim is left in place, so
// a still-borrowable one stays at the front and is re-picked (order-based camping).
// It holds l's lock while scanning and descends into a child's list while still
// holding l's lock, so the locks nest root→leaf. searchList is the ONLY holder of
// two list locks at once and always in that one order, so no lock-order cycle can
// form. The returned candidate is a hint; acquireInto's stealOutUpTo CAS is the
// authority.
//
// A non-nil candidate is returned **ref-pinned** (`refs++`), taken under the list lock
// where the cache is known linked and alive — so it cannot be destroyed (its memory
// reclaimed) between the search and the caller's stealOut. The caller MUST ReleaseRef
// it. The victim is cross-subtree (off the acquirer's ancestor chain, so not pinned by
// the acquirer's refs); without this pin only GC keeps it alive across the take, which
// is fine for a GC'd cache but not for a pooled one.
func searchList(l *cacheList, w uint64, exclude *Cache) *Cache {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := l.head; c != nil; c = c.next {
		if !c.alive.Load() {
			// Being destroyed: destroy clears alive before its locked remove, which is
			// blocked on this very lock, so a dying cache is still linked here. Skip it
			// (its children are already drained).
			continue
		}
		if c != exclude {
			if h, u := c.counts.load(); h >= u+w {
				if c.tryPin() {
					return c // borrowable victim, pinned across the steal; caller ReleaseRefs
				}
				continue // raced into destroy after the alive check; skip
			}
		}
		if v := searchList(&c.children, w, exclude); v != nil {
			return v // already pinned by the recursive hit
		}
	}
	return nil
}

// Permit is the transient handle for a body occupying w permits from one backing
// cache.
type Permit struct {
	backing *Cache
	weight  uint64
}

// Held reports whether this Permit currently occupies a slot (a non-zero Permit). A
// zero Permit (Held false) is the not-yet-acquired / suspended state — callers use it
// to distinguish a lent-out permit from a held one without reaching into the backing.
func (pm Permit) Held() bool {
	return pm.backing != nil
}

// Release ends the run segment the Permit backed; the permits stay cached in held
// (cache-don't-return), now borrowable — and may satisfy a parked AcquireWait or a
// postponed manager, so it wakes one consumer. Every release wakes (not just a
// borrowable 0→ crossing): a multi-held cache freeing its second idle permit is no
// crossing, yet a second waiter could take it.
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	pm.backing.counts.release(pm.weight)
	pm.backing.pool.wake()
}

// wake routes one freed permit to a single waiting consumer — a postponed manager
// (listeners) first, since it represents in-process admission, else a parked executor
// (waiters). The consumer receives a [rdvq.Notification] whose Forward re-delivers the
// wake to the next consumer (Notifier.Notify's total conservation) rather than
// swallowing it: without that, a stale postpone listener (one whose work already
// re-checked and ran, leaving its idempotent shared controller listener registered)
// would consume the wake and report it delivered, and a genuinely-waiting executor
// would never be notified — a borrowable permit idle forever (the residual ~1/120
// -race TestBySimulation hang). Notify is cheap when both sets are empty, so the
// uncontended release is unaffected.
func (p *Pool) wake() {
	p.notify.Notify(nil)
}

// WakeAll wakes EVERY waiting consumer so each re-runs its acquire. It is for events
// that free MULTIPLE permits at once — a destroy returning held to the Resource, or an
// out-of-band capacity raise (SetMaxConcurrency) — where one wake would under-notify.
func (p *Pool) WakeAll() {
	p.notify.NotifyAll()
}

// tryPin adds a reference only if the cache is still referenced (refs > 0), reporting
// success. It is the steal's safe weak-upgrade: a cache whose last reference already
// dropped is committed to destroy (it may have set alive=false and be blocked on its
// list lock, still linked), so an unconditional refs++ would resurrect it and cause a
// double-destroy. The CAS refuses that. Used under the list lock, where a pin success
// then keeps the cache from being destroyed/recycled until the matching ReleaseRef.
func (c *Cache) tryPin() bool {
	for {
		n := c.refs.Load()
		if n <= 0 {
			return false // committed to destroy — do not resurrect
		}
		if c.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// ReleaseRef drops one reference on c. The last reference (unit exited AND all
// sub-waves drained) destroys it. Returns true if this call destroyed the cache.
func (c *Cache) ReleaseRef() bool {
	n := c.refs.Add(-1)
	if n > 0 {
		return false
	}
	if n < 0 {
		panic("permits: ReleaseRef underflow")
	}
	c.destroy()
	return true
}

// destroy unlinks the cache from its list, returns its held to the Resource, and ends
// its draw on the parent (cascade). It runs only at refs==0 (quiescent). Removal is
// exact under the list lock; counts.drain coordinates the return to the Resource with
// any concurrent stealOut (a steal that took the cache as a candidate before removal),
// so conservation holds without a lock on the counter.
func (c *Cache) destroy() {
	c.alive.Store(false)
	c.list().remove(c)
	if held := c.counts.drain(); held > 0 {
		//nolint:gosec // G115: held is a permit count bounded by the Resource's capacity
		c.pool.resource.Release(int(held))
		// Returning held permits to the Resource frees that much capacity, which can
		// satisfy several postponed managers / parked waiters at step 3 — wake them all.
		c.pool.WakeAll()
	}
	// Recycle c. Safe now and only now: refs==0 (destroy's precondition) with tryPin
	// refusing to resurrect, c is unlinked, inUse==0, and no Permit backs it — so no
	// goroutine still references c. Capture parent/pool first; Put resets c, after which
	// c must not be touched. The parent cascade uses the captured parent, not c.
	parent := c.parent
	pool := c.pool
	pool.cachePool.Put(c)
	if parent != nil {
		parent.ReleaseRef() // the sub-wave's draw on the parent ends
	}
}
