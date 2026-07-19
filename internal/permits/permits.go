// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/dll"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/petenewcomb/streampool/internal/trace"
)

// Concurrency model (hybrid: lock-free hot path, one pool mutex for the queue)
//
// The hot acquire path is lock-free; the demand queue is guarded by one Pool
// mutex; the forest structure is guarded by per-list mutexes; wakeups ride the
// Pool's notifier (its own lock-free machinery). The four are independently
// synchronized and meet only through atomics.
//
//   - Per-cache (held, inUse) is the atomic128 counter (counts.go). Acquire steps 1–2
//     (own cache, then the ancestor chain) are a lock-free gated CAS up-walk; ancestors
//     are pinned by refcounts (a live cache refs its parent), so the walk reads parent
//     pointers without a lock. Step 3 is the Resource's own atomic. Release is one CAS.
//   - The Pool's mutex guards the demand FIFO and the anchor install. A demand is
//     registered iff it is linked; registration, retirement, and invalidation are
//     immediate link/unlink operations under the mutex. Lock-free readers see only
//     the published anchor (the barrier gate), a pure cache: a stale read is
//     compensated by register-then-confirm, never load-bearing.
//   - Readiness notification is the Pool's rdvq.Notifier — the pool's notification
//     domain per docs/notification-conservation.md. Capacity events (release, drain,
//     raise, a satisfied demand's re-probe) mint into it; postponed admissions
//     register their queue's listener with it; blockers park in its waiter set. The
//     Pool routes nothing: tokens walk the domain, unproductive wakes forward, and
//     only exhaustion ends one.
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
//     No path holds a cacheList lock while taking the Pool mutex or vice versa.
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

// OverdraftResource is the optional holdable capability consulted when the Pool has
// dynamically proven a registered demand infeasible at current capacity: a head
// standing, its gather exhausted, TryAcquire refused, zero inUse anywhere (while a
// head stands occupies are gated and only releases move the world, so the proof is
// free and exact), and no stranger suspensions (weighted-acquisition.md §Overdraft).
// It is policy only, with no accounting duties — a granted amount lives in the Pool's
// allowance, never in held or checkedOut, so conservation is untouched.
//
//	granted=true           — grant: the unit proceeds over the current limit now
//	                         (the Pool installs a standing overdraft episode for n)
//	granted=false, err=nil — not now: the head keeps waiting and re-asks on the next
//	                         capacity change. NO commitment — a later call for the
//	                         same demand may grant, wait again, or refuse. The
//	                         obligation is the resource's: having said "not now", it
//	                         must ensure a wake eventually re-drives the head (a
//	                         release, a SetMaxConcurrency raise, later a NotifyAt
//	                         timer), or the head wedges.
//	err != nil             — refuse: the head retires (the successor is offered the
//	                         turn) and the caller fails the unit with err (the
//	                         resource's own reason; the caller then invalidates the
//	                         demand)
//
// Overdraft must not call back into the Pool. It is serialized structurally: the
// initial grant runs only on the standing head (headship admits one), and episode
// extensions run under the episode object's own lock. A Resource that does not
// implement the capability defaults to GRANT: at the proven-infeasible point the unit
// is satisfiable only by overdraft, and a briefly exceeded concurrency cap beats a
// killed unit. Resources whose limits are hard safety walls (e.g. memory) implement
// the capability to refuse.
type OverdraftResource interface {
	Resource
	Overdraft(n int) (granted bool, err error)
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

// cachePool recycles Cache nodes across all Pools (caches are fungible — newCache
// re-stamps pool/parent). Safe to recycle on destroy only because the steal
// ref-pins its victim, so a Cache is never reclaimed while another goroutine still
// references it (see tryPin / searchList).
var cachePool = omnipool.For[Cache]()

// cacheDestroyHook, when non-nil, is invoked with each Cache as its Reset recycles it — the
// seam a test uses to observe destruction at the moment it happens (the cache is still the
// destroyed incarnation, not yet reissued from the pool). Production leaves it nil; the
// cost is one relaxed atomic load per destroy, uncontended.
var cacheDestroyHook atomic.Pointer[func(*Cache)]

// Pool is the Resource boundary and the root of a forest of caches.
type Pool struct {
	resource Resource
	roots    cacheList

	// mu guards the demand FIFO and the anchor install. Registration,
	// retirement, and invalidation are immediate link/unlink under it. Nothing
	// under mu blocks, calls user code, mints notifications, or takes a
	// cacheList lock.
	mu sync.Mutex

	// fifo is the always-on demand queue (weighted-acquisition.md "Queue
	// unification") in strict arrival order, every weight: every acquire that
	// cannot be satisfied immediately registers here, and only the front
	// demand (the head) acquires — gathering into its own body cache while
	// freed capacity is held for it. A standing overdraft episode front-links
	// its sentinel here, keeping the barrier armed until the episode ends.
	fifo dll.List[*Demand]

	// anchor publishes the head demand's body cache — the barrier. nil ⇔
	// nobody waits ⇔ the ordinary lock-free machinery, verbatim. Non-nil ⇒
	// every acquisition arm is gated unless the acquirer is exempt (its chain
	// passes through the anchor, or it is the episode owner resuming into its
	// own home). Written under mu (refreshAnchor); read lock-free. It is a
	// pure cache, never load-bearing: a stale nil leaks one ordinary acquire
	// (not a systematic bypass), and a stale non-nil over-gates one acquire
	// into a registration whose confirm re-reads fresh.
	anchor atomic.Pointer[Cache]

	// fallback is the Pool's fallback-mode interest set (docs/notification-
	// conservation.md): queues holding postponed pool-gated work whose demand
	// was withdrawn plant their listener here, and a capacity event with no
	// standing head walks it. Registered demands are never reached through it —
	// head-directed delivery goes through the head's attendant. Reach it
	// through [Pool.Fallback].
	fallback rdvq.Listeners

	// overdraftPolicy is the Resource's own Overdraft when it implements
	// [OverdraftResource], else nil — the capability-discovery nil-field test of
	// limiter-resource-classes.md; nil defaults to GRANT (see the interface doc).
	// Resolved once at NewPool; consulted only by overdraft evaluations (the
	// head's — serialized by headship — and extensions, under od.mu).
	overdraftPolicy OverdraftResource

	// od is the standing overdraft episode, or nil. Demand-allocated: a Pool
	// carries no episode state until a head's overdraft is granted; the object
	// returns to a process-wide pool at episode end. Installed and cleared under
	// mu (episode transitions are mutex-serialized); loaded lock-free by
	// release's excess return and the exempt-claimant arms, whose stale reads
	// are compensated structurally: every such reader lives inside the episode
	// subtree, whose cache refs pin the anchor, and episode end IS the anchor's
	// destroy — so a live reader implies a standing (un-recycled) episode.
	od atomic.Pointer[overdraft]

	// suspended counts permit-holders that have lent their permit back for the
	// duration of a sub-wave drain ([Cache.Suspend]/[Cache.Resume]), pool-wide;
	// each suspension also counts on the drain-target cache. Maintained whether
	// or not an episode stands. Equality of the pool total with the sum along
	// an evaluator's own chain is the exact no-stranger test (§Overdraft
	// resolution (c)): a suspension targeting a chain cache belongs to a drain
	// the evaluator runs causally inside — its resume follows the evaluator's
	// completion and can never observe the over-commitment — while any other
	// suspension is a stranger whose resume races it.
	suspended atomic.Int64

	// rank is a process-global monotonic identity assigned at construction. It is
	// the canonical global acquisition order over Pools: an op that binds several
	// limiters acquires them in ascending rank, and because every op uses the same
	// order, a joint acquirer blocked at one limiter holds only limiters ordered
	// before it — every wait-for edge points strictly up the order, so no cycle can
	// close (weighted-acquisition.md §"Multi-limiter: the FIFO under joint
	// admission"). All limiters are holdable, so creation order — a stable
	// total order — suffices.
	rank uint64
}

// nextPoolRank issues the process-global monotonic Pool ranks.
var nextPoolRank atomic.Uint64

// Rank returns this Pool's position in the canonical global acquisition order.
func (p *Pool) Rank() uint64 { return p.rank }

// Fallback returns the Pool's fallback-mode interest set. A queue postponing a
// pool-gated work without a registration (an unattended miss withdrew the
// demand) plants its listener here — before its final re-attempt, per the
// standard race-closing order — so a capacity event arriving while no head
// stands can wake one of its workers.
func (p *Pool) Fallback() *rdvq.Listeners {
	return &p.fallback
}

// Attendant is a registered demand's wake target: a [rdvq.Waiter] when the
// demand's owner parks (a blocking submit), or the [rdvq.Listener] of the queue
// holding the demand's postponed work (a listen-capable postpone). Head-directed
// delivery fires exactly the head's attendant (docs/notification-conservation.md).
type Attendant interface{ Notify() }

// notifyCapacity delivers a capacity event by mode. With a standing (non-
// sentinel) head, delivery beyond the head's attendant is provably futile under
// arrival-order reservation, so exactly that attendant is fired — one-shot: the
// slot clears on fire and the owner re-arms it each park cycle; a nil slot means
// the owner is running and attempt-on-arrival covers it. With no head, the
// fallback walk fires every planted queue-interest listener. With neither,
// silence: nobody anywhere is waiting, and later claimants see the capacity
// directly (mint-after-visibility). Callers invoke it AFTER making capacity
// visible. Head retirement and head withdrawal call it too — the cascade rule,
// which is how a multi-unit event admits claimants one per delivery.
//
//nolint:contextcheck // background context used only for tracing
func (p *Pool) notifyCapacity() {
	p.mu.Lock()
	head := p.fifo.Front()
	for head != nil && head.sentinel {
		head = p.fifo.Next(head)
	}
	if head != nil {
		a := head.attendant
		head.attendant = nil
		p.mu.Unlock()
		if trace.IsEnabled() {
			trace.Logf(context.Background(), "permits.notifyCapacity", "Pool=%p head=%p attended=%v", p, head, a != nil)
		}
		if a != nil {
			a.Notify()
		}
		return
	}
	p.mu.Unlock()
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.notifyCapacity", "Pool=%p fallback walk", p)
	}
	p.fallback.NotifyAll()
}

// overdraft is ONE standing episode's state (weighted-acquisition.md §Overdraft),
// pooled and installed on the owning Pool only while the episode stands (see
// Pool.od for the guard and the structural-pin lifetime argument).
type overdraft struct {
	// mu serializes episode EXTENSIONS against each other (the initial grant
	// needs no lock: headship makes the gathering head the sole evaluator, and
	// the object is unpublished while it is initialized) and guards total.
	// The user Overdraft call runs under it — it must not call back into the
	// Pool. Episode-cold by definition.
	mu sync.Mutex

	// total is the episode's outstanding aggregate grant D (guarded by mu;
	// extensions add to it, episode end asserts it home and zeroes).
	total uint64

	// allowance is the remaining un-claimed portion of the grant (the §Overdraft
	// design term) — one side of the episode invariant
	// Σ max(inUse−held, 0) + allowance == total. Claimed by occupyTaking (an
	// exempt occupy that cannot fit under held pushes inUse past it), refilled by
	// release's excess return. Unstealable and uncacheable by structure: steals
	// move held, and the grant is never in held.
	allowance atomic.Uint64

	// sentinel is the standing-head Demand: front-linked into the Pool's fifo
	// (and published as the anchor) at grant, so the barrier stays armed —
	// arrivals keep queueing behind it and no successor gathers — until the
	// episode body cache's destroy (refs==0: body exited, all sub-waves
	// drained, every suspension resumed) retires it. sentinel.cache is the
	// head's body cache — the exemption anchor. Exempt claimants under the
	// episode need no tracking: capacity events walk the Pool's notification
	// domain like any other, and a claimant's retry is simply the acquire that
	// succeeds.
	sentinel Demand
}

// Init implements omnipool.Initer: one-time setup when the pool creates a fresh
// object. The sentinel flag is immutable.
func (od *overdraft) Init() {
	od.sentinel.sentinel = true
}

// Reset implements omnipool.Resetter: a retired episode carries nothing forward —
// endEpisode already asserted the allowance home and unlinked the sentinel.
func (od *overdraft) Reset() {
	od.total = 0
	od.allowance.Store(0)
	od.sentinel.cache.Store(nil)
}

// overdraftPool recycles episode state across all Pools.
var overdraftPool = omnipool.For[overdraft]()

// NewPool returns a Pool drawing permits from r.
func NewPool(r Resource) *Pool {
	if r == nil {
		panic("permits: nil Resource")
	}
	p := &Pool{resource: r, rank: nextPoolRank.Add(1)}
	if odr, ok := r.(OverdraftResource); ok {
		p.overdraftPolicy = odr
	}
	p.fallback.Init()
	return p
}

// Resource returns the Pool's backing Resource — the accounting object permits are drawn
// from. Callers that constructed the Resource use it to reach Resource-specific controls
// (e.g. a semaphore's dynamic capacity), type-asserting back to the concrete type.
//
// TODO(weighted-acquisition step 4): retire this round-trip-and-assert in favor of a
// typed handle kept by the constructor — naturally a distinct Semaphore surface
// carrying the method — since callers needing Resource-specific controls had the
// concrete pointer at construction.
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

	// RefCounter is the cache's object-lifetime reference count (embedded, promoting
	// AddRef/TryAddRef/RefCount): a live cache refs its parent, each sub-wave (NewChild)
	// and each suspension ([Cache.Suspend]) adds one, and the last drop recycles the
	// cache through cachePool — running Reset (unlink, return held to
	// the Resource, end any anchored episode, cascade to the parent). newCache's
	// cachePool.Get arms it to 1. a64 (no generation): every holder is strong, and the
	// resurrection race is closed by TryAddRef, whose refs>0 CAS refuses a cache already
	// committed to recycle (a stealer's weak-upgrade under the list lock).
	omnipool.RefCounter

	// suspended counts permit-holders currently suspended into a drain of THIS
	// cache's wave (§Overdraft resolution (c)); each holds a ref on this cache
	// for the suspension's duration, so a nonzero count pins the cache (and,
	// transitively, an episode whose subtree it is in).
	suspended atomic.Int64
}

// Reset is the omnipool recycle hook (Resetter), run by cachePool.Release when the last
// reference drops (refs==0: the unit's body exited AND all sub-waves drained → quiescent).
// It unlinks the cache from its list, returns its held to the
// Resource, ends the standing episode it may anchor, drops its draw on the parent (cascade),
// then nils the pointer fields. It must NOT touch the embedded RefCounter (release() left
// refs at 0; the next cachePool.Get re-arms it). children is empty (refs==0 ⟹ all sub-waves
// drained). Removal is exact under the list lock; counts.drain coordinates the return to the
// Resource with any concurrent stealOut (a steal that took the cache as a candidate before
// removal), so conservation holds without a lock on the counter.
func (c *Cache) Reset() {
	if c.suspended.Load() != 0 {
		// Impossible when brackets are balanced: every suspension ref-pins c.
		panic("permits: recycle with suspensions still targeting this cache")
	}
	if od := c.pool.od.Load(); od != nil && od.sentinel.cache.Load() == c {
		// This cache anchored the standing overdraft episode; refs==0 means the owner's
		// body exited and the exempt subtree fully drained, so the episode ends here —
		// BEFORE the drain below, so the drain's token finds the post-episode barrier.
		c.pool.endEpisode(c)
	}
	c.list().remove(c)
	if held := c.counts.drain(); held > 0 {
		//nolint:gosec // G115: held is a permit count bounded by the Resource's capacity
		c.pool.resource.Release(int(held))
		// Returning held permits to the Resource frees that much capacity: a
		// capacity event. Multi-unit capacity needs no herd — the cascade rule
		// admits claimants one per delivery.
		c.pool.notifyCapacity()
	}
	if h := cacheDestroyHook.Load(); h != nil {
		(*h)(c) // test seam: observe the destroy before c returns to the pool
	}
	// Drop the sub-wave's draw on the parent (cascade). Capture parent before nilling; the
	// cascade's Release may recurse into the parent's own recycle, but touches the captured
	// parent, never c. This runs before omnipool Puts c back to the pool.
	parent := c.parent
	c.pool = nil
	c.parent = nil
	c.prev = nil
	c.next = nil
	if parent != nil {
		parent.ReleaseRef() // the sub-wave's draw on the parent ends
	}
}

func newCache(p *Pool, parent *Cache) *Cache {
	c := cachePool.Get() // omnipool arms the owner reference: refs = 1
	c.pool = p
	c.parent = parent
	return c
}

// Pool returns the Pool c draws permits from.
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
	c := newCache(parent.pool, parent)
	parent.AddRef() // the sub-wave draws on parent; panics if parent is already destroyed
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
// explicitly invalidated, never dropped, and the CALLER holds the identity so
// retries re-present the SAME demand rather than registering a fresh one per retry
// (re-presentation is idempotent: one FIFO entry). Obtain one from [NewDemand]
// (pooled; return it with [Demand.Free]) or embed one in a pooled host — the zero
// value is ready. A Demand's operations (Acquire retries, Invalidate) are
// externally serialized — one goroutine at a time, like the handle that carries
// it; what other goroutines observe is published through the Pool mutex and the
// demand's atomics.
type Demand struct {
	// Links is the demand's membership in the Pool's fifo. Registered ⇔
	// linked. Guarded by the Pool's mu.
	dll.Links[*Demand]

	// gen retires outstanding captured references to this pooled identity
	// across registrations (the captured-generation discipline): it bumps at
	// every registration end. Registration and retirement are immediate
	// link/unlink, so nothing inside the Pool depends on it — it is hygiene
	// for external holders of recycled identities.
	gen atomic.Uint64

	// pool is non-nil exactly while the demand is registered in that Pool —
	// published atomically so Invalidate can find the registration and
	// re-presentations can route.
	pool atomic.Pointer[Pool]

	// cache is the demand's body cache (C_B^L): created lazily at first
	// registration as a child of the registering cache, it is where the head's
	// gather hoards and where every registered acquisition lands (the Permit backs
	// from it). It PERSISTS across satisfied episodes — a resume reacquire hits its
	// hoard as a step-0 own-home occupy — and is destroyed by Invalidate. Atomic
	// because barrier readers reach it lock-free through the published anchor
	// while the owner satisfies or invalidates; a stale VALUE stays benign (a
	// leaked or over-gated acquire re-drives), but the access itself must be
	// coherent.
	cache atomic.Pointer[Cache]

	// w is the registered weight (0 when not registered) — stamped at
	// registration; retries must re-present it unchanged (weigh-once).
	w uint64

	// attendant is the demand's wake target — nil while the owner is running
	// (unattended-but-cycling). A property of the demand, not the registration:
	// it survives invalidate/re-register cycles (a sibling reclaim's lend rule
	// can withdraw a registration mid-park; the confirm's re-acquire re-registers
	// and the standing attendant must still be heard). Written by the owner
	// before its final pre-park re-check, cleared by delivery on fire; a stale
	// stash costs one generation-guarded spurious wake. Published under the
	// registering Pool's mu by enqueue; while unregistered only the owner
	// touches it.
	attendant Attendant

	// sentinel marks a pool-owned episode sentinel (immutable once set by
	// overdraft.Init).
	sentinel bool
}

// demandPool recycles standalone Demands; gen survives recycling (only a fresh
// object is zero), so references retired by Invalidate stay retired across reuse.
var demandPool = omnipool.For[Demand]()

// NewDemand returns a pooled Demand. Return it with [Demand.Free] once
// invalidated (or let Free invalidate it).
func NewDemand() *Demand {
	return demandPool.Get()
}

// Reset implements [omnipool.Resetter]: recycling withdraws the identity —
// Invalidate is idempotent, deregisters a live registration, releases the home, and
// bumps gen so any outstanding reference goes stale.
func (d *Demand) Reset() {
	d.Invalidate()
}

// Free returns a pooled Demand for reuse.
func (d *Demand) Free() {
	demandPool.Release(d)
}

// Invalidate withdraws the demand — the caller-side edge for a dropped or
// cancelled postpone, wave teardown, a demand deadline, or the
// withdraw-before-going-deep discipline (no goroutine may park while a demand
// only it can attend stands registered). Idempotent, and safe on a demand that
// was never registered. The registration is unlinked immediately; if the demand
// was the head, the successor is offered the turn (one minted token). The
// demand's body cache is released: its partial hoard needs no give-back protocol
// (Decision 1) — the destroy path drains it to the Resource like any cached
// permits, and the freed capacity is counts-visible to the next head's gather.
// Must not be called while a Permit backed by the demand's body cache is still
// held (release first — the handle lifecycle already sequences this; destroy's
// inUse panic is the tripwire).
//
//nolint:contextcheck // background context used only for tracing
func (d *Demand) Invalidate() {
	d.gen.Add(1) // retires every outstanding captured reference
	if p := d.pool.Load(); p != nil {
		p.mu.Lock()
		wasHead := p.fifo.Front() == d
		p.fifo.Remove(d)
		d.deregister()
		p.refreshAnchor()
		p.mu.Unlock()
		if trace.IsEnabled() {
			trace.Logf(context.Background(), "permits.invalidate", "Pool=%p Demand=%p wasHead=%v", p, d, wasHead)
		}
		if wasHead {
			// Head withdrawal is a capacity event (the cascade rule): the
			// successor's attendant is woken, or the fallback walk runs if the
			// queue drained empty.
			p.notifyCapacity()
		}
	}
	if c := d.cache.Load(); c != nil {
		d.cache.Store(nil)
		c.ReleaseRef() // outside mu: the drop may recycle c (Reset ends episodes, mints)
	}
}

// deregister clears the registration-scoped fields; the Pool mutex must be held
// and d must already be unlinked.
func (d *Demand) deregister() {
	d.gen.Add(1) // hygiene: every registration end retires captured references
	d.w = 0
	d.pool.Store(nil)
}

// SetAttendant records the demand's wake target for head-directed delivery.
// The owner calls it before the final re-check that precedes parking (the
// standard race-closing order): a capacity event landing between the failed
// attempt and this write is caught by that re-check, and one landing after it
// fires the attendant. Valid on an unregistered demand too — a registration
// created by the subsequent re-check (or by a re-register after a mid-park
// withdrawal) carries the standing attendant with it.
func (d *Demand) SetAttendant(a Attendant) {
	if p := d.pool.Load(); p != nil {
		p.mu.Lock()
		d.attendant = a
		p.mu.Unlock()
		return
	}
	// Unregistered: unreachable by deliverers (not in any fifo), owner-serialized.
	d.attendant = a
}

// Registered reports whether d currently stands in a Pool's demand queue — from its
// registering miss until satisfaction retires it or Invalidate withdraws it. A
// registration is an admission slot (ultimately the pool's FIFO headship), which makes
// it a held resource for deadlock-ordering purposes: a goroutine must not park while a
// demand only it can attend stands registered (the withdraw-before-going-deep
// discipline; see also the streampool reclaim lend rule).
func (d *Demand) Registered() bool {
	return d.pool.Load() != nil
}

// refreshAnchor re-derives the barrier from the front of the fifo and publishes
// the head demand's body cache (nil when the queue is empty). mu must be held.
func (p *Pool) refreshAnchor() {
	var a *Cache
	if hd := p.fifo.Front(); hd != nil {
		a = hd.cache.Load()
	}
	p.anchor.Store(a)
}

// Acquire makes w permits available for a body in c to run and returns a Permit
// recording the backing cache and weight. A zero Permit (Held false) with a nil
// error means the body must wait; a non-nil error is a resource-authored overdraft
// refusal — the unit's distinct failure, after which the caller must Invalidate d
// rather than retry. d is the caller-held demand identity (see [Demand]).
//
// The unified demand queue (weighted-acquisition.md "Queue unification") shapes
// the flow. With the queue EMPTY, this is the ordinary lock-free machinery —
// one anchor load, then the locality-ordered arms. While a head STANDS, every
// acquisition arm — including the step-1/2 up-walk, so local recirculation
// cannot bypass the head invisibly — is gated unless the acquire is exempt (its
// chain passes through the head's body cache, or it is the episode owner
// resuming into its own home), and every gated or missed acquire of EVERY weight
// registers, waiting in strict arrival order. A registered demand acquires only
// through its own body cache: as head it gathers into it — multi-source assembly
// whose partial hoard stays borrowable throughout (Decision 1; a blocked
// weighted acquire is not hold-and-wait) — and as a non-head it waits for its
// turn. An uncontended slow-path acquire still satisfies within one call:
// register → instant head → gather → retire.
//
// A miss leaves the demand registered; the caller retries by re-presenting the
// same demand — from a queue worker's retry sweep (its queue's listener
// its attendant's wake path), a park confirm, or its own next attempt —
// and withdraws it with [Demand.Invalidate] when it stops attending.
//
// Under a STANDING overdraft episode (the barrier anchored by the episode
// sentinel), an exempt claimant that misses every ordinary arm never registers —
// waiting behind its own episode would deadlock its drain — and instead claims
// from the episode allowance, extending the episode (a further serialized
// overdraft evaluation) when the remaining allowance cannot cover it.
func (c *Cache) Acquire(d *Demand, w int) (Permit, error) {
	if d == nil {
		panic("permits: Acquire with nil Demand")
	}
	if w < 1 {
		panic("permits: Acquire weight < 1")
	}
	home := d.cache.Load()
	if home != nil && home.parent != c {
		panic("permits: demand homed under a different cache (Invalidate between homes)")
	}
	uw := uint64(w)
	p := c.pool

	// A registered demand waits its turn; only the head acquires (through its
	// own body cache).
	if reg := d.pool.Load(); reg != nil {
		if reg != p {
			panic("permits: demand registered in a different Pool")
		}
		return p.registeredAcquire(d, uw)
	}

	// The fast-path gate: a BARE LOAD of the anchor, nothing stronger. nil ⇒
	// nobody waits ⇒ the ordinary lock-free machinery below, verbatim.
	anchor := p.anchor.Load()
	if anchor != nil && !exemptFromBarrier(c, home, anchor) {
		return p.enqueue(c, d, uw) // every weight joins the FIFO behind the head
	}

	// Step 0: the demand's persistent body-cache home from a prior satisfied
	// episode — the resume-reacquire hit, and where any leftover hoard lives.
	if home != nil && home.counts.acquireLocal(uw) {
		return Permit{backing: home, weight: uw}, nil
	}

	// Steps 1–2: lock-free up-walk; ancestors pinned by refcounts.
	for a := c; a != nil; a = a.parent {
		if a.counts.acquireLocal(uw) {
			return Permit{backing: a, weight: uw}, nil
		}
		// Walked past a without being satisfied: a had too little to lend, so it is
		// hot — move it to the back of its sibling list so the steal prefers quiescent
		// caches. A satisfied hit (above) pays nothing: its remaining idle stays a
		// fair victim.
		a.touch()
	}
	// Exempt claimant under a STANDING episode: it must NOT gather/steal — the
	// recorded "descendants never gather" rule (allowance fungibility and episode
	// extension substitute). The up-walk above already inherited the parked hoard
	// in place; take any REAL free Resource capacity (checkout adds held and inUse
	// equally — no excess, no leak), else claim from the allowance. A steal here
	// would deposit real held into an excess-carrying cache and silently cover
	// overdraft without refunding the allowance — the Σ excess + allowance == total
	// leak the grant-mode episode model surfaced. (A non-sentinel head's own gather
	// runs through headGather.acquireInto, not this path, so it still steals.)
	if od := p.od.Load(); od != nil {
		if oa := od.sentinel.cache.Load(); oa != nil && exemptFromBarrier(c, home, oa) {
			if p.resource.TryAcquire(w) {
				c.counts.checkout(uw)
				return Permit{backing: c, weight: uw}, nil
			}
			return p.claimOrExtend(claimStartFor(c, home, oa), oa, uw)
		}
	}
	if w == 1 {
		// Steps 3–4: free Resource, then steal (the w=1 "gather" degenerates to a
		// single atomic take-and-occupy).
		if backing := p.acquireInto(c, w); backing != nil {
			return Permit{backing: backing, weight: uw}, nil
		}
		return p.enqueue(c, d, uw) // step 5: wait, in arrival order
	}
	// w ≥ 2: the whole weight in one Resource grant is the last non-registering
	// arm — it is atomic, so it is not freelance gathering. No single-victim steal
	// here: a partial take would strand loose permits or freelance-deposit them;
	// the head's gather harvests victims instead.
	if p.resource.TryAcquire(w) {
		c.counts.checkout(uw)
		return Permit{backing: c, weight: uw}, nil
	}
	return p.enqueue(c, d, uw)
}

// chainPassesThrough reports whether b is on c's ancestor chain (c itself
// included) — the barrier exemption test: an acquire may proceed while armed iff
// its chain passes through the head's body cache. Lock-free: parents are immutable
// and pinned by refcounts. Cost is armed-only, bounded by the acquirer's depth.
func chainPassesThrough(c, b *Cache) bool {
	if b == nil {
		return false
	}
	for a := c; a != nil; a = a.parent {
		if a == b {
			return true
		}
	}
	return false
}

// exemptFromBarrier reports whether an acquire from c whose demand is homed at
// home (nil when unhomed) may proceed while the barrier is armed with the given
// anchor (the head's body cache): its chain passes through the anchor (resolution
// (b) — exactly the causal subtree; siblings, ancestors, and arrivals stay gated),
// or the home IS the anchor (the satisfied episode owner resuming — its acquiring
// cache is the home's parent, so the chain test alone would miss it). Pointer
// comparisons only: a stale anchor value must never be dereferenced.
func exemptFromBarrier(c, home, anchor *Cache) bool {
	return chainPassesThrough(c, anchor) || (home != nil && home == anchor)
}

// claimStartFor picks where an exempt claimant's claim walk begins: the episode
// owner starts (and ends) at its home — the episode anchor itself, since its
// acquiring cache is the anchor's parent, outside the subtree — while every other
// exempt claimant starts at its own cache. bestClaimCache then walks up to the
// anchor, so the claimed excess always lands inside the episode subtree, which is
// what makes "the allowance is necessarily home at episode end" structural (the
// subtree's inUse has fully drained by then).
func claimStartFor(c, home, anchor *Cache) *Cache {
	if home != nil && home == anchor {
		return home
	}
	return c
}

// bestClaimCache walks the claimant's chain from start up to the episode anchor
// (inclusive) and returns the cache whose occupy would draw least from the
// allowance — the ordinary inherit-in-place, allowance-topped, honoring the
// design's "lent capacity plus remaining allowance" before any extension. The
// snapshot reads are racy hints; occupyTaking's CAS is the authority and the
// caller loops. Per-cache all-or-nothing granularity is structural (one Permit,
// one backing) — borrowable fragmented across several chain caches stays
// unharvested, the same limit as the step-2 inherit.
func bestClaimCache(start, anchor *Cache, w uint64) *Cache {
	best := start
	bestNeed := claimNeed(start, w)
	for a := start; a != anchor && a != nil; {
		a = a.parent
		if a == nil {
			break
		}
		if n := claimNeed(a, w); n < bestNeed {
			best, bestNeed = a, n
		}
	}
	return best
}

// claimNeed estimates how much of an occupy of w on c would come from the
// allowance: the excess delta the occupy would create.
func claimNeed(c *Cache, w uint64) uint64 {
	held, inUse := c.counts.load()
	return excessOver(held, inUse+w) - excessOver(held, inUse)
}

// enqueue registers d (weight uw, homed under c) at the back of the demand FIFO.
// The body cache is created lazily on first registration and reused across the
// demand's episodes; the demand's fields are caller-serialized and published by
// the linked insertion under the Pool mutex.
//
// When d lands as the instant head, an inline gather satisfies an uncontended
// acquire in one call — but ONLY for w ≥ 2. The w ≥ 2 fast path does not gather
// (Acquire tried a whole-grant TryAcquire and stopped), so this is its FIRST
// gather. A w = 1 acquire, by contrast, reached enqueue only after its fast-path
// acquireInto (the w=1 steal) JUST failed over the same forest with no
// intervening change, and its body cache is a fresh empty child that adds no
// borrowable — so an inline re-gather would redundantly re-walk to the same
// miss. Skip it: return the miss and let the caller's register-then-confirm
// recheck drive the single as-head gather, which also catches any capacity
// freed during the registration. A gated w = 1 can never be the instant head (a
// head already stands), so this only affects the not-gated instant-head case,
// where the prior fast-path gather is guaranteed to have run.
//
//nolint:contextcheck // background context used only for tracing
func (p *Pool) enqueue(c *Cache, d *Demand, uw uint64) (Permit, error) {
	if d.cache.Load() == nil {
		d.cache.Store(c.NewChild())
	}
	d.w = uw
	d.pool.Store(p)
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.enqueue", "Pool=%p Demand=%p w=%d cache=%p", p, d, uw, c)
	}
	p.mu.Lock()
	p.fifo.PushBack(d)
	isHead := p.fifo.Front() == d
	p.refreshAnchor()
	p.mu.Unlock()
	if isHead && uw >= 2 {
		return p.headGather(d, uw)
	}
	return Permit{}, nil
}

// registeredAcquire is a re-presentation of an already-registered demand (a
// retry from a queue worker's sweep, a park confirm, a capacity wake): the head
// gathers, everyone else keeps waiting.
func (p *Pool) registeredAcquire(d *Demand, uw uint64) (Permit, error) {
	if d.w != uw {
		panic("permits: re-presented demand with a different weight (registered weight is stamped)")
	}
	p.mu.Lock()
	isHead := p.fifo.Front() == d
	p.mu.Unlock()
	if !isHead {
		return Permit{}, nil // a head stands ahead; wait in arrival order
	}
	return p.headGather(d, uw)
}

// headGather drives the head demand's assembly into its body cache and, on
// success, retires the registration. An exhausted gather runs the overdraft
// evaluation instead of returning a bare miss.
//
//nolint:contextcheck // background context used only for tracing
func (p *Pool) headGather(d *Demand, uw uint64) (Permit, error) {
	home := d.cache.Load() // registered ⇒ non-nil, stable until this call retires it
	for {
		//nolint:gosec // G115: uw came from Acquire's int w, validated >= 1
		if p.acquireInto(home, int(uw)) != nil {
			p.retire(d)
			return Permit{backing: home, weight: uw}, nil
		}
		// Gather exhausted THIS pass: the steal walk found nothing and the
		// Resource refused the shortfall. The hoard stays; a wait outcome
		// retries on capacity wakes. Before consulting overdraft policy,
		// re-establish the §Overdraft proof premises AT ONE POINT IN TIME: the
		// gather and the walk below are separate snapshots, and a release
		// landing between them leaves takable capacity the gather never saw —
		// granting then would over-commit past capacity that is right there.
		// anyInUse ⇒ wait (those releases re-drive the head); borrowable
		// anywhere else ⇒ the exhaustion premise broke — re-gather, which takes
		// it (everyone else is gated, so nothing bounces). Only a truly dry
		// forest reaches the policy, which is what makes the evaluation uniform
		// across weights (PN): a weight-1 head gets here only at literally zero
		// capacity, and whether that means "paused, wait for the raise" or
		// "grant past it" is the RESOURCE's policy call, not a weight rule
		// (streampool's semaphore says "not now" while paused, preserving
		// limit-0-blocks; a non-implementing resource grants).
		anyInUse, anyBorrowable := p.walkCounts(home)
		if anyInUse || p.strangerSuspended(home) {
			if trace.IsEnabled() {
				trace.Logf(context.Background(), "permits.headGather",
					"Pool=%p Demand=%p w=%d wait: anyInUse=%v suspended=%d", p, d, uw, anyInUse, p.suspended.Load())
			}
			return Permit{}, nil
		}
		if anyBorrowable {
			continue
		}
		// The Resource's FREE pool is invisible to the walk: a destroy draining
		// capacity back between the gather and the walk leaves the forest dry
		// while TryAcquire would succeed (common under cache churn, not a rare
		// race). Re-establish the refusal premise LAST — take the shortfall if it
		// is there and finish the gather instead of consulting policy.
		held, inUse := home.counts.load()
		need := excessOver(held, inUse+uw) - excessOver(held, inUse)
		if need == 0 {
			continue // raced coverable — the next acquireInto pass occupies
		}
		//nolint:gosec // G115: need ≤ uw, which came from the int w
		if p.resource.TryAcquire(int(need)) {
			if home.counts.depositOccupy(need, uw) {
				p.retire(d)
				return Permit{backing: home, weight: uw}, nil
			}
			continue // hoard shrank underneath; the grant stays as hoard — re-gather
		}
		return p.headOverdraft(d, uw)
	}
}

// retire ends d's registration on satisfaction: immediate unlink, and one token
// delivered so the new head's attendant is offered the turn — the promotion
// cascade rule, by which a multi-unit capacity event admits claimants one per
// delivery until the first miss. The demand's body cache persists as its home
// until Invalidate.
//
//nolint:contextcheck // background context used only for tracing
func (p *Pool) retire(d *Demand) {
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.retire", "Pool=%p Demand=%p", p, d)
	}
	p.mu.Lock()
	p.fifo.Remove(d)
	d.deregister()
	p.refreshAnchor()
	p.mu.Unlock()
	p.notifyCapacity()
}

// headOverdraft runs when the head's gather has exhausted the forest and the
// Resource: the infeasibility proof is evaluated and, on a grant, the standing
// episode is installed — a pooled overdraft object whose sentinel front-links
// into the fifo, so the barrier stays armed (arrivals keep queueing, no
// successor gathers: seriality across the head's park gaps) until the episode
// body cache's destroy retires it. On refusal the demand retires (the queue
// progresses without waiting for the caller's Invalidate) and the unit fails
// with the resource's error. A wait outcome is a plain miss: the head retries
// on capacity wakes. The evaluation needs no lock: headship makes the gathering
// head the sole evaluator (an episode's extensions cannot overlap it — the
// sentinel would hold the headship instead), and the od object is unpublished
// while it is initialized.
func (p *Pool) headOverdraft(d *Demand, uw uint64) (Permit, error) {
	home := d.cache.Load()
	held, inUse := home.counts.load()
	ask := excessOver(held, inUse+uw) - excessOver(held, inUse)
	if ask == 0 {
		// The hoard became coverable between the gather's miss and here; a token is
		// already owed for whatever freed it — miss and let the retry gather.
		return Permit{}, nil
	}
	granted, err := p.evaluateOverdraft(home, ask)
	if err != nil {
		p.retire(d)
		return Permit{}, err
	}
	if !granted {
		return Permit{}, nil // wait (running work, a stranger, or a resource "not now")
	}
	// Install the standing episode: the sentinel front-links (and the anchor
	// re-derives to it) in the same critical section that retires the granted
	// head, so the barrier never opens in between.
	od := overdraftPool.Get()
	od.total = ask
	od.allowance.Store(ask)
	od.sentinel.cache.Store(home)
	p.mu.Lock()
	p.od.Store(od)
	p.fifo.Remove(d)
	d.deregister()
	p.fifo.PushFront(&od.sentinel)
	p.refreshAnchor()
	p.mu.Unlock()
	// Claim the head's own occupation from the fresh allowance. Nothing can shrink
	// the hoard or the allowance in the gap: the barrier is still armed and the
	// episode subtree is empty (the head body has not run yet), so there are no
	// exempt claimants and no gatherers.
	if !home.counts.occupyTaking(uw, &od.allowance) {
		panic("permits: freshly granted allowance did not cover the head's shortfall")
	}
	return Permit{backing: home, weight: uw}, nil
}

// claimOrExtend satisfies an exempt claimant under a standing overdraft episode
// after every ordinary arm has missed: claim the shortfall from the episode
// allowance (occupyTaking on the best chain cache — inUse pushed past held,
// "allowance fungibility"), and when lent capacity plus the remaining allowance
// cannot cover it, run the serialized episode EXTENSION — the same evaluation as
// the initial grant, for the shortfall only, added to the outstanding aggregate
// and cleared at the original episode's end. A wait outcome is a plain miss —
// the claimant's retry rides its queue's listener or its park like any other
// waiter in the Pool's notification domain (no claimant tracking: a token walks
// the domain and the claimant's retry is simply the acquire that succeeds). A
// refusal returns the resource-authored error — the unit's distinct failure, no
// wedge: the episode completes without it.
//
// The episode cannot end (nor its pooled state recycle) mid-claim: a live exempt
// claimant's cache chain ref-pins the episode cache, and episode end IS that
// cache's destroy — so the episode outlives every claimant that can reach this
// path. The od identity re-check under od.mu is a belt for the benign stale
// reads in Acquire.
func (p *Pool) claimOrExtend(start, anchor *Cache, w uint64) (Permit, error) {
	od := p.od.Load()
	if od == nil {
		return Permit{}, nil // stale read: the episode already ended — retry fresh
	}
	for {
		c := bestClaimCache(start, anchor, w)
		if c.counts.occupyTaking(w, &od.allowance) {
			return Permit{backing: c, weight: w}, nil
		}
		od.mu.Lock()
		if p.od.Load() != od {
			// Stale belt (a pinned claimant can never see this): the episode
			// ended — miss, and the retry re-runs the gate fresh.
			od.mu.Unlock()
			return Permit{}, nil
		}
		c = bestClaimCache(start, anchor, w)
		if c.counts.occupyTaking(w, &od.allowance) { // recheck under the lock
			od.mu.Unlock()
			return Permit{backing: c, weight: w}, nil
		}
		need := claimNeed(c, w)
		ask := need - od.allowance.Load() // occupyTaking just failed: allowance < need
		granted, err := p.evaluateOverdraft(c, ask)
		if err != nil {
			od.mu.Unlock()
			return Permit{}, err
		}
		if !granted {
			od.mu.Unlock()
			return Permit{}, nil // wait: capacity events walk the pool's domain
		}
		od.total += ask
		od.allowance.Add(ask)
		od.mu.Unlock()
		// The refilled allowance covers the shortfall unless raced; a racing
		// claimant shrinking it re-runs the evaluation.
	}
}

// evaluateOverdraft is the overdraft evaluation — serialized structurally: the
// initial grant runs only on the head (headship admits one), extensions run
// under od.mu, and the two can never overlap (an episode's sentinel holds the
// headship a gathering head would need). The free-and-exact infeasibility
// proof: while the gate is closed only releases move the world, so zero inUse
// anywhere on top of the caller's already-exhausted gather (or refused
// TryAcquire) dynamically proves nothing inside the system can satisfy the
// shortfall — guarded by the ancestor-exempt trigger: a suspended holder off
// the evaluator's own chain is a stranger whose resume races the
// over-commitment, so the evaluator waits for the suspension to end instead
// ([Cache.Resume] nudges it). Only a passed proof consults the Resource's
// policy, which must not call back into the Pool.
func (p *Pool) evaluateOverdraft(anchor *Cache, ask uint64) (bool, error) {
	if p.anyInUse() {
		return false, nil // something still runs; its releases can move the world
	}
	if p.strangerSuspended(anchor) {
		return false, nil // wait for the stranger's suspension to end
	}
	if p.overdraftPolicy == nil {
		return true, nil // non-implementing holdable: grant
	}
	//nolint:gosec // G115: ask ≤ the demand's weight, which came from an int
	return p.overdraftPolicy.Overdraft(int(ask))
}

// anyInUse reports whether any cache in the forest has a running occupier.
func (p *Pool) anyInUse() bool {
	anyInUse, _ := p.walkCounts(nil)
	return anyInUse
}

// walkCounts sweeps the forest in one pass for the head-proof premises: whether
// any cache has a running occupier (inUse > 0), and whether any cache OTHER THAN
// exclude holds borrowable idle a steal could take (exclude is the head's own
// home, whose borrowable is already counted against its shortfall). The locking
// mirrors searchList: each list's lock is held while scanning it, and the walk
// descends holding the parent's lock — root→leaf only, so it composes with the
// steal's ordering discipline.
func (p *Pool) walkCounts(exclude *Cache) (anyInUse, anyBorrowable bool) {
	return walkCountsList(&p.roots, exclude)
}

func walkCountsList(l *cacheList, exclude *Cache) (anyInUse, anyBorrowable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := l.head; c != nil; c = c.next {
		h, u := c.counts.load()
		if u > 0 {
			anyInUse = true
		}
		if c != exclude && h > u {
			anyBorrowable = true
		}
		if anyInUse && anyBorrowable {
			return
		}
		cu, cb := walkCountsList(&c.children, exclude)
		anyInUse = anyInUse || cu
		anyBorrowable = anyBorrowable || cb
		if anyInUse && anyBorrowable {
			return
		}
	}
	return
}

// strangerSuspended reports whether any suspended permit-holder is off the
// evaluator's own chain: exact equality of the pool-wide suspension count with
// the sum along the chain (anchor → root) — resolution (c). Suspensions
// targeting chain caches belong to drains the evaluator runs causally inside
// (their resume follows its completion); anything else is a stranger whose
// resume races the over-commitment. A transient mismatch (a suspend or resume
// mid-bracket) reads as a stranger — conservative, and self-healing: every
// resume nudges the pool.
func (p *Pool) strangerSuspended(anchor *Cache) bool {
	var sum int64
	for a := anchor; a != nil; a = a.parent {
		sum += a.suspended.Load()
	}
	return p.suspended.Load() != sum
}

// endEpisode retires a standing overdraft episode; it runs from the episode body
// cache's destroy — refs==0: the owner's body exited (Invalidate dropped the
// demand's ref), all sub-waves drained, every suspension into the subtree
// resumed. Every claimed excess has therefore returned (excess lives only inside
// the subtree, and a drained subtree has zero inUse), which the allowance-home
// check asserts. The sentinel unlinks — one token offers the turn to the next
// queued demand, or the barrier disarms — and the episode object returns to its
// pool.
func (p *Pool) endEpisode(c *Cache) {
	od := p.od.Load()
	if od == nil || od.sentinel.cache.Load() != c {
		panic("permits: episode end without a standing episode anchored here")
	}
	od.mu.Lock()
	if got := od.allowance.Load(); got != od.total {
		od.mu.Unlock()
		panic("permits: allowance not fully home at episode end")
	}
	od.mu.Unlock()
	p.mu.Lock()
	p.fifo.Remove(&od.sentinel)
	p.od.Store(nil)
	p.refreshAnchor()
	p.mu.Unlock()
	overdraftPool.Release(od)
	p.notifyCapacity()
}

// acquireInto runs steps 3–4 for c, landing w permits in c's own counts. Returns c
// on success or nil if the pool cannot cover w right now (the caller waits). The
// whole weight in one Resource grant is the fast path; otherwise a multi-source
// gather assembles the weight into c's own held from partial steals (coldest victims
// first) and the Resource's remainder, then occupies atomically once covered
// (weighted-acquisition.md Decision 1). The hoard stays borrowable the entire time,
// and a miss returns with it in place — anyone may take it meanwhile, and the retry
// finds what remains at step 1; cache-don't-return is the rollback. The Resource arm
// is all-or-nothing at the current shortfall, so free capacity smaller than the
// shortfall stays unharvested. NOTE: concurrent w ≥ 2 gatherers can contest each
// other's hoards (freelance gathering); sequential correctness and conservation
// are complete here.
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
// A non-nil candidate is returned **ref-pinned** (`refs++` via tryPin), taken under the list
// lock where the cache is still referenced — so it cannot be destroyed (its memory reclaimed)
// between the search and the caller's stealOut. The caller MUST ReleaseRef it. The victim is
// cross-subtree (off the acquirer's ancestor chain, so not pinned by the acquirer's refs);
// without this pin only GC keeps it alive across the take, which is fine for a GC'd cache but
// not for a pooled one.
//
// A cache whose last reference already dropped is committed to recycle — its Reset's locked
// list remove is blocked on this very lock, so it is still linked here — but tryPin's refs>0
// CAS refuses it (returns nil for it), and its children are already drained (refs==0 ⟹ empty),
// so recursing into them is a no-op. No separate liveness flag is needed.
func searchList(l *cacheList, w uint64, exclude *Cache) *Cache {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := l.head; c != nil; c = c.next {
		if c != exclude {
			if h, u := c.counts.load(); h >= u+w {
				if c.tryPin() {
					return c // borrowable victim, pinned across the steal; caller ReleaseRefs
				}
				continue // last reference already dropped (committed to recycle); skip
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

// Held reports whether this Permit currently occupies a permit (a non-zero Permit).
// A zero Permit (Held false) is the not-yet-acquired / suspended state — callers use
// it to distinguish a lent-out permit from a held one without reaching into the
// backing.
func (pm Permit) Held() bool {
	return pm.backing != nil
}

// Release ends the run segment the Permit backed; the permits stay cached in held
// (cache-don't-return), now borrowable — one token mints into the Pool's
// notification domain (mint after visibility: the counts move first). A weighted
// release needs no herd and no chain: the head its token admits re-probes on
// retirement (conservation rule 5), so satisfiable claimants admit one by one
// until the first miss exhausts.
//
//nolint:contextcheck // background context used only for tracing
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.Release", "Pool=%p backing=%p w=%d", pm.backing.pool, pm.backing, pm.weight)
	}
	if excess := pm.backing.counts.release(pm.weight); excess > 0 {
		// Overdraft excess goes home to the allowance BEFORE the mint, so a woken
		// exempt claimant's retry finds it claimable. The episode necessarily
		// stands (excess exists only inside its subtree, whose refs pin the
		// anchor); a nil od here is a bracketing bug.
		od := pm.backing.pool.od.Load()
		if od == nil {
			panic("permits: overdraft excess returned with no standing episode")
		}
		od.allowance.Add(excess)
	}
	pm.backing.pool.notifyCapacity()
}

// Suspend records that a permit-holder has lent its permit back for the duration
// of a drain targeting this cache's wave (§Overdraft resolution (c)). The
// suspension counters are what let an overdraft evaluation tell an exempt
// ancestor — a suspended holder on the evaluator's own chain, whose resume is
// causally after the evaluator's completion — from a stranger whose resume races
// the over-commitment. Call it BEFORE releasing the permit, on the holder's
// goroutine, bracketed with [Cache.Resume]; the cache is ref-pinned for the
// suspension's duration so the counter's home outlives the drain. The cache is
// alive by construction: the holder runs within the target wave's still-open
// scope.
//
//nolint:contextcheck // background context used only for tracing
func (c *Cache) Suspend() {
	c.AddRef()
	c.suspended.Add(1)
	if n := c.pool.suspended.Add(1); trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.Cache.Suspend", "Pool=%p Cache=%p suspended=%d", c.pool, c, n)
	}
}

// Resume ends a [Cache.Suspend] bracket. Call it BEFORE the reacquire: the
// resuming holder stops being a suspension and becomes a visible (gated,
// waiting) demand, which is the design's chosen fairness point — a standing
// evaluation waiting out this suspension may then be granted, rather than
// waiting for the holder's full release (which the barrier gates, and so would
// wedge). One token mints so a waiting evaluator re-runs its stranger check.
//
//nolint:contextcheck // background context used only for tracing
func (c *Cache) Resume() {
	p := c.pool
	if n := p.suspended.Add(-1); trace.IsEnabled() {
		trace.Logf(context.Background(), "permits.Cache.Resume", "Pool=%p Cache=%p suspended=%d", p, c, n)
	}
	c.suspended.Add(-1)
	c.ReleaseRef() // may destroy c — nothing below touches it
	p.notifyCapacity()
}

// tryPin adds a reference only if the cache is still referenced (refs > 0), reporting
// success. It is the steal's safe weak-upgrade: a cache whose last reference already
// dropped is committed to recycle (its Reset's locked list remove blocked on this lock, so
// it is still linked), so an unconditional refs++ would resurrect it and cause a
// double-recycle. TryAddRef's CAS refuses that. Used under the list lock, where a pin
// success then keeps the cache from being recycled until the matching ReleaseRef.
func (c *Cache) tryPin() bool {
	return c.TryAddRef()
}

// ReleaseRef drops one reference on c, recycling it — through cachePool.Release, which runs
// Reset (unlink, return held to the Resource, cascade) — when this was
// the last reference (unit exited AND all sub-waves drained). The recycle is self-contained
// in Reset (the destruction event, at the right moment), so callers need no return: an
// over-release panics in the counter, and a leak surfaces in the pool's/wave's invariants.
func (c *Cache) ReleaseRef() {
	cachePool.Release(c)
}

// NotifyCapacity delivers an externally minted capacity event — a Resource
// whose capacity grew without any permit release (a concurrency-limit raise).
// Delivery is mode-directed like every capacity event.
func (p *Pool) NotifyCapacity() {
	p.notifyCapacity()
}
