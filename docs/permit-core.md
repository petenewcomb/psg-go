# The Permit Core: A Hierarchical Permit Cache

**Status: the concurrent core — a lock-free hot path over a per-`Cache`-mutex forest:
the locality-ordered acquire, cache-don't-return, the move-to-back LRU steal, and the
non-blocking / blocking acquire modes with rdvq wait/wake — is implemented and
`-race`-validated in `internal/permits`; not yet wired into the live limiter (the
manager/executor split of `dispatch-execution-split.md`).** This document specifies the permit allocation
model at the heart of the limiter — a **hierarchical cache of permits** in which
caches hold capacity locally and let it flow only on demand. It
refines the permit section of `dispatch-execution-split.md` (which framed permits as
per-unit "base holds" with a *last-resort* zero-leaf suspend) and supersedes the
*eager* suspend/reclaim protocol of `limiter-suspend-resume.md`. The headline
results: acquisition is a single locality-ordered primitive that keeps the common
case lock-free and local; idle capacity is *cached, not returned*, and flows by
on-demand pull; the model is **deadlock-free per-limiter with no cycle graph**; and
joint admission of multiple limiters is deadlock-free via a **global acquisition
order** with **no user-facing coordinator** — only the deferred *prioritized*
discipline uses an internal global arbiter, and never for deadlock-freedom.

## Implementation and terminology

The model maps onto four types (`internal/permits`, the source of truth for names):

- **`Resource`** — the pluggable accounting object permits are drawn from (semaphore,
  memory, rate, weighted), and the only thing that knows *capacity*. This document's
  bare **capacity** / **C** refers to a Resource's capacity.
- **`Pool`** — the Resource boundary and the root of a forest of caches. It is the
  only place permits cross in or out of the Resource (check-out and return-on-
  destroy) and it owns the cross-subtree steal. This document's earlier **"L" / "the
  backing store"** is the `Pool` (drawing from a `Resource`); read every "L" below as
  the Pool, and "free L"/"return to L" as checking out of / returning to the Resource
  through the Pool.
- **`Cache`** — a per-unit node in the forest, one per wave/sub-wave, holding `held`/
  `inUse` and cache-don't-return. This document's earlier **"pool"** (the per-unit
  node) is the `Cache`; read every node-"pool" below as a cache.
- **`Permit`** — the transient handle a running body holds for one occupied permit;
  `Release` ends a run segment.

### Concurrency (as implemented)

The core is a **hybrid**: the hot acquire path is lock-free; the forest *structure* is
guarded by per-`Cache` mutexes.

- A `Cache`'s `(held, inUse)` is one 128-bit atomic word (`atomic128`), so each
  transition preserves `0 ≤ inUse ≤ held` as a single CAS; both halves are `uint64`
  *amounts*, so a weighted Resource is representable.
- The acquire up-walk (steps 1–2) is lock-free: ancestors are **pinned by refcounts**
  (a live cache holds a reference on its parent), so the chain is stable without a
  lock.
- Each cache's children, and the Pool's roots, are an **intrusive doubly-linked list
  guarded by a per-list mutex**, kept coldest-first by `touch` (the move-to-back LRU
  telemetry; see "The steal search"). A cache's list links are guarded by the mutex of
  the list that contains it (its parent's children, or the roots).
- The steal walks the forest under those locks (front-to-back, descending root→leaf);
  it is the **only** operation holding two list locks at once, and always in that one
  order, so no lock-order cycle can form. Every other structural op — `touch`, create,
  destroy — takes a single list lock.
- `destroy` runs only at `refs == 0`; it **unlinks the cache exactly** (under its list
  lock, so the steal never sees it — no lazy reaping) and **CAS-drains** `held` back to
  the Resource, coordinating with a concurrent steal's `held−−` so conservation holds
  without a lock on the counter.
- Two acquire modes ride the same search: **non-blocking** `Acquire` (the manager's
  admit; postpones on a miss) and **blocking** `AcquireWait` (the executor's reacquire;
  parks on the Pool's `rdvq` waiters). Every `Release` and the capacity a `destroy`
  returns **wake** parked waiters (gated by a waiter count so the uncontended release
  is a single atomic load).

The lock is **off the common hot path** (steps 1–2 are the lock-free counter up-walk);
it guards only `touch`, the steal, and create/destroy — all colder, contention- or
churn-only, and per-`Cache` so they spread. If a specific list ever shows up as a
tail-latency bottleneck, that list's internal can be swapped for a lock-free structure
without changing `Acquire`/`Release` — a localized, measurement-gated optimization. (A
fully-lock-free `nbcq` forest was tried — `6ba8833` — but it had to drop the move-to-back
LRU, since a shared lock-free queue has no interior reorder, and grew a sentinel-cycle
steal + lazy reaping + a planned `next`-pointer fast lane just to claw the LRU back; the
locked DLL is simpler and restores the LRU directly. This is essentially `e5b20b0`'s
"lock-free hot path, locked steal/destroy.")

## The problem, and why inheritance replaces eager-suspend

A [Limiter] gates how much of an op runs at once: a permit is taken before a body
runs and given back when it completes. The hazard lives *between* those points. A
body can **park** — most importantly, it parks when it drives a sub-wave
synchronously (`CloseAndSkimAll`/`SkimAll`), blocking until that sub-wave drains.
While parked, the body holds its permit but does no work. With a `limit==1`
limiter, a sibling unit that needs the same permit can never get it — the holder
is parked, not releasing — and the system livelocks. This is the pre-existing
intermittent `TestBySimulation -race` hang, orthogonal to any op's logic;
unlimiting everything makes it vanish.

`limiter-suspend-resume.md` dissolved this by *suspending* the permit whenever its
holder parked (the eager protocol: a two-class park rule, a suspend/reclaim
bracket, help-shaped reclaim). That works but churns — every park pays a
suspend and every resume a re-acquire — and it spread a five-state machine across
the body path.

The permit core dissolves the *same* livelock from the other direction. Instead of
the parked holder giving its permit back, **its sub-wave is allowed to use it.**
The holder is parked, not computing; its permit is idle; its own descendant work
may draw on it. Nothing is reallocated and nothing leaves the holder's subtree.
Inheritance-of-the-idle-hold is thus not a perf tweak layered on top — it is the
replacement for eager-suspend, and it is what makes the rest of the model fall
out.

## Permits as a hierarchical cache

The whole architecture is a **hierarchical cache of permits**, structured like a
memory allocator's thread-cache → central free list → page heap. The Pool is the
Resource boundary (capacity `C`); each Cache holds permits checked out from the
Resource; a body needing a permit looks for one starting from the most local cache
and reaching outward only on a miss. Two rules follow, and they organize the rest of
this document:

- **Acquire locality-first.** A body's search order is *its own cache → its ancestor
  chain → free Resource capacity → steal from another subtree → wait*. The near steps
  (own cache, ancestors) are lock-free bumps of a local counter along one goroutine's
  synchronous-nesting chain; only a local miss touches the Resource's contended global
  counter, and only a global miss reaches across subtrees. The common acquisition
  never leaves the local cache.
- **Cache, don't return.** A cache that finishes using a permit does **not** hand it
  back to the Resource — its own subtree is the likely next consumer, and returning
  would just force a contended re-acquire. The permit stays *cached* in the cache
  (idle, hence borrowable), where it is most likely wanted. A permit leaves a cache
  only two ways: the cache is destroyed (refcount → 0, its permits drain back through
  the Pool toward the Resource or a waiter), or another cache **pulls** it under
  genuine demand. Flow is pull-based and lazy — permits move *when actually needed*,
  never proactively.

**This complements minimum-WIP rather than fighting it.** Caching keeps `Σ held`
high — many permits checked out and held idle — which is *not* high WIP. WIP is the
number of *contenders* for permits, and minimum-WIP means minimizing them. A cached
permit lets work already in progress *complete*, by pulling locally instead of
rejoining the global contention; finishing in-progress work is exactly what drains
the contender pool and makes way for more. Eagerly returning permits (the low-`Σ
held`, "fewest permits checked out" reading) does the opposite — it forces
in-progress work to re-contend on the Resource every time it resumes, *adding*
contenders. So the cache is the minimum-WIP mechanism, under WIP's correct
definition.

## Nested caches

Model a unit's permits not as a flat count but as a **cache** its descendants draw
from. Each unit holding permits owns a cache with two numbers:

- **`held`** — the permits this cache currently holds (caches): its *base* (the
  unit's own weight, taken at admission — `1` for a semaphore) plus any *deltas* or
  stolen-in permits. Grows on acquire/steal-in; shrinks *only* when another cache
  pulls a permit away or the cache is destroyed — a body completing lowers `inUse`,
  not `held` (the permit stays cached, see "Permits as a hierarchical cache").
- **`inUse`** — how many of `held` currently back a *running* descendant.

with the per-cache invariant **`inUse ≤ held`** always. A unit's sub-wave siblings
**contend for that unit's cache** exactly as if it were a semaphore of size
`held`: up to `held` of them run concurrently, each occupying one permit;
the rest wait. Because the parent is parked while its sub-wave runs, this
double-uses nothing — the parent is not computing, so its permits are free for
its children.

The structure is recursive. When a child body starts it occupies one permit from
its parent's cache (`inUse++`); that occupied permit seeds the child's own cache for
*its* children, should it in turn park to drive a sub-wave (and the child grows
that cache with deltas of its own if its children need more). A running body
therefore occupies exactly one permit and subdivides nothing; caches with depth
exist only under *parked* units.

**`borrowable = held − inUse`** is the idle subset of a cache — permits the holder
acquired but that back no running descendant right now, available to be *stolen* by
another cache (taken outright, with no return obligation; see "Acquisition"). It is
nonzero only while the holder is parked driving a sub-wave (a running holder has
`inUse == held` for its cache-of-one). This is the finer-grained generalization of
the earlier "zero-leaf" idea: we do not require the *whole* cache to be idle to
reclaim from it; we reclaim the idle *subset*, even while some siblings still run.

## Ownership and lifetime: caches outlive units

A unit's permits do not belong to its *execution*; they belong to a **cache** that
the unit and its sub-waves jointly own — because a sub-wave can outlive the unit
that created it (a body may spawn a sub-wave and exit without draining it; the
sub-wave is drained elsewhere). If permits released when the unit's body
completed, that sub-wave's still-running units would draw on capacity already
returned to the Resource, overcommitting it.

- **The cache is the durable owner of its permit set, reference-counted over the
  unit plus every live sub-wave drawing on it.** It returns `held` to the Resource
  only when the last holder is gone — the unit has exited *and* all its sub-waves
  have drained — never at unit completion alone.
- **A unit's permits are modelled as a cache uniformly, from admission.** Holding a
  bare permit and promoting to a cache on the first sub-wave has a hazard when a
  unit spawns *several*: the second sub-wave would find the permits already
  captured by the first. A single stable cache, created when the unit acquires its
  base, with each sub-wave attaching as a child, removes the hand-over. Only the
  child-cache objects are lazy; the parent cache is the unit's own permit set.
- **Every cache is the same object; the structure is uniform and recursive.** A
  cache has `held` (permits it owns), `inUse`, and a parent it draws from. A unit's
  cache starts with the unit's base weight; a sub-wave's cache starts owning nothing
  and draws on its parent. The **base is genuinely pooled** — all of a unit's
  sub-waves contend for the unit's cache — and that is the *only* free sharing. A
  **delta is held by the sub-wave that drove it**: its units use it directly, and
  a sibling sub-wave shares it only by driving its own delta that steals the
  first's idle capacity. So everything beyond a sub-wave drawing on its parent's
  base — including sharing between sibling sub-waves of one unit — goes through the
  ordinary steal path. Two extremes were rejected: *promoting every delta to one
  per-unit cache* (a sub-wave that exits could not release its own deltas without
  waiting on its siblings), and *partitioning even the base per sub-wave* (forcing
  a steal for every cross-sub-wave share, needless churn).

The cache hierarchy mirrors the unit→sub-wave nesting, with lifetime
`max(owning unit, its sub-waves)` by refcount. This is invisible to the invariants
and the deadlock-freedom result, which are already cache-centric: a permit is
*released* to the Resource when its cache's refcount reaches zero, but becomes
*borrowable* — the thing liveness depends on — as soon as its drawer stops running.

## Acquisition: one locality-ordered primitive

There is a single acquire operation, and *base*, *delta*, and *inheritance* are
three names for where its result came from, not three mechanisms. A cache that needs
a permit always acquires into its **own** `held`, searching outward in locality
order:

1. **Own cache** — a permit this cache already holds and isn't using
   (`held > inUse`). A lock-free local hit; nothing moves.
2. **Ancestor chain** — an idle (cached, borrowable) permit in a parked ancestor.
   This is *inheritance*: the parent is parked precisely so its children may use
   its permits, and will not want them back until its sub-wave drains (by which
   point the child is done), so the draw is cost-free. Still local to one
   goroutine's synchronous-nesting chain.
3. **Free Resource capacity** — if the Resource is globally below its limit, check
   out a fresh permit. The first step that touches the Resource's contended counter,
   and the first that raises `Σ held`.
4. **Steal from elsewhere in the forest** — take an idle permit cached anywhere
   else: victim `held−−`, this cache `held++`. It is a *steal*, not a loan —
   no return obligation, no lender/borrower bookkeeping; the victim simply loses the
   permit and reacquires it from scratch through this same search if it needs it
   again. Finding one is a **search of the forest from the root**, not a counter
   check — idle is leaf-determined and may sit cached deep in a subtree; it descends
   *including the requester's own top-level subtree*, which is how near cousins (off
   the ancestor path, missed by steps 1–2) are covered by the same algorithm. How it
   is guided to a victim, cheaply, is "The steal search" below. The genuine last
   resort: cross-subtree, the only step with a search and with contention cost, and
   rare by construction (it fires only when own cache, ancestors, and free Resource
   all miss — saturation *and* cross-subtree contention).
5. **Wait** — nothing free or borrowable anywhere; park on the Resource's
   availability and re-search when a permit frees. A caller that must not block takes
   steps 1–4 only and, on a miss, *postpones* (holds the work un-admitted, retried on
   the same wake) instead of waiting — that is how a manager admits without blocking,
   while a mid-body reacquire does take this step. See `dispatch-execution-split.md`,
   "Permits: managed off the executor."

**Inheritance is nearest-first; stealing is root-first.** Reaching step 4 means
steps 1–3 all missed — every ancestor up to the root, *and* free Resource. So a body
never reaches into a foreign subtree until it has exhausted its own subtree's entire
cache and the Resource: the cheap common draw (step 2) stops at the nearest idle
ancestor, while the rare steal (step 4) is always anchored at the root and searches
the forest from there. That anchoring keeps stealing maximally deferred and the
cross-subtree coordination single-point, and is why stealing stays rare enough to
leave cycle detection deferred.

A **delta** is simply an acquisition that missed steps 1–2 and had to reach step 3
or 4 — a sub-wave needing more than its ancestors cache, taking the extra into its
own cache. It is held by the sub-wave that drove it and released independently of any
sibling (a sibling shares it only by its own acquisition stealing this cache's idle
capacity at step 4). So "the base is pooled; deltas are per-driver; everything else
steals" is just this one primitive seen from different sources — and the ancestor
draw (step 2) is a near cache hit, *not* the last-resort steal (step 4), which is
the distinction the "stealing is rare" claim turns on.

**Flow is pull-based; there is no proactive return.** When a body finishes, its
permit stays cached in its cache — step-1 fodder for that cache's next acquisition,
and borrowable by others meanwhile. A cache that had a permit stolen at step 4 does
*not* get it handed back; if its own demand rises again it re-acquires through the
same search, stealing the permit back only if and when it actually needs it.
Permits return to the Resource only when a cache is destroyed (refcount → 0).
This is "cache, don't return" made concrete: idle capacity sits where it last
landed until pulled. The only cost is that a cache stolen from, if it wants the
permit back, must wait for or re-steal it from the current holder — which the
deadlock-freedom result below shows is always safe.

## The steal search: routing idle, then choosing a victim

Step 4 must *find* idle capacity, and idle is a **leaf-determined fact**: a permit
backs a running body only at a leaf — the cache of the sub-wave the body runs in —
and under cache-don't-return a finished body's permit stays cached at that leaf. So
idle accumulates at the leaves where work ran, and an interior cache has no local
knowledge of which of its descendants hold it. A *truly* blind descent would average
a half-forest walk, precisely under the saturation where steals fire. So the search
needs guidance — but the guidance that is *free* is exactly the walk telemetry (the
LRU order `touch` maintains), while an exact idle index has to earn its keep.

Correctness sits under both: **liveness rests on neither.** It comes from
wake-on-free plus an exhaustive fallback — if the guided front-to-back walk finds
nothing near the front, it still covers the whole forest, and if it finds nothing it
waits and re-searches when the next freed permit wakes it. The take is always a CAS at
the leaf, so any guidance is a *hint*: at worst it costs a deeper walk or a re-search,
never a wrong grant and never a permanent miss.

**The implemented telemetry — move-to-back LRU.** Each cache's children, and the
Pool's roots, are an intrusive doubly-linked list under a per-list mutex, kept
**coldest-first**: an acquire up-walk that passes a cache *without being satisfied*
(`held ≤ inUse` — nothing to lend, so its subtree is actively demanding through it)
moves it to the **back** of its sibling list — an O(1) interior relink under that one
lock. A *satisfied* hit pays nothing (it had spare, so its remaining idle stays a fair
victim). The list order *is* the steal telemetry, so the steal is a plain front-to-back
DFS taking the **first borrowable** cache — already the least-recently-active victim,
with early-out — and it leaves the victim in place, so a still-borrowable one stays at
the front and is re-picked (**order-based camping**: steals concentrate on one stable
idle source instead of scattering, which would cause re-steal churn). The take is a
**revalidating `stealOut` CAS** (a concurrent acquire may have consumed the idle permit
since the walk saw it). The exhaustive full-forest walk is paid only under saturation
(nothing borrowable near the front), the case where the acquirer must wait regardless;
with wake-on-free (a parked `AcquireWait` re-searches on every freed permit), no
borrowable is permanently missed.

This restores the sequential sketch's **move-to-back** directly — an in-place relink
under the per-list lock — which the fully-lock-free `nbcq` port had to drop (a shared
lock-free queue has no interior reorder, forcing that port into a sentinel-cycle steal
plus a per-cache `next`-pointer fast lane to *approximate* the LRU). The locked DLL
needs neither. A real clock is reserved only for a possible duration-*threshold* policy,
never for this ordering.

**Deferred — an exact idle index.** Whether to add bookkeeping that prunes the deeper
walks is an open, measurement-gated question — and the obvious candidate, an exact "is
there borrowable below me" bit per cache, is *not* an easy win, on either of its two
possible uses:

- **As a filter** (skip a subtree whose bit reads empty) the bit's maintenance must
  be **exact**. A single missed or reordered update that leaves a subtree marked
  empty while it holds idle makes the search *skip real idle* — and because filtering
  removes the exhaustive fallback for that branch, that is a correctness/liveness
  bug, not a backtrack. This outlaws the cheap relaxed, short-circuited propagation
  ("stop when the ancestor's bit doesn't flip" is exactly where a race strands a
  subtree) and forces full synchronized propagation on every leaf `borrowable`
  0-crossing, acquire *and* release.
- **As a pure prioritizer** (lax, with the fallback intact) one bit cannot rank
  candidates, so to be worth its propagation it would have to carry more than a bit
  — a magnitude or recency — i.e. still more cost.

Given how rare steals are by construction, it is genuinely unclear this ever pays
for itself. So the design commits only to the move-to-back telemetry plus the
exhaustive fallback, and leaves the door open to *some* additional bookkeeping — the
subtree-idle aggregate being one candidate, under the constraints above — to be
introduced only if measurement shows the search too lossy.

## Driving is an alternation: lend while waiting, reacquire to compute

"Driving" a sub-wave — calling submit, or a skim/`CloseAndSkimAll` on it — is not
a single monolithic park. The driving body alternates between two phases, and its
base allocation follows which phase it is in:

- **Blocked-waiting** — parked on the sub-wave's queue with no result to process,
  or stalled in a submit's governor/permit gate. The body is not computing, so its
  base is idle, lent to the sub-wave's units exactly as the inheritance model
  describes.
- **Computing** — the instant the body resumes to run a **skim handler** on a
  drained result, or to **return from the drive call** back into its own body, it
  is computing again and must hold its full base. So it **reacquires its base
  before invoking each skim handler and before returning** from the drive.

Reacquisition is not special machinery — it is the ordinary locality-ordered
acquire (own cache → ancestors → free Resource → steal → wait), and in the common
case it is a step-1 hit on the body's own freshly-cached permits. The eager model's
bespoke help-shaped reclaim collapses into "a body that wants to compute needs its
permits; get them the normal way." Reacquire can block (waiting for a sub-unit to
release), can reach the free Resource or steal to compute now, and by taking its
permits back it legitimately denies them to others — that is the limiter doing its
job. The "at most N computing" bound is preserved precisely because a computing
body — its own code *including* a skim-handler invocation — always holds its full
base, while only a purely blocked-waiting body lends it.

A body drives **at most one sub-wave at a time** (it is one goroutine, parked on
one queue), but driving can **nest**: a body driving wave A may, inside a handler,
drive a further wave B (`CloseAndSkimAll(B)`). The base then follows the computation
locus *down* the nesting — held while the body computes, lent to B's units while it
waits on B, reacquired to run B's handler — and unwinds back out as each drive
completes. Interleaved or nested driving is just the alternation applied at each
level; no level holds the base while a deeper level needs to lend it. (A *skim*
handler may nest a drive too, but it is limiter-free, so it holds no base to lend —
B's units simply acquire their own permits; the only restriction is that B not be a
wave the handler is part of — its own wave or an ancestor.)

The deadlock-freedom argument below already accommodates this: a skim-handler
invocation is itself a running computation that eventually completes (returns to
the wait, freeing its base) or parks (a nested drive, lending it). The isolated
sketch must exercise these reacquire-before-handler and reacquire-before-return
points, and the nested-drive scenario, in its model-checked state space — they are
where the base's lend/reacquire churn and any reacquire contention actually live.

## Deadlock-freedom, with no cycle graph

Holding permits through parks creates an obvious worry: a delta can need a permit
that is, right now, a base-hold of another parked unit in a different subtree, and
those holds interlock into a cycle. `dispatch-execution-split.md` met this with a
cycle detector and a last-resort breaker. **Partial-idle stealing removes the
need for either**, by the following argument.

Consider an acquire that misses every cache, finds no free Resource capacity, and
finds nothing to steal — steps 1–4 all fail. That can only mean *every* permit the
Pool holds has `inUse > 0`: every permit is backing a running body. Running bodies
make progress; each eventually either completes or **parks to drive a sub-wave**, and
*both* transitions drop its `inUse`, making that permit borrowable (it stays cached,
and reaches the Resource only once its cache's refcount hits zero). So a request that
cannot be satisfied now is always behind work that is actively running and will
yield. Equivalently, by contraposition: a genuine cyclic deadlock requires every unit
in the cycle to be parked (a unit blocked on an acquire is, by construction, the
child of a parked parent); all-parked means every cycle member's cache is fully idle;
fully idle means fully borrowable — so the steal step succeeds and there was no
deadlock.

The load-bearing fact is **per-limiter and local**: *a parked holder's permit is
idle, hence borrowable.* It needs no view across limiters, which is why a
cross-limiter cycle (B holds an L1 permit and parks needing L2; C holds an L2
permit and parks needing L1) dissolves without any unified scheduler: when B
parks its L1 permit goes idle and borrowable; when C parks its L2 permit goes idle
and borrowable; each side's steal decision is purely local to its own limiter,
and both deltas clear. No goroutine ever has to see the whole cycle.

So the baseline ships with **no wait-for graph, no cycle walk, and no detector**,
and is provably live. Precise cycle detection survives only as a *churn-reduction*
optimization — choosing *which* idle permit to steal to minimize re-acquisition —
worth building only if a measured steal/re-acquire rate justifies it, and under
the structural rules (intake-only limiting; you cannot skim a wave you are part of)
genuine contention should be rare enough that it may never pay for itself.

This rests on three structural assumptions, each of which the surrounding design
already guarantees; a violation of any would reopen the cycle, so they are
contract, not convenience:

- **A unit blocked acquiring a permit holds no `inUse` permit of that limiter
  itself.** Additional concurrency is obtained only by submitting children into a
  sub-wave and parking to drive it — never by a running body grabbing a second permit
  while staying runnable. An op of weight > 1 takes its whole weight atomically at
  admission, so there is no intra-acquisition hold-and-wait.
- **Parking makes the parked unit's occupied permit idle** (`inUse−−` on park,
  `inUse++` on resume), so "parked ⟹ borrowable" is exact and needs no global
  bookkeeping.
- **Limiters gate intake, not drain, and a drain never cyclically depends on a
  permit.** Skim handlers and funnel flushes are limiter-free — they hold no permit.
  A drain *may* drive a sub-wave (whose bodies acquire via the cache, deadlock-free),
  but **a body may not skim a wave it is part of** — its own wave or any ancestor
  (its `parentWaves`). That is the one rule that would otherwise let a drain wait,
  transitively, on itself (a reentrant self-wait on its own goroutine, or a
  cross-goroutine ancestor cycle). So a parked holder's drain always completes and
  frees it: it holds
  no permit and cannot cyclically depend on one. (This *narrows* the former blanket
  skim-drive ban, which forbade a skim handler from driving *any* sub-wave — an
  independent sub-wave is not an ancestor, so it is now allowed.)

## Invariants (the model-check targets)

The isolated sketch holds these under adversarial nesting and cross-wave Resource
sharing:

- **Per-cache:** `0 ≤ inUse ≤ held`.
- **Conservation:** `Σ held` over all caches equals the permits checked out of the
  Resource (the Pool's `checkedOut` mirror), and `Σ held ≤ capacity`. A steal is a
  transfer (one cache `held−−`, another `held++`) and never changes the sum; only a
  fresh check-out (step 3) raises it, and only cache destruction lowers it. Caching
  drives `Σ held` toward `C`, but the cap is never exceeded.
- **Concurrency bound:** `#running bodies = Σ inUse ≤ capacity`, where "running"
  counts a body only while it executes its own code or a skim handler — a body that
  is purely blocked-waiting on a sub-wave it drives is lending, not running (see
  "Driving is an alternation").
- **No silent infinite wait:** every blocked delta eventually acquires, steals,
  or surfaces (see "Infeasible demand" in `dispatch-execution-split.md` — a demand
  exceeding total capacity fails fast; a demand within capacity always resolves).
- **Liveness:** no reachable state has a blocked delta while some permit is
  borrowable or some using body is running.

## Cross-limiter joint admission (no user-facing coordinator)

With deadlock-freedom established per-limiter, the only cross-limiter job left is
**joint admission** when an op's `WithLimits` spans several limiters. This needs
**no user-facing coordinator** — and, for the default discipline, no coordinating
object at all:

- **Ordered (default).** The framework acquires a `WithLimits` set in a single
  **global canonical order** (a process-wide limiter sequence). That is deadlock-free
  by the standard lock-ordering argument, fully automatic, and requires no per-set or
  per-group object. A limiter may appear in any combination of `WithLimits` sets
  because one global order makes them all mutually safe.
- **Prioritized (deferred).** Cross-op priority with starvation-avoidance
  (withholding) is the one discipline that needs a central arbiter — it must see all
  pending demand to decide who to grant and when to withhold. It is a single
  **internal global arbiter**, opt-in per op/wave, and still exposes **no coordinator
  type**: a vector touches only the limiters it names, so disjoint ops never contend
  in the fit decision. No user-defined grouping is needed.

Everything per-limiter — the lock-free local cache acquire/release of steps 1–3 *and*
the cross-subtree steal scan of step 4 (under that limiter's per-list locks) — stays
within that limiter and never routes through any cross-limiter coordination.

## Residual points

- **Executor count is bounded by nesting, not capped.** A parked executor is just
  a unit driving a sub-wave; the number of simultaneously parked executors is the
  live sub-wave nesting depth times its fan-out, which the program's own structure
  bounds. Active (running) work stays bounded by permits and the governor, so no
  explicit executor cap is needed or wanted.
- **POSTPONED reconciles cleanly.** A unit whose grant yielded before its body
  started has `inUse == 0` and never entered any cache as a running occupant, so its
  base/delta simply returns to the cache — the same path as any pre-body give-back.
  The cache model needs no POSTPONED-specific case.

## Rejected alternatives

- **Eager suspend/reclaim on every park** (`limiter-suspend-resume.md`). Correct
  but churns a suspend per park and a re-acquire per resume, and spreads a
  five-state machine across the body path. Inheritance of the idle hold reaches the
  same liveness with bookkeeping that stays local except during the rare steal.
- **Restrict stealing to fully-idle (zero-leaf) caches.** Strictly less permissive
  than partial-idle stealing, leaving usable capacity stranded under a parked unit
  whose sub-wave only partly occupies its cache. Partial-idle stealing is a
  superset, so it is at least as live and strictly better utilized.
- **Global scheduler / cycle detector for deadlock-freedom.** Overbuilt: the
  per-limiter steal-idle baseline is already deadlock-free (above). The global
  coordinator and any cycle detection are retained only for the deferred
  joint-acquisition and churn-reduction roles, not for safety.
- **Eager return of idle permits to the Resource** (minimize permits checked out).
  Reads high `Σ held` as the cost to minimize, but that is the wrong cost: eager
  return forces in-progress work to re-contend on the Resource's global counter every
  time it resumes, *adding* contenders — the opposite of minimum-WIP. Caching idle
  permits where they are likely wanted lets in-progress work complete locally and
  drain the contender pool (see "Permits as a hierarchical cache").
- **Active recall of taken permits.** A return obligation (track who stole what,
  recall it on demand) reintroduces preemption-shaped complexity and a second
  protocol. A steal with no return obligation — the victim reacquires from scratch
  if it needs the permit back — is simpler and, by the deadlock-freedom result,
  always safe.

## Open / next

- **Done (`internal/permits`):** the hybrid core for a single Resource — caches as the
  atomic128 `(held, inUse)` counter; the locality-ordered acquire (own → ancestor →
  free Resource → steal → wait) with a refcount-pinned lock-free up-walk;
  cache-until-pulled flow; the **move-to-back LRU steal** over a per-`Cache`-mutex
  intrusive DLL, with **exact removal** and CAS-drain destroy; and both acquire modes
  (non-blocking `Acquire`, blocking `AcquireWait`) with `rdvq` wake-on-free.
  Model-checked under `pgregory.net/rapid` (the algorithm, sequentially) and
  `-race`-stress-tested (the concurrency: contended inherit/delta/steal, structural
  churn vs steal, and AcquireWait liveness). No global coordinator, no idle index.
- **Lock-free forest (deferred, measurement-gated):** swap a hot list's internal for a
  lock-free structure only if it shows up as a tail-latency bottleneck — localized
  behind `Acquire`/`Release`, no API change. (The fully-lock-free `nbcq` redux was
  tried and reverted; see "Concurrency (as implemented)".)
- Leave the steal-search bookkeeping open: measure the deeper-walk/re-steal rate;
  introduce an idle index (subtree-idle bit or richer) only if measurement justifies
  it — and only with exact maintenance if it filters.
- Defer the cross-limiter coordinator and any cycle detection until a measured
  churn number, or the joint-acquisition feature, justifies them.
- Map the manager/executor pools and the governor's per-wave gate around the core
  (`dispatch-execution-split.md`, Phase 2b/2c), then sequence the migration off the
  live eager code in `limiter.go`.

