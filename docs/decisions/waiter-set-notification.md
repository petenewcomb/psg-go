# Waiter-set notification: ordering, reclamation, and why not a wholesale redesign

> Decision record (2026-06-30). Captures why the `rdvq` waiter/notification substrate is
> factored the way it is — one allocation-efficient reclamation mechanism with per-use
> ordering — and why three tempting redesigns (LIFO everywhere, caller-held
> `Receiver`/`Waiter` parameters, permit-forest affinity bucketing) were rejected or
> deferred. Written after driving streampool's per-dispatch allocations from ~37 to ~1.1.

## Context

A blocking dispatch parks on a *waiter set*: it registers, is woken when the thing it
waits for becomes available, and either consumes it or abandons the wait. The mechanism
is `rdvq`'s `Waiters` / `inboxOnlyQueue` (a generation-stamped inbox plus a lock-free
hint queue, `emptyInboxes`), reached through `Waiters.WaitFunc` / `Notifier.Notify`.

Crucially, this one substrate serves **heterogeneous** kinds of waiting:

- **permit acquisition** — `blockAcquire` / `AcquireWait` park on `pool().Waiters()` for a
  freed permit (the only forest-shaped kind);
- **work-availability** — `workWaiters` (wave help-drain), the `Scheduler` waiters and
  `Handoff[Work]`, the `Executor`'s `Handoff[Task]`, `workq` `Pending`/`accepted`, the
  governor's upstream `Notifier`;
- **result/skim** — `skimQueue.ListenersFor()`.

Only the first is tied to the permit forest; the rest have no affinity structure.

### The allocation problem

Each registration borrows an inbox and publishes a generation-stamped `inboxHint` (an
nbcq **node + value cell**). Profiling the comparison harness showed streampool spending
~5 of ~7 residual allocs/task here, *plus* a per-block `confirmFn` closure in
`blockAcquire`. Two experiments pinned the cause of the hint churn precisely:

- **`GOGC=off`** left it unchanged → not `sync.Pool` GC-clearing.
- **`GOMAXPROCS=1`** left it unchanged → not cross-P `sync.Pool` private-slot trapping
  (an earlier, wrong hypothesis).

It was a genuine **push-without-matching-pop accumulation**: `blockAcquire`'s
block-and-help loop registers a waiter and then aborts via `confirmFn` the instant a
permit frees during registration (the missed-notification-race closer). Each abort leaves
a hint that no `Notify` ever pops, so its node+value stay live in `emptyInboxes`, out of
the pool; under abort-heavy contention they accumulate and every registration allocates
fresh storage.

## Decision: reap on the abandon path (landed)

`inboxOnlyQueue.reapStale`, called from the abandon branch of `PopFrontFunc`: front-pop a
bounded run of leading hints, drop the **stale** ones (`TryPopFront` already recycles
their node+value), and re-push the first **live** one before stopping. Senders'
`TryPushBack` already reap leading stale hints, but only when a `Notify` arrives — the
abort path outruns notifies, so the abandoner reaps too. It lives in the shared substrate,
so every waiter use benefits uniformly.

**Safety — at most redirected, never lost.** Re-pushing a live hint briefly removes it
from the queue, which looked like a lost-wakeup risk. It is not: the reaper runs on the
*abandon* path, meaning it just *acquired* a permit (that's why it's abandoning) and is
*holding* it, not releasing — so it is not the notifier. A concurrent release-`Notify`
that hits the window either redirects to another waiter or leaves the permit free in the
cache; and a parked waiter implies pending work, which implies some holder will release,
which implies a future `Notify` that finds the re-pushed hint. So a missed wake is
deferred to the next release, not lost. `TestWaitersReapPreservesLiveWaiter` guards the
invariant; `rdvq -race` (saturation) + `TestBySimulation -race` confirm no hangs.

Combined with caching the `blockAcquire` `confirmFn` (and `h.release`) as bound method
values on the pooled `heldPermit`, this took streampool from ~7.3 → ~2.6 → **~1.1
allocs/task, flat across load** — essentially the per-task closure floor that every
bounded pool pays. The full arc: ~37 → 19 (meta pooling) → 7.3 (gen-stamped inbox +
`h.release`) → 2.6 (reap) → 1.1 (`confirmFn`).

## Rejected / deferred alternatives

### LIFO everywhere — rejected

A Treiber stack is the simplest allocation-free intrusive set, but it **starves**: it
wakes newest-first and renotify walks down toward the oldest, so under sustained
registration the bottom never gets reached. Starvation is unbounded wait → tail-latency
spikes, and tail is the primary metric. Out.

### Wholesale caller-held `Receiver`/`Waiter` parameters — rejected as a general redesign

Tempting because caller-held *intrusive* waiters allocate nothing. But:

- **The waits aren't homogeneous.** There is no single waiter contract to converge on:
  permit and work waiting want FIFO fairness; `Handoff` deliberately wants LIFO (warmest
  worker, scale-to-zero) because executor workers are interchangeable. A one-size
  redesign can't be right for all of them.
- **A lock-guarded intrusive list** (the Go runtime's `hchan`/`sudog` approach) makes
  reclamation trivial but **serializes the hot register/notify path** — exactly the
  contention `rdvq` is lock-free to avoid (`BenchmarkHandoffVsChan` shows the unbuffered
  channel's single mutex serializing past `GOMAXPROCS`).
- **A lock-free intrusive list** does *not* escape the hard problem; it relocates it. "When
  may the caller reuse its node while a drawer may still reference it?" is the identical
  race the generation stamp already solves — you'd rebuild an equivalent gen/epoch/hazard
  scheme, not delete it.
- **Draws wouldn't even be faster.** A draw's cost is dominated by the goroutine wakeup,
  identical across structures; the only delta is the hint indirection + pool borrow, i.e.
  the ~1 alloc the reap already drove out.

So the current factoring is the right one: **one reclamation substrate (gen-stamp + reap)
with per-use ordering selected by the collection trait** — FIFO `inboxQueue` for fair
permit/work waiting, LIFO `inboxStack` for interchangeable `Handoff` workers.

### Permit-forest affinity bucketing — deferred (measurement-gated)

`Cache.Acquire` up-walks `c → parent → … → root`, so a permit local in cache X is directly
consumable by X's **subtree** (descendants up-walk to X); cross-subtree access is only via
**steal** (`searchList(&p.roots)`, coldest-first, ordered by `touch()`). The single
per-pool `Waiters` is therefore subject to **head-of-line blocking**: a permit freed in X
wakes the globally-oldest waiter, who may be in another subtree, can't reach X by up-walk,
and won't steal X specifically → fails and renotifies, walking the queue to find an
X-subtree consumer. Strict FIFO is actively wrong here; we want to wake *a waiter who can
consume this permit*, not the oldest.

Affinity bucketing would fix this: per-cache waiter sets; release in X notifies X's own
waiters, then descends toward the `touch()`-marked demanding subtree, then falls back to
the steal-driven global set. Every woken waiter could then consume the permit directly.

Deferred because:

- It is **permit-forest-specific** — none of the work/skim/handoff waiting has a forest,
  and that non-permit waiting is the *common* case (permits only block when a limiter is
  actually saturated). It optimizes a corner of a corner.
- It **adds** structure (per-cache sets + demand-directed escalation); it does not
  simplify.
- The **common shallow-forest case is a no-op** (one root cache → one bucket → no
  mismatch), including in the current benchmark (flat limiter).

It is the only one of the three that is a genuine improvement rather than a lateral move,
but it should be driven by a deep-, contended-, heterogeneous-forest workload that
actually shows the renotify-walk/steal churn in the tail — which does not exist yet.

## Planned: `RenotifyFunc` → `Notification` (conservation-discharge refactor)

Separate from the alloc work, this hardens the renotify conservation soft spot (the
`wrappedRenotify` leak-on-discard, TODO.md). It is **not** an allocation win — `wrappedRenotify`
is already pooled and not in the residual — its value is making conservation *total and
explicit*. Design is fully settled (below); it is a ~20-file, concurrency-critical cascade,
so it should land as its own focused pass with the full `-race`/sim gate, not bolted onto
other work. The core (`Notification` type, `Notifier.Notify`, `Listeners`/`Waiters`) was
prototyped and compiled cleanly; reverted to keep the tree green pending the focused pass.

**The type — a value struct, not an interface (deliberate):**

```go
type Notification struct {
    n        *Notifier // set ⇒ listener-style Forward re-circulates; nil ⇒ waiter-style terminal
    fallback func()    // never nil once delivered (defaults to noop); the terminal action
}
func (m Notification) Empty() bool  // zero value ⇒ no wake delivered (woken by ctx/timer)
func (m Notification) Consume()     // wake USED — suppress fallback (no-op body; intent marker)
func (m Notification) Forward()     // could NOT use — listener re-offers via n.Notify; waiter runs fallback
```

A value struct because the bug we fix *is* a pooled-wrapper-not-returned leak: a value type
removes it **by construction** (nothing to forget to return). It references only long-lived
things (the `*Notifier`; pre-existing fallbacks — `noop` or `unmetDemandFn`) and rides in
storage pooled regardless (the parked inbox's channel for waiters, the callback stack for
listeners), so it needs **no pool of its own** and *removes* the `wrappedRenotify` pool. An
interface would force a pooled pointer impl (to avoid per-wake boxing through `chan
Notification`), re-introducing the very pooling/Put-discipline we're deleting. Trade-off
accepted: no double-settle detection (settling twice is a caller bug the value can't catch).

**`Notify` becomes total:** `Notifier.Notify(fallback func())` (and `Waiters.Notify`) run the
fallback exactly when no consumer takes the wake — so call sites drop the `if !Notify(fn) {
fn() }` guard (`accepted.go` ×3, the orphan handler). Internal delivery is `Listeners.deliver`
/ `Waiters.deliver` (return whether taken). `NoopRenotify` → unexported `noop` (default
fallback), preserving its no-nil-check role.

**The asymmetry, commented at `Notification.Forward`:** listener `Forward` re-circulates
(`n.Notify(fallback)`) because a listener may hold a still-live reserved resource that must
reach another consumer or it strands a starving waiter — and it's cheap (callbacks). Waiter
`Forward` is terminal (`fallback()`) because a woken waiter that can't use the wake means the
resource is already gone; re-offering would cascade wasteful goroutine wakeups. A *stranded*
(delivered-then-abandoned, never used) waiter wake is salvaged separately by the orphan path
(`WaitFunc`'s `processOrphanFn` re-offers via `w.Notify(stranded.fallback)`).

**Migration is mechanical once the types change** — `renotifyFn()` → `Forward()`,
drop-without-calling → `Consume()`, `rf != nil` → `!n.Empty()`. **Surface (~20 files):**
rdvq `types/notifier/listeners/waiters/listener/queue/handoff/outbox` (incl.
`PopSelectResult`'s outbox-ready renotify and the `chan RenotifyFunc` → `chan Notification`
plumbing); workq `accepted/scheduler/wait`; `wave.go` (the `block`/`skimSelect` return
threading); `permithandle.go` (`blockAcquire`/`reclaim` consumer loops); `execpool/executor.go`;
and the rdvq + workq tests. **Gate:** full suite + rdvq `-race` (incl. `saturation_test`) +
large `TestBySimulation -race` + a conservation-discharge note.

## Status

- **Landed:** the abandon-path reap; `confirmFn`/`h.release` method-value caching.
- **Not pursued:** LIFO-everywhere; wholesale `Receiver`/`Waiter` parameters.
- **Planned (focused pass):** `RenotifyFunc` → `Notification` (spec above) — design settled
  + core-compiled, reverted to keep the tree green; execute as its own gated change.
- **Deferred, measurement-gated:** permit-forest affinity bucketing — revisit only if a
  deep-forest workload demonstrates HOL in the tail; the fix is affinity *bucketing of the
  existing queues*, not a waiter-set replacement.

See also `docs/rdvq-inbox-reclamation.md` (the generation-stamped inbox the reap builds
on) and the package overview in `internal/rdvq/doc.go` (FIFO/LIFO consumer-selection
strategies).
