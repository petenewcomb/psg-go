// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"

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
// extension point (semaphore, memory, rate, weighted); n carries the weight (always
// 1 in this weight-1 cut).
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

	// waiters parks executors blocked in AcquireWait; a permit freed by Release (back
	// to a cache, borrowable) or destroy (back to the Resource) wakes them to
	// re-search. numWaiters gates the wake so the no-contention Release hot path is a
	// single atomic load, not a queue push.
	waiters    rdvq.Waiters
	numWaiters atomic.Int64
}

// NewPool returns a Pool drawing permits from r.
func NewPool(r Resource) *Pool {
	if r == nil {
		panic("permits: nil Resource")
	}
	p := &Pool{resource: r}
	p.waiters.Init()
	return p
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

func newCache(p *Pool, parent *Cache) *Cache {
	c := &Cache{pool: p, parent: parent}
	c.refs.Store(1)
	c.alive.Store(true)
	return c
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

// Acquire makes one permit available for a body in c to run and returns a Permit
// recording the backing cache. ok is false if the body must wait.
func (c *Cache) Acquire() (Permit, bool) {
	if !c.alive.Load() {
		panic("permits: Acquire on a destroyed cache")
	}
	// Steps 1–2: lock-free up-walk; ancestors pinned by refcounts.
	for a := c; a != nil; a = a.parent {
		if a.counts.acquireLocal() {
			return Permit{a}, true
		}
		// Walked past a without being satisfied: a had nothing to lend, so it is hot —
		// move it to the back of its sibling list so the steal prefers quiescent caches.
		// A satisfied hit (above) pays nothing: its remaining idle stays a fair victim.
		a.touch()
	}
	// Steps 3–4: free Resource, then steal.
	if backing := c.pool.acquireInto(c); backing != nil {
		return Permit{backing}, true
	}
	// Step 5: wait.
	return Permit{}, false
}

// AcquireWait is the blocking acquire — for an executor reacquiring mid-body. It does
// the non-blocking Acquire and, on a miss, parks on the Pool's waiters until a permit
// frees (Release or destroy wakes it), re-searching each time, until it succeeds or
// ctx is cancelled. The confirm callback re-runs Acquire AFTER registering as a
// waiter, so a permit freed between the miss and the park is taken immediately rather
// than lost — and if it succeeds there, that is the one acquisition (no double-take).
// (The non-blocking Acquire stays the manager's admit path, which postpones on a
// miss; that postpone hook lands with the manager/executor split.)
func (c *Cache) AcquireWait(ctx context.Context) (Permit, error) {
	for {
		if pm, ok := c.Acquire(); ok {
			return pm, nil
		}
		p := c.pool
		p.numWaiters.Add(1)
		var pm Permit
		var ok bool
		_, err := p.waiters.Wait(ctx, func() bool {
			pm, ok = c.Acquire()
			return !ok // park only if still no permit
		})
		p.numWaiters.Add(-1)
		if ok {
			return pm, nil
		}
		if err != nil {
			return Permit{}, err
		}
		// Woken by a freed permit; loop and retry.
	}
}

// acquireInto runs steps 3–4 for c, landing the permit in c's own held. Returns c on
// success or nil if both miss (the caller waits). The loop re-checks the Resource each
// turn (a concurrent destroy may free capacity) and retries the search when a steal
// candidate's idle permit was consumed before the take.
func (p *Pool) acquireInto(c *Cache) *Cache {
	for {
		if p.resource.TryAcquire(1) {
			c.counts.checkout()
			return c
		}
		v := searchList(&p.roots)
		if v == nil {
			return nil // step 5: nothing free, nothing borrowable
		}
		if v.counts.stealOut() {
			c.counts.checkout()
			return c
		}
		// The candidate's idle permit was consumed (a lock-free acquire, or another
		// steal) between the search and the take. Loop and re-search.
	}
}

// searchList finds a borrowable cache to steal from in the forest rooted at l, or nil
// if none is borrowable anywhere. It is a front-to-back DFS returning the FIRST
// borrowable cache — coldest-first by touch order, so the common case both picks the
// least-recently-active victim and terminates early; the victim is left in place, so a
// still-borrowable one stays at the front and is re-picked (order-based camping). It
// holds l's lock while scanning and descends into a child's list while still holding
// l's lock, so the locks nest root→leaf. searchList is the ONLY holder of two list
// locks at once and always in that one order, so no lock-order cycle can form. The
// returned candidate is a hint; acquireInto's stealOut CAS is the authority.
func searchList(l *cacheList) *Cache {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := l.head; c != nil; c = c.next {
		if h, u := c.counts.load(); h > u {
			return c // borrowable victim, left in place
		}
		if v := searchList(&c.children); v != nil {
			return v
		}
	}
	return nil
}

// Permit is the transient handle for a body occupying one permit.
type Permit struct {
	backing *Cache
}

// Release ends the run segment the Permit backed; the permit stays cached in held
// (cache-don't-return), now borrowable — and may satisfy a parked AcquireWait, so it
// wakes one waiter. Every release wakes (not just a borrowable 0→1 crossing): a
// multi-held cache freeing its second idle permit is no crossing, yet a second waiter
// could take it.
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	pm.backing.counts.release()
	pm.backing.pool.wakeOne()
}

// wakeOne wakes a single parked waiter if any are parked. The numWaiters gate keeps
// the uncontended path (no waiters) to one atomic load.
func (p *Pool) wakeOne() {
	if p.numWaiters.Load() > 0 {
		p.waiters.Notify(nil)
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
		// satisfy several parked waiters at step 3 — wake them all.
		if c.pool.numWaiters.Load() > 0 {
			c.pool.waiters.NotifyAll()
		}
	}
	if c.parent != nil {
		c.parent.ReleaseRef() // the sub-wave's draw on the parent ends
	}
}
