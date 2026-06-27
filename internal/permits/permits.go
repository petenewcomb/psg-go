// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/petenewcomb/streampool/internal/rdvq"
)

// Concurrency model (Phase 2a-ii, lock-free)
//
// There is no lock anywhere. Per-cache (held, inUse) is the atomic128 counter
// (counts.go); the forest structure is built from lock-free nbcq queues and atomic
// refs/alive.
//
//   - Acquire steps 1–2 walk UP the ancestor chain — pinned by refcounts (a live
//     cache holds a reference on its parent), so each hop is a lock-free gated CAS.
//     Step 3 is the Resource's own atomic. Release is one CAS.
//   - Each cache's children, and the Pool's roots, are nbcq.Queue[*Cache] (lock-free
//     MS-queues). NewChild/NewCache PushBack; there is no removal — a destroyed cache
//     is reaped lazily when a steal pops it, and a drained subtree's whole queue is
//     GC'd with its parent.
//   - The steal (step 4) is the exhaustive forest walk that liveness rests on. It
//     cycles each level's queue once, bounded by a per-pass SENTINEL cache it pushes
//     and pops back; it rotates examined caches to the rear (LRU) and recurses into
//     subtrees. The take is a revalidating CAS (a concurrent acquire may consume the
//     idle permit between the walk and the take).
//   - destroy runs only at refs==0 (unit exited AND all sub-waves drained → the cache
//     is quiescent). It CAS-drains held to the Resource (counts.drain), coordinating
//     with a concurrent stealOut so conservation holds without a lock, marks the cache
//     dead (for lazy reaping), and decrements the parent's refs (cascade).

// Resource is the pluggable accounting object permits are drawn from — the open
// extension point (semaphore, memory, rate, weighted); n carries the weight (always
// 1 in this weight-1 cut).
type Resource interface {
	TryAcquire(n int) bool
	Release(n int)
}

// Pool is the Resource boundary and the root of a forest of caches.
type Pool struct {
	resource Resource
	roots    nbcq.Queue[*Cache]

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
	p.roots.Init()
	p.waiters.Init()
	return p
}

// NewCache creates a top-level cache (a tree root drawing on the Pool).
func (p *Pool) NewCache() *Cache {
	c := newCache(p, nil)
	p.roots.PushBack(c)
	return c
}

// Cache is a per-unit node in the Pool's forest. Its (held, inUse) is the lock-free
// atomic counter; its children form a lock-free queue; refs/alive are atomic. parent
// is immutable after construction, so the acquire up-walk reads it lock-free.
type Cache struct {
	pool     *Pool
	parent   *Cache
	counts   counts
	children nbcq.Queue[*Cache]
	refs     atomic.Int64
	alive    atomic.Bool

	// sentinel marks a per-pass terminator pushed by the steal walk (not a real
	// cache); see searchQueue.
	sentinel bool
}

func newCache(p *Pool, parent *Cache) *Cache {
	c := &Cache{pool: p, parent: parent}
	c.children.Init()
	c.refs.Store(1)
	c.alive.Store(true)
	return c
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
	parent.children.PushBack(c)
	return c
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
		v := searchQueue(&p.roots)
		if v == nil {
			return nil // step 5: nothing free, nothing borrowable
		}
		if v.counts.stealOut() {
			c.counts.checkout()
			return c
		}
	}
}

// searchQueue cycles q once, returning a borrowable cache or nil. It is bounded by a
// per-pass sentinel it pushes and pops back; examined caches rotate to the rear (LRU);
// dead caches are dropped (lazy reap); a non-borrowable live cache is recursed into.
// Another concurrent search's sentinel is bounced back unexamined.
func searchQueue(q *nbcq.Queue[*Cache]) *Cache {
	mine := &Cache{sentinel: true}
	q.PushBack(mine)
	for {
		c, ok := q.TryPopFront()
		switch {
		case !ok:
			// Transient: every entry is momentarily held by concurrent searches.
			// Report nothing this pass; the caller's loop / the wake retries.
			return nil
		case c == mine:
			return nil // a full pass with nothing borrowable
		case c.sentinel:
			q.PushBack(c) // another search's terminator — bounce it
			continue
		case !c.alive.Load():
			continue // reap dead lazily
		}
		if h, u := c.counts.load(); h > u {
			q.PushBack(c)
			return c
		}
		if v := searchQueue(&c.children); v != nil {
			q.PushBack(c)
			return v
		}
		q.PushBack(c)
	}
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

// destroy marks the cache dead, returns its held to the Resource, and ends its draw
// on the parent (cascade). It is lock-free: counts.drain coordinates the return with
// any concurrent stealOut; the cache stays in its parent's children queue until a
// steal reaps it (or the parent is destroyed, GCing the whole queue).
func (c *Cache) destroy() {
	c.alive.Store(false)
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
