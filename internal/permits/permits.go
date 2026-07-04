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

// OverdraftResource is the optional holdable capability consulted when the Pool has
// dynamically proven a registered demand infeasible at current capacity: barrier
// armed, the head's gather exhausted, TryAcquire refused, zero inUse anywhere (while
// armed, occupies are gated and only releases move the world, so the proof is free
// and exact), and no stranger suspensions (weighted-acquisition.md §Overdraft). It is
// policy only, with no accounting duties — a granted amount lives in the Pool's
// allowance, never in held or checkedOut, so conservation is untouched.
//
//	granted=true           — overdraft granted for n
//	granted=false, err=nil — a promise: normal operation can eventually satisfy n
//	err != nil             — refuse: the unit fails with err (the resource's own
//	                         reason), the demand is invalidated, and the barrier passes
//
// Overdraft runs under the Pool's episode lock and must not call back into the Pool.
// A Resource that does not implement the capability defaults to GRANT: at the
// proven-infeasible point the unit is satisfiable only by overdraft, and a briefly
// exceeded concurrency cap beats a killed unit. Resources whose limits are hard
// safety walls (e.g. memory) implement the capability to refuse.
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

// Pool is the Resource boundary and the root of a forest of caches.
type Pool struct {
	resource Resource
	roots    cacheList

	// notify routes a freed permit to one waiting consumer with renotify conservation.
	// Its embedded Listeners are non-blocking manager postpones (register a callback via
	// Listeners, re-run admission when fired); its embedded Waiters are blocked
	// executors — AcquireWait and the top-level block-and-help gate (which waits on
	// Waiters() while help-draining). On a freed permit, Notify(nil) wakes ONE consumer
	// (listeners before waiters) but hands it a renotify so a consumer that cannot use
	// the wake re-delivers it to the next — the conservation that prevents a stale
	// postpone listener from swallowing a wake a real waiter needed.
	notifier rdvq.Notifier

	// The demand-side head-of-line barrier (weighted-acquisition.md Decision 2): fifo
	// holds the registered w ≥ 2 demands in arrival order — sticky head, FIFO
	// succession, no weight-based ordering — under fifoMu (cold by construction: only
	// w ≥ 2 misses register). barrier mirrors fifo[0], nil iff the FIFO is empty;
	// stored under fifoMu, loaded lock-free as the one-load armed check on every
	// acquire, the exemption anchor (proceed while armed iff the acquiring chain
	// passes through the head's body cache), and the wake router (armed capacity
	// events go to the head's own mailbox — the single consumer that can act).
	// Publish ordering makes the lock-free reads safe: the head's cache and mailbox
	// are written before the barrier Store that publishes it. Transient read races
	// are benign: a stale nil during arming leaks one ordinary acquire (not the
	// systematic step-1 recirculation bypass the barrier exists to close); a stale
	// head in wake() drops the wake into an empty mailbox, which is compensated —
	// the release decremented counts BEFORE the stale load, the successor is
	// promoted AFTER that, and its park-time confirm re-reads counts fresh, so the
	// freed capacity is seen without the wake.
	// PN: I get that this is unnecessary for w = 1 in the general case, but
	// shouldn't w = 1 still be queued if barrier is non-nil?
	fifoMu  sync.Mutex
	fifo    []*Demand
	barrier atomic.Pointer[Demand]

	// overdraftPolicy is the Resource's own Overdraft when it implements
	// [OverdraftResource], else nil — the capability-discovery nil-field test of
	// limiter-resource-classes.md; nil defaults to GRANT (see the interface doc).
	// Resolved once at NewPool; consulted only by evaluations, under fifoMu.
	overdraftPolicy OverdraftResource

	// od is the standing overdraft episode, or nil. Demand-allocated: a Pool
	// carries no episode state (and pays no notifier setup) until a head's
	// overdraft is granted; the object returns to a process-wide pool at episode
	// end. Written under fifoMu — the install swaps the sentinel into fifo[0], so
	// the FIFO lock is the natural guard — and loaded lock-free by wake routing,
	// release's excess return, and claimant parks. Those lock-free reads are safe
	// structurally: every such reader lives inside the episode subtree, whose
	// cache refs pin the anchor, and episode end IS the anchor's destroy — so a
	// live reader implies a standing (un-recycled) episode. Barrier readers that
	// can be stale across an end (wake routing) identify sentinels by the
	// immutable Demand.sentinel flag and re-load od rather than dereferencing
	// through the stale pointer.
	od atomic.Pointer[overdraft]

	// suspended counts permit-holders that have lent their permit back for the
	// duration of a drive episode (SuspendDriver/ResumeDriver), pool-wide; each
	// suspension also counts on the drive-target cache's suspendedDrivers.
	// Maintained whether or not an episode stands (suspension is drive
	// attribution, not episode state). Equality of the pool total with the sum
	// along an evaluator's own chain is the exact no-stranger test (§Overdraft
	// resolution (c)): a suspension targeting a chain cache is a drive the
	// evaluator runs causally inside — its resume follows the evaluator's
	// completion and can never observe the over-commitment — while any other
	// suspension is a stranger whose resume races it.
	suspended atomic.Int64
}

// overdraft is ONE standing episode's state (weighted-acquisition.md §Overdraft),
// pooled and installed on the owning Pool only while the episode stands (see
// Pool.od for the guard and the structural-pin lifetime argument).
type overdraft struct {
	// total is the episode's outstanding aggregate grant D (guarded by the owning
	// Pool's fifoMu; extensions add to it, episode end asserts it home and zeroes).
	total uint64

	// allowance is the remaining un-claimed portion of the grant (the §Overdraft
	// design term) — one side of the episode invariant
	// Σ max(inUse−held, 0) + allowance == total. Claimed by occupyTaking (an
	// exempt occupy that cannot fit under held pushes inUse past it), refilled by
	// release's excess return. Unstealable and uncacheable by structure: steals
	// move held, and the grant is never in held.
	allowance atomic.Uint64

	// sentinel is the standing-head Demand: installed at fifo[0] (and published
	// as the barrier) at grant, so the barrier stays armed — arrivals keep
	// queueing behind it and no successor gathers — until the episode body
	// cache's destroy (refs==0: body exited, all sub-waves drained, every
	// suspension resumed) retires it. sentinel.cache is the head's body cache —
	// the exemption anchor — written under fifoMu before the publishing barrier
	// Store. Its mailbox stays uninitialized: wake routing branches on the
	// sentinel flag before ever touching a mailbox.
	sentinel Demand

	// claimants is where exempt claimants park while the episode stands: the
	// satisfied standing head consumes no mailbox wakes, so armed capacity events
	// route here instead — the actionable consumers are the episode's own
	// subtree. Everyone else keeps their usual targets (registered demands their
	// mailboxes, gated weight-1 the general set, woken when the episode's end
	// disarms or promotes). No exempt claimant can still be parked here at
	// episode end: a parked claimant's cache holds refs that keep the episode
	// cache from destroying.
	claimants rdvq.Notifier
}

// Init implements omnipool.Initer: one-time setup when the pool creates a fresh
// object. The claimants notifier outlives every recycle (rdvq's generation
// discipline), and the sentinel flag is immutable — what lets stale barrier
// readers classify the demand without dereferencing recycled episode state.
func (od *overdraft) Init() {
	od.claimants.Init()
	od.sentinel.sentinel = true
}

// Reset implements omnipool.Resetter: a retired episode carries nothing forward —
// endEpisode already asserted the allowance home and detached the sentinel.
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
	p := &Pool{resource: r}
	if odr, ok := r.(OverdraftResource); ok {
		p.overdraftPolicy = odr
	}
	p.notifier.Init()
	return p
}

// Listeners returns the Pool's manager-retry listener set. A manager that misses on
// Acquire registers a callback here (via workq's AddToListeners) and returns; the next
// freed permit invokes it to re-run admission. Pool-level (not per-cache) because a free
// anywhere in the Pool can satisfy the miss through inherit/free/steal.
//
// Listeners and Waiters are exposed separately — and the Notifier itself is not —
// deliberately: they are the REGISTER/PARK side only. Every notify entry must route
// through the Pool (wake / ChainProbe), which is what steers armed capacity events
// to the head's mailbox or a standing episode's claimants; a raw Notifier would let
// callers inject wakes that bypass that routing.
func (p *Pool) Listeners() *rdvq.Listeners {
	return &p.notifier.Listeners
}

// Waiters returns the Pool's executor waiter set — the blocking park target a top-level
// block-and-help gate waits on while help-draining its wave. A freed permit (Release /
// destroy) wakes one. (AcquireWait uses the same set internally for the mid-body park.)
func (p *Pool) Waiters() *rdvq.Waiters {
	return &p.notifier.Waiters
}

// Resource returns the Pool's backing Resource — the accounting object permits are drawn
// from. Callers that constructed the Resource use it to reach Resource-specific controls
// (e.g. a semaphore's dynamic capacity), type-asserting back to the concrete type.
// PN: the need for such type-assertion is a smell we should dig into.
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

	// suspendedDrivers counts permit-holders currently suspended for a drive
	// episode targeting THIS cache's wave (§Overdraft resolution (c)); each holds a
	// ref on this cache for the suspension's duration, so a nonzero count pins the
	// cache (and, transitively, an episode whose subtree it is in).
	suspendedDrivers atomic.Int64
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
	c := cachePool.Get()
	c.pool = p
	c.parent = parent
	c.refs.Store(1)
	c.alive.Store(true)
	return c
}

// Pool returns the Pool c draws permits from — the boundary that owns the manager
// listener set (Listeners) and the executor waiters a body parks on.
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
// fresh one per retry (re-presentation is idempotent: one FIFO entry). Obtain one
// from [NewDemand] (pooled; return it with [Demand.Free]) or embed one in a pooled
// host whose own Init calls [Demand.Init] — the zero value is NOT ready (Init sets
// up the mailbox once per object; the omnipool Initer convention replaces the old
// lazy mailboxReady flag). A Demand's operations (Acquire retries, Invalidate) are
// externally serialized — one goroutine at a time, like the handle that carries it;
// the Pool's fifoMu covers everything other goroutines observe.
type Demand struct {
	// gen guards pooled/recycled identities against ABA once external references to
	// the identity exist (the captured-generation discipline of
	// rdvq-inbox-reclamation.md): Invalidate bumps it, retiring every outstanding
	// reference.
	gen atomic.Uint64

	// pool is non-nil exactly while the demand is registered in that Pool's FIFO —
	// published atomically so Invalidate can find the registration. The fields
	// below are guarded by that Pool's fifoMu while registered and caller-serialized
	// otherwise.
	pool atomic.Pointer[Pool]

	// cache is the demand's body cache (C_B^L): created lazily at first
	// registration as a child of the registering cache, it is where the head's
	// gather hoards and where every registered acquisition lands (the Permit backs
	// from it). It PERSISTS across satisfied episodes — a resume reacquire hits its
	// hoard as a step-0 own-home occupy — and is destroyed by Invalidate. Atomic
	// because barrier readers reach it lock-free through a possibly-stale head
	// pointer (the exemption anchor) while the owner satisfies or invalidates; a
	// stale VALUE stays benign (a leaked or over-gated acquire re-drives), but the
	// access itself must be coherent.
	cache atomic.Pointer[Cache]

	// mailbox is the registered demand's own wake target: while registered, the
	// demand parks HERE, never on the Pool's general set — an armed capacity event
	// (release/drain/raise) is deliverable only to the head, and promotion only to
	// the successor, so both are single-consumer wakes with a known address. That
	// addressing is what dissolves both the broadcast (herd) and the wake-one
	// conservation hole (a waiter-style Forward is terminal, so a bystander taking
	// the one wake would drop it and strand the head). Initialized once per object
	// by Demand.Init — before any registration can publish the demand, so wake()'s
	// lock-free barrier read always sees a ready mailbox.
	mailbox rdvq.Notifier

	// w is the registered weight (0 when not registered) — stamped at
	// registration; retries must re-present it unchanged (weigh-once).
	w uint64

	// sentinel marks a pool-owned episode sentinel (immutable once set by
	// overdraft.Init): barrier readers that may hold a stale head pointer across
	// an episode end classify it by this flag alone, never dereferencing further
	// into possibly-recycled episode state.
	sentinel bool

	// registered marks live FIFO membership (guarded by pool's fifoMu).
	registered bool
}

// demandPool recycles standalone Demands; gen survives recycling (only a fresh
// object is zero), so references retired by Invalidate stay retired across reuse.
var demandPool = omnipool.For[Demand]()

// NewDemand returns a pooled, initialized Demand. Return it with [Demand.Free] once
// invalidated (or let Free invalidate it).
func NewDemand() *Demand {
	return demandPool.Get()
}

// Init implements [omnipool.Initer]: one-time setup when the pool creates a fresh
// object — the mailbox outlives every recycle (rdvq's generation discipline retires
// stale references; Invalidate bumps gen). A host that embeds a Demand by value
// (e.g. streampool's pooled permit handle) calls this from its own Init instead.
func (d *Demand) Init() {
	d.mailbox.Init()
}

// Reset implements [omnipool.Resetter]: recycling withdraws the identity —
// Invalidate is idempotent, deregisters a live registration, releases the home, and
// bumps gen so any outstanding reference goes stale.
func (d *Demand) Reset() {
	d.Invalidate()
}

// Free returns a pooled Demand for reuse.
func (d *Demand) Free() {
	demandPool.Put(d)
}

// Invalidate withdraws the demand — the caller-side edge for a dropped or cancelled
// postpone, wave teardown, or a demand deadline. Idempotent, and safe on a demand
// that was never registered. Invalidating the registered HEAD passes the barrier to
// the next demand in FIFO order. The demand's body cache is released: its partial
// hoard needs no give-back protocol (Decision 1) — the destroy path drains it to the
// Resource like any cached permits, seeding the wake chain on the freed capacity.
// Must not be called while a Permit backed by the demand's body cache is still held
// (release first — the handle lifecycle already sequences this; destroy's inUse
// panic is the tripwire).
func (d *Demand) Invalidate() {
	d.gen.Add(1)
	if p := d.pool.Load(); p != nil {
		p.deregister(d)
		return
	}
	if c := d.cache.Load(); c != nil {
		d.cache.Store(nil)
		c.ReleaseRef()
	}
}

// Acquire makes w permits available for a body in c to run and returns a Permit
// recording the backing cache and weight. A zero Permit (Held false) with a nil
// error means the body must wait; a non-nil error is a resource-authored overdraft
// refusal — the unit's distinct failure, after which the caller must Invalidate d
// rather than retry. d is the caller-held demand identity (see [Demand]).
//
// The demand-side barrier (weighted-acquisition.md Decision 2) shapes the flow:
// while armed, every acquisition arm — including the step-1/2 up-walk, so local
// recirculation cannot bypass the head invisibly — is gated unless the acquire is
// exempt (its chain passes through the head's body cache, or it is the episode
// owner resuming into its own home). A gated weight-1 acquire simply misses
// (Decision 3: weight-1 never registers); a gated w ≥ 2 acquire registers its
// demand and waits its FIFO turn. A registered demand acquires only through its
// own body cache: as head it gathers into it — multi-source assembly whose
// partial hoard stays borrowable throughout (Decision 1; a blocked weighted
// acquire is not hold-and-wait) — and as a non-head it waits. Unarmed w ≥ 2 gets
// the fast path (single-node up-walk occupy, then a whole-weight Resource grant);
// any miss registers — gathering is head-only, so gather-vs-gather livelock is
// unrepresentable. An uncontended w ≥ 2 still satisfies within one call:
// register → instant head → gather → dequeue → disarm.
//
// Under a STANDING overdraft episode (the barrier held by the episode sentinel),
// an exempt claimant that misses every ordinary arm never registers — queueing
// behind the episode would deadlock its own drain — and instead claims from the
// episode allowance, extending the episode (a further serialized overdraft
// evaluation) when the remaining allowance cannot cover it.
func (c *Cache) Acquire(d *Demand, w int) (Permit, error) {
	if d == nil {
		panic("permits: Acquire with nil Demand")
	}
	if w < 1 {
		panic("permits: Acquire weight < 1")
	}
	if !c.alive.Load() {
		panic("permits: Acquire on a destroyed cache")
	}
	home := d.cache.Load()
	if home != nil && home.parent != c {
		panic("permits: demand homed under a different cache (Invalidate between homes)")
	}
	uw := uint64(w)
	p := c.pool

	// A registered demand waits its turn; only the head acquires (through its own
	// body cache).
	if d.pool.Load() != nil {
		return p.registeredAcquire(d, uw)
	}

	hd := p.barrier.Load()
	var anchor *Cache
	if hd != nil {
		anchor = hd.cache.Load()
		if !exemptFromBarrier(c, home, anchor) {
			if w == 1 {
				return Permit{}, nil // gated; weight-1 never registers
			}
			return p.registerAndAcquire(c, d, uw) // join the FIFO behind the head
		}
	}
	episodeStanding := hd != nil && hd.sentinel

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
	if w == 1 {
		// Steps 3–4: free Resource, then steal (the w=1 "gather" degenerates to a
		// single atomic take-and-occupy).
		if backing := p.acquireInto(c, w); backing != nil {
			return Permit{backing: backing, weight: uw}, nil
		}
		if episodeStanding && anchor != nil {
			return p.claimOrExtend(claimStartFor(c, home, anchor), anchor, uw)
		}
		return Permit{}, nil // step 5: wait
	}
	// w ≥ 2: the whole weight in one Resource grant is the last non-registering
	// arm — it is atomic, so it is not freelance gathering. No single-victim steal
	// here: a partial take would strand loose permits or freelance-deposit them;
	// the head's gather harvests victims instead.
	if p.resource.TryAcquire(w) {
		c.counts.checkout(uw)
		return Permit{backing: c, weight: uw}, nil
	}
	if episodeStanding && anchor != nil {
		return p.claimOrExtend(claimStartFor(c, home, anchor), anchor, uw)
	}
	return p.registerAndAcquire(c, d, uw)
}

// chainPassesThrough reports whether b is on c's ancestor chain (c itself
// included) — the barrier exemption test: an acquire may proceed while armed iff
// its chain passes through the head's body cache. Lock-free: parents are immutable
// and pinned by refcounts. Cost is armed-only.
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
// cache is the home's parent, so the chain test alone would miss it).
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

// registerAndAcquire registers d (weight uw, homed under c) at the back of the
// demand FIFO and, if that made it the head, gathers immediately — so an
// uncontended w ≥ 2 acquire completes in one call. The body cache is created
// lazily on first registration and reused across the demand's episodes; the
// mailbox is initialized (once) BEFORE the barrier publish that makes this demand
// reachable from wake()'s lock-free barrier read.
func (p *Pool) registerAndAcquire(c *Cache, d *Demand, uw uint64) (Permit, error) {
	p.fifoMu.Lock()
	if d.cache.Load() == nil {
		d.cache.Store(c.NewChild())
	}
	d.w = uw
	d.registered = true
	d.pool.Store(p)
	p.fifo = append(p.fifo, d)
	isHead := len(p.fifo) == 1
	if isHead {
		p.barrier.Store(d)
	}
	p.fifoMu.Unlock()
	if !isHead {
		return Permit{}, nil
	}
	return p.headGather(d, uw)
}

// registeredAcquire is a re-presentation of an already-registered demand (a
// postpone retry, a mailbox wake, or a succession wake): the head gathers,
// everyone else keeps waiting.
func (p *Pool) registeredAcquire(d *Demand, uw uint64) (Permit, error) {
	p.fifoMu.Lock()
	if d.w != uw {
		p.fifoMu.Unlock()
		panic("permits: re-presented demand with a different weight (registered weight is stamped)")
	}
	isHead := len(p.fifo) > 0 && p.fifo[0] == d
	p.fifoMu.Unlock()
	if !isHead {
		return Permit{}, nil
	}
	return p.headGather(d, uw)
}

// headGather drives the head demand's assembly into its body cache and, on
// success, retires the demand: dequeue, then promote the successor (one wake to
// its mailbox — the known single party that can now act) or disarm (a chained
// seed to the general set: capacity the barrier held uncontested may now satisfy
// several ordinary waiters, and the chain walks them). An exhausted gather runs
// the overdraft evaluation instead of returning a bare miss.
func (p *Pool) headGather(d *Demand, uw uint64) (Permit, error) {
	home := d.cache.Load() // registered ⇒ non-nil, stable until this call retires it
	//nolint:gosec // G115: uw came from Acquire's int w, validated >= 1
	if p.acquireInto(home, int(uw)) == nil {
		// Gather exhausted: the forest has no borrowable and the Resource refused
		// the shortfall. The hoard stays; a wait outcome re-drives via wakes.
		return p.headOverdraft(d, uw)
	}
	p.fifoMu.Lock()
	if len(p.fifo) == 0 || p.fifo[0] != d {
		p.fifoMu.Unlock()
		panic("permits: satisfied head is not the FIFO front")
	}
	next := p.dequeueFrontLocked()
	d.registered = false
	d.w = 0
	d.pool.Store(nil) // d.cache persists — the demand's home until Invalidate
	p.fifoMu.Unlock()
	p.barrierPassed(next)
	return Permit{backing: home, weight: uw}, nil
}

// headOverdraft runs when the head's gather has exhausted the forest and the
// Resource: the infeasibility proof is evaluated and, on a grant, the standing
// episode is installed — a pooled overdraft object whose sentinel takes the head's
// FIFO slot, so the barrier stays armed (arrivals keep queueing, no successor
// gathers: seriality across the head's park gaps) until the episode body cache's
// destroy retires it. On refusal the demand is dequeued (passing the barrier) and
// the unit fails with the resource's error. A wait outcome is a plain miss: the
// head parks on its mailbox, re-driven by armed capacity events and suspension-end
// nudges. The whole evaluation — forest walk, stranger check, policy call — runs
// under fifoMu, which serializes it against every other evaluation, registration,
// and episode transition.
func (p *Pool) headOverdraft(d *Demand, uw uint64) (Permit, error) {
	home := d.cache.Load()
	p.fifoMu.Lock()
	if len(p.fifo) == 0 || p.fifo[0] != d {
		p.fifoMu.Unlock()
		panic("permits: evaluating head is not the FIFO front")
	}
	held, inUse := home.counts.load()
	ask := excessOver(held, inUse+uw) - excessOver(held, inUse)
	if ask == 0 {
		// The hoard became coverable between the gather's miss and here; a wake is
		// already owed for whatever freed it — miss and let the retry gather.
		p.fifoMu.Unlock()
		return Permit{}, nil
	}
	granted, err := p.evaluateOverdraft(home, ask)
	if err != nil {
		// Refused: dequeue (the FIFO progresses without waiting for the caller's
		// Invalidate) and fail the unit with the resource-authored error.
		next := p.dequeueFrontLocked()
		d.registered = false
		d.w = 0
		d.pool.Store(nil)
		p.fifoMu.Unlock()
		p.barrierPassed(next)
		return Permit{}, err
	}
	if !granted {
		p.fifoMu.Unlock()
		return Permit{}, nil // wait (running work, a stranger, or a promise)
	}
	// Install the standing episode: the pooled od publishes (Store under fifoMu)
	// with its sentinel replacing the satisfied head at fifo[0] and as the barrier
	// (the anchor cache written before the publishing Stores); the demand itself
	// dequeues, its body cache persisting as its home and as the episode anchor.
	od := overdraftPool.Get()
	od.total = ask
	od.allowance.Store(ask)
	od.sentinel.cache.Store(home)
	p.od.Store(od)
	p.fifo[0] = &od.sentinel
	p.barrier.Store(&od.sentinel)
	d.registered = false
	d.w = 0
	d.pool.Store(nil)
	p.fifoMu.Unlock()
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
// the initial grant, for the shortfall only, added to the outstanding aggregate and
// cleared at the original episode's end. A wait outcome returns a miss (the
// claimant parks on episodeNotify; releases, racing extensions, and suspension-end
// nudges re-drive it); a refusal returns the resource-authored error — the unit's
// distinct failure, no wedge: the episode completes without it.
//
// The episode cannot end (nor its pooled state recycle) mid-claim: a live exempt
// claimant's cache chain ref-pins the episode cache, and episode end IS that
// cache's destroy — so the episode outlives every claimant that can reach this
// path. The od identity re-check under fifoMu is a belt for the benign stale-hd
// read in Acquire.
func (p *Pool) claimOrExtend(start, anchor *Cache, w uint64) (Permit, error) {
	od := p.od.Load()
	if od == nil {
		return Permit{}, nil // stale hd: the episode already ended — re-drive fresh
	}
	for {
		c := bestClaimCache(start, anchor, w)
		if c.counts.occupyTaking(w, &od.allowance) {
			return Permit{backing: c, weight: w}, nil
		}
		p.fifoMu.Lock()
		if p.od.Load() != od {
			// Stale-hd belt (a pinned claimant can never see this): the episode
			// ended — miss, and the retry re-runs the gate fresh.
			p.fifoMu.Unlock()
			return Permit{}, nil
		}
		c = bestClaimCache(start, anchor, w)
		if c.counts.occupyTaking(w, &od.allowance) { // recheck under the lock
			p.fifoMu.Unlock()
			return Permit{backing: c, weight: w}, nil
		}
		need := claimNeed(c, w)
		ask := need - od.allowance.Load() // occupyTaking just failed: allowance < need
		granted, err := p.evaluateOverdraft(c, ask)
		if err != nil {
			p.fifoMu.Unlock()
			return Permit{}, err
		}
		if !granted {
			p.fifoMu.Unlock()
			return Permit{}, nil // wait: park on the episode's claimants set
		}
		od.total += ask
		od.allowance.Add(ask)
		p.fifoMu.Unlock()
		// The refilled allowance covers the shortfall unless raced; a racing
		// claimant shrinking it re-runs the evaluation.
	}
}

// evaluateOverdraft is the overdraft evaluation (fifoMu held — one lock serializes
// evaluations against each other and against every registration and episode
// transition): the free-and-exact infeasibility proof — while armed only releases
// move the world, so zero inUse anywhere on top of the caller's already-exhausted
// gather (or refused TryAcquire) dynamically proves nothing inside the system can
// satisfy the shortfall — guarded by the ancestor-exempt trigger: a suspended
// holder off the evaluator's own driver chain is a stranger whose resume races the
// over-commitment, so the evaluator waits for the suspension to end instead
// (ResumeDriver nudges it). Only a passed proof consults the Resource's policy.
// The forest walk nests list locks under fifoMu (nothing takes fifoMu while
// holding a list lock), and the policy call runs under fifoMu — it must not call
// back into the Pool.
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

// anyInUse reports whether any cache in the forest has a running occupier. The
// locking mirrors searchList: each list's lock is held while scanning it, and the
// walk descends holding the parent's lock — root→leaf only, so it composes with
// the steal's ordering discipline.
func (p *Pool) anyInUse() bool {
	return anyInUseList(&p.roots)
}

func anyInUseList(l *cacheList) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := l.head; c != nil; c = c.next {
		if _, u := c.counts.load(); u > 0 {
			return true
		}
		if anyInUseList(&c.children) {
			return true
		}
	}
	return false
}

// strangerSuspended reports whether any suspended permit-holder is off the
// evaluator's own driver chain: exact equality of the pool-wide suspension count
// with the sum along the chain (anchor → root) — resolution (c). Suspensions
// targeting chain caches are drives the evaluator runs causally inside (their
// resume follows its completion); anything else is a stranger whose resume races
// the over-commitment. A transient mismatch (a suspend or resume mid-bracket)
// reads as a stranger — conservative, and self-healing: every armed resume nudges.
func (p *Pool) strangerSuspended(anchor *Cache) bool {
	var sum int64
	for a := anchor; a != nil; a = a.parent {
		sum += a.suspendedDrivers.Load()
	}
	return p.suspended.Load() != sum
}

// endEpisode retires a standing overdraft episode; it runs from the episode body
// cache's destroy — refs==0: the owner's body exited (Invalidate dropped the
// demand's ref), all sub-waves drained, every suspension into the subtree resumed.
// Every claimed excess has therefore returned (excess lives only inside the
// subtree, and a drained subtree has zero inUse), which the allowance-home check
// asserts; then the sentinel dequeues, the barrier passes — promotion of the next
// registered head, or a chained disarm seed to the general set — and the episode
// object returns to its pool (safe: no claimant can still be parked on it, by the
// same structural pin that gates this call).
func (p *Pool) endEpisode(c *Cache) {
	p.fifoMu.Lock()
	od := p.od.Load()
	if od == nil || len(p.fifo) == 0 || p.fifo[0] != &od.sentinel || od.sentinel.cache.Load() != c {
		p.fifoMu.Unlock()
		panic("permits: episode end without the standing sentinel at the FIFO front")
	}
	if got := od.allowance.Load(); got != od.total {
		p.fifoMu.Unlock()
		panic("permits: allowance not fully home at episode end")
	}
	p.od.Store(nil)
	next := p.dequeueFrontLocked()
	p.fifoMu.Unlock()
	p.barrierPassed(next)
	overdraftPool.Put(od)
}

// deregister removes an invalidated demand from the FIFO (promoting the successor
// if it was the head) and releases its body cache; the destroy path drains any
// hoard back to the Resource, itself seeding the chain on the freed capacity.
// No-op if a racing satisfaction already dequeued it.
func (p *Pool) deregister(d *Demand) {
	p.fifoMu.Lock()
	if !d.registered {
		p.fifoMu.Unlock()
		return
	}
	wasHead := p.fifo[0] == d
	var next *Demand
	if wasHead {
		next = p.dequeueFrontLocked()
	} else {
		for i, e := range p.fifo {
			if e == d {
				copy(p.fifo[i:], p.fifo[i+1:])
				p.fifo[len(p.fifo)-1] = nil
				p.fifo = p.fifo[:len(p.fifo)-1]
				break
			}
		}
	}
	d.registered = false
	d.w = 0
	d.pool.Store(nil)
	cache := d.cache.Load()
	d.cache.Store(nil)
	p.fifoMu.Unlock()
	cache.ReleaseRef() // outside fifoMu: destroy takes list locks
	if wasHead {
		p.barrierPassed(next)
	}
}

// dequeueFrontLocked removes fifo[0], re-points the barrier at the successor (nil
// when the FIFO empties — disarm), and returns the successor for the caller to
// wake AFTER releasing fifoMu. fifoMu must be held.
func (p *Pool) dequeueFrontLocked() *Demand {
	copy(p.fifo, p.fifo[1:])
	p.fifo[len(p.fifo)-1] = nil
	p.fifo = p.fifo[:len(p.fifo)-1]
	if len(p.fifo) > 0 {
		next := p.fifo[0]
		p.barrier.Store(next)
		return next
	}
	p.barrier.Store(nil)
	return nil
}

// barrierPassed delivers the head-change wake, outside fifoMu: promotion is one
// wake to the successor's own mailbox (the known single consumer); disarm is a
// chained seed to the general set — whatever capacity the barrier held
// uncontested is a multi-permit event of unknown usable size for the ordinary
// waiters the barrier was gating.
func (p *Pool) barrierPassed(next *Demand) {
	if next != nil {
		next.mailbox.Notify(nil)
		return
	}
	p.notifier.NotifyChained(nil)
}

// AcquireWait is the blocking acquire — for an executor reacquiring mid-body. It does
// the non-blocking Acquire and, on a miss, parks on the Pool's waiters until a permit
// frees (Release or destroy wakes it), re-searching each time, until it succeeds or
// ctx is cancelled. Cancellation invalidates d (the demand is withdrawn, not
// dropped). The confirm callback re-runs Acquire AFTER registering as a waiter, so a
// permit freed between the miss and the park is taken immediately rather than lost —
// and if it succeeds there, that is the one acquisition (no double-take).
//
// Chain discipline: a success on a CHAINED wake (weighted release / multi-permit
// drain — capacity that may satisfy more waiters) owes the chain one fresh probe
// (rule 2); a failed re-acquire after any wake is the chain's terminal probe — the
// capacity is genuinely gone, so the wake simply drops (rule 3; this is the
// pre-existing discard, now load-bearing by design). (The non-blocking Acquire stays
// the manager's admit path, which postpones on a miss; that postpone hook lands with
// the manager/executor split.)
func (c *Cache) AcquireWait(ctx context.Context, d *Demand, w int) (Permit, error) {
	p := c.pool
	var m rdvq.Notification
	for {
		pm, err := c.Acquire(d, w)
		if err != nil {
			d.Invalidate() // an overdraft refusal fails the unit; the demand retires
			return Permit{}, err
		}
		if pm.Held() {
			if m.Chained() {
				p.ChainProbe() // rule 2: pay the chain forward
			}
			return pm, nil
		}
		confirm := func() bool {
			pm, err = c.Acquire(d, w)
			return err == nil && !pm.Held() // park only while there is still nothing
		}
		// Park target follows registration state: a registered demand parks on its
		// OWN mailbox (armed capacity events and its promotion are addressed there;
		// it must not compete for — or worse, consume — general-set wakes it cannot
		// use); an exempt claimant under a standing episode parks on episodeNotify
		// (where armed capacity events route while the satisfied head stands); any
		// other unregistered demand parks on the Pool's general set. The state can
		// change across a park (registration happens inside Acquire; episodes end),
		// so re-evaluate every iteration.
		var waitErr error
		if d.pool.Load() != nil {
			m, waitErr = d.mailbox.Wait(ctx, confirm)
		} else if od := p.standingEpisode(c, d); od != nil {
			m, waitErr = od.claimants.Wait(ctx, confirm)
		} else {
			m, waitErr = p.notifier.Wait(ctx, confirm)
		}
		if pm.Held() {
			if m.Chained() {
				p.ChainProbe() // rule 2, confirm-path success
			}
			return pm, nil
		}
		if err != nil {
			d.Invalidate() // confirm-path overdraft refusal
			return Permit{}, err
		}
		if waitErr != nil {
			d.Invalidate()
			return Permit{}, waitErr
		}
		// Woken by a freed permit; loop and retry (a top-of-loop miss is rule 3).
	}
}

// standingEpisode returns the standing episode an unregistered claimant should
// park on (its claimants set — where armed capacity events route, and only the
// episode's exempt subtree can act on them), or nil to use the general set.
// Structural liveness note: a claimant parked there cannot outlive the episode —
// its cache's refs keep the episode cache from destroying — so the claimants set
// never strands a waiter across an episode end, and the returned object cannot be
// recycled while the claimant exists.
func (p *Pool) standingEpisode(c *Cache, d *Demand) *overdraft {
	hd := p.barrier.Load()
	if hd == nil || !hd.sentinel || !exemptFromBarrier(c, d.cache.Load(), hd.cache.Load()) {
		return nil
	}
	return p.od.Load()
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
// crossing, yet a second waiter could take it. A WEIGHTED release seeds a chained
// wake — its w freed permits may satisfy several waiters, and plain wake-one would
// strand all but the first over borrowable capacity.
func (pm Permit) Release() {
	if pm.backing == nil {
		panic("permits: Release of a zero Permit")
	}
	if excess := pm.backing.counts.release(pm.weight); excess > 0 {
		// Overdraft excess goes home to the allowance BEFORE the wake, so a woken
		// exempt claimant's retry finds it claimable. The episode necessarily
		// stands (excess exists only inside its subtree, whose refs pin the
		// anchor); a nil od here is a bracketing bug.
		od := pm.backing.pool.od.Load()
		if od == nil {
			panic("permits: overdraft excess returned with no standing episode")
		}
		od.allowance.Add(excess)
	}
	pm.backing.pool.wake(pm.weight > 1)
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
//
// chained marks a wake for capacity that may satisfy MORE than one consumer (a
// weighted release, a multi-permit drain): each productive consumer then owes the
// chain one fresh probe (ChainProbe / Notification.ProbeOrigin), so consumers admit
// one by one until the first miss — the serialized wake chain
// (limiter-resource-classes.md Decision 3) in place of the broadcast WakeAll this
// package used to carry, which herded N consumers to satisfy k.
//
// While the barrier is armed, EVERY capacity event routes to the head's own mailbox
// instead — the one consumer that can act (everyone else is gated), so wake-one is
// exact there and the chained bit is moot (the head always re-gathers against
// counts). A wake dropped into a stale head's empty mailbox during a barrier
// transition is compensated by the successor's park-time confirm re-reading counts
// (see the barrier field's comment).
func (p *Pool) wake(chained bool) {
	if hd := p.barrier.Load(); hd != nil {
		if hd.sentinel {
			// Standing episode: the satisfied head consumes no wakes; the
			// actionable consumers are the episode's exempt claimants, and a
			// multi-permit event may satisfy several of them, so the chained bit
			// rides through. od is re-loaded rather than reached through hd (a
			// stale sentinel could point into recycled episode state); a nil od
			// means the episode ended under us — drop, the same compensated class
			// as a stale head's empty mailbox (the release decremented counts
			// first; endEpisode's barrierPassed re-drives the successors).
			od := p.od.Load()
			if od == nil {
				return
			}
			if chained {
				od.claimants.NotifyChained(nil)
				return
			}
			od.claimants.Notify(nil)
			return
		}
		hd.mailbox.Notify(nil)
		return
	}
	if chained {
		p.notifier.NotifyChained(nil)
		return
	}
	p.notifier.Notify(nil)
}

// ChainProbe emits one fresh chained wake. It is both the SEED for a pool-external
// multi-permit capacity event (a SetMaxConcurrency raise announcing unknown headroom)
// and the LINK a productive consumer of a chained wake owes when its wake was
// waiter-style (no origin recorded — the streampool gate loops call this on the Pool
// they already hold; listener-style consumers use Notification.ProbeOrigin instead).
// While the barrier is armed it routes to the head like every capacity event.
func (p *Pool) ChainProbe() {
	p.wake(true)
}

// SuspendDriver records that a permit-holder has lent its permit back for the
// duration of a drive episode targeting this cache's wave (§Overdraft resolution
// (c)). The suspension counters are what let an overdraft evaluation tell an exempt
// ancestor — a suspended holder on the evaluator's own driver chain, whose resume
// is causally after the evaluator's completion — from a stranger whose resume races
// the over-commitment. Call it BEFORE releasing the permit, on the driving
// goroutine, bracketed with [Cache.ResumeDriver]; the cache is ref-pinned for the
// suspension's duration so the counter's home outlives the drive. The cache is
// alive by construction: the driver runs within the target wave's still-open scope.
func (c *Cache) SuspendDriver() {
	c.refs.Add(1)
	c.suspendedDrivers.Add(1)
	c.pool.suspended.Add(1)
}

// ResumeDriver ends a SuspendDriver bracket. Call it BEFORE the reacquire: the
// resuming holder stops being a suspension and becomes a visible (gated, parked)
// demand, which is the design's chosen fairness point — a standing evaluation
// waiting out this suspension may then be granted, rather than waiting for the
// holder's full release (which the barrier gates, and so would wedge). An armed
// pool is nudged so a parked evaluator re-evaluates its stranger check.
func (c *Cache) ResumeDriver() {
	p := c.pool
	p.suspended.Add(-1)
	c.suspendedDrivers.Add(-1)
	c.ReleaseRef() // may destroy c — nothing below touches it
	if p.barrier.Load() != nil {
		p.wake(true)
	}
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
	if c.suspendedDrivers.Load() != 0 {
		// Impossible when brackets are balanced: every suspension ref-pins c.
		panic("permits: destroy with suspended drivers still targeting this cache")
	}
	if od := c.pool.od.Load(); od != nil && od.sentinel.cache.Load() == c {
		// This cache anchored the standing overdraft episode; refs==0 means the
		// owner's body exited and the exempt subtree fully drained, so the episode
		// ends here — BEFORE the drain below, so the drain's wake routes
		// post-episode (to the promoted head's mailbox or the general set).
		c.pool.endEpisode(c)
	}
	c.list().remove(c)
	if held := c.counts.drain(); held > 0 {
		//nolint:gosec // G115: held is a permit count bounded by the Resource's capacity
		c.pool.resource.Release(int(held))
		// Returning held permits to the Resource frees that much capacity, which can
		// satisfy several postponed managers / parked waiters at step 3 — seed the
		// wake chain (chained iff more than one permit returned; rule 2 walks the
		// satisfiable consumers from there).
		c.pool.wake(held > 1)
	}
	// Recycle c. Safe now and only now: refs==0 (destroy's precondition) with tryPin
	// refusing to resurrect, c is unlinked, inUse==0, and no Permit backs it — so no
	// goroutine still references c. Capture parent/pool first; Put resets c, after which
	// c must not be touched. The parent cascade uses the captured parent, not c.
	parent := c.parent
	cachePool.Put(c)
	if parent != nil {
		parent.ReleaseRef() // the sub-wave's draw on the parent ends
	}
}
