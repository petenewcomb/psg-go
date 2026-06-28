# Phase 2b Plan: Dispatch/Execution Split — Pool & Queue Model

**Status: design in progress (2026-06-27).** Captures the pool/queue model converged
so far in design discussion. Forest construction, governor placement, mode mapping, and
migration sequencing are still being worked out (see "Open / next — more detail to
settle"). Builds on `docs/dispatch-execution-split.md` (the architecture) and
`docs/permit-core.md` (the permit allocation core, now implemented as the hybrid
lock-free-hot-path / per-`Cache`-mutex forest in `internal/permits`).

## Goal

Split **admission** (schedulers/managers) from **execution** (executors): schedulers own
queues + permits + governor and **never run user code**, so there is always a goroutine
admitting ready work no matter how many executors are parked inside blocking bodies (the
always-live-dispatcher invariant). Map this onto the existing `internal/worker` pool +
`internal/workq` queues + the new `internal/permits` core; put the governor gate on the
admission path; migrate off `limiter.go`'s eager `directRequest`/`suspendForEpisode`/
`reclaimRequest` **without a flag day** (old and new coexist; cut over at green
checkpoints).

## The pool model: one generic pool, two instances

The current `worker.Pool` (`internal/worker/pool.go`) is already "a demand-spawned,
idle-exiting goroutine pool whose workers run a loop over a shared queue." Factor its
**lifecycle machinery** into a generic core, reused verbatim, with the per-worker loop
pluggable:

- demand-driven spawn — the spawn chain + `spawnConcurrencyLimit` de-stampede
  (`pool.go:184-275`); spawn token held only spawn→work-secure, never through a body;
- idle-exit scale-to-zero — `workerIdleTimeout`, each worker independently times out;
- refcount + `Wait` lifecycle (`pool.go:114-155`).

Two instances:

- **Executor pool** — runs user bodies; **may block** (that is its job). The worker is
  the "simple loop": `PopFront` (blocking rendezvous) → `Run` → repeat, idle-exit. It
  drives the new unbuffered primitive (below). **Not** a `workq.Worker`; it has **no
  `TryPopFront`**.
- **Scheduler pool** — admits work; **never runs user code**. `workq.Worker`-style over a
  shared `workq.Accepted` (the buffered priority tier: fresh → postponed → scheduled).
  Per item: non-blocking permit acquire (`permits.Cache.Acquire`: own → ancestor → free
  Resource → steal) + governor check; on success **blocking-`PushBack`** the admitted
  body onto the executor primitive; on a permit miss **postpone**.

## The queue model: buffered intake, unbuffered handoff

The two stages are deliberately asymmetric:

- **body → scheduler: buffered, non-blocking (`Accepted`).** A nested same-wave submit
  drops work and the body races on; backpressure is applied at *admission*, not by
  blocking the executor body. `postponed` (permit miss) and `scheduled` (deadline
  flushes) are *necessary* buffering.
- **scheduler → executor: a NEW unbuffered rendezvous primitive.** Blocking `PushBack`;
  **zero buffer dwell** for runnable bodies.

### The new unbuffered primitive

= `inboxOnlyQueue` + a new sender-side `inboxWaiters` — i.e. **today's `rdvq.Queue` minus
the entire outbox tier, plus a `Waiters` on the sender side mirroring `outboxWaiters`.**

- **Kept:** the inbox tier (receiver-owned mailboxes; LIFO consumer selection via
  `inboxStack`).
- **Deleted:** the outbox tier — `outboxes`/`emptyOutboxes`/`fullOutboxes`/`outboxPool`,
  the generation-stamped `outboxHint`s + `claimEmpty`, drop-and-go fill, block-fill
  pacing, `outboxFreed`. This is the hairiest lock-free code in rdvq.
- **`inboxWaiters` (new, sender side):** `PushBack` tries a direct handoff to a parked
  receiver; if none, it **parks the sender** until a receiver appears (blocking send).
  Mirror of `outboxWaiters` (receiver side: parks consumers waiting for a filled outbox).
- **No `TryPopFront`.** `TryPopFront` is a `Queue`-only method that drains `fullOutboxes`
  (`queue.go:467`); with no outbox tier there is nothing to poll. The consumer's only
  verb is the blocking rendezvous `PopFront`. ("Unbuffered" ⟺ no `TryPopFront`.)
- Net: a **lock-free unbuffered channel** with LIFO-warmest consumer selection.

### Why LIFO consumer selection is load-bearing (not just locality)

With **FIFO** consumer selection the queue round-robins work across all parked workers,
so every worker is refreshed on a rotation; if that period is shorter than the idle
timeout, *no* worker ever accumulates a full idle window — the pool pins at its
high-water mark and never shrinks. **LIFO** lands work on the most-recently-parked
(warmest) worker, which re-parks on top and absorbs the load, while workers deeper in the
stack are never selected, time out, and exit. So **LIFO is what makes scale-to-zero
fire.** Pairing: **FIFO item order** (fairness, no work starves) + **LIFO consumer
order** (scale-down). This is why the executor pool needs `inboxStack`, not
`inboxQueue`.

### Latency: a buffer-push and a spawn signal are the same event

Work only has to be buffered when the handoff found no waiting worker — and that exact
condition fires demand → spawn. So the buffer is a *transient spawn-gap holding area*
whose depth trends to zero under healthy scaling; runnable work's dwell time is bounded
by spawn latency, not queue-drain time (the P99/max win). With the unbuffered primitive
this is literal: the producer **parks (block-as-demand)** holding the body, an executor
is spawned, and it takes the body directly — no intermediate buffer.

### Demand vs. backpressure (scale only when all busy)

Two reasons work waits; only one should spawn:

- **spawn-gap** (no worker free for *runnable* work) → **fire demand** (spawn). This is
  the executor handoff and fresh scheduler-intake.
- **necessary buffering** (the work genuinely *can't proceed*: `postponed` on a permit
  miss, `scheduled` for a deadline) → **never spawn**. A scheduler applying backpressure
  parks as an *available waiter*, so it doesn't count as busy. The retry signals —
  `permits.Pool` wake-on-free (the `Release`/`destroy` notify) and governor-clear — must
  **wake** a parked scheduler (`Accepted` waiters; no-op if all busy, since a busy
  scheduler re-checks `postponed` on its next loop), **not** route through `unmetDemand`.

So "scale only when all schedulers are busy, not when they're applying backpressure" =
"fire demand only for spawn-gap buffering, never for necessary buffering."

## Forest construction (the permit-core wiring)

This is how `internal/permits` maps onto waves/bodies. Settled in design discussion;
records the model and the alternatives weighed.

### One node per (sub-wave, limiter); bodies are occupants

A `permits.Cache` is created **per `(wave, limiter)`** — `C_W^L`. `C_W^L.held` = the
permits wave W holds for limiter L (its checked-out base + deltas, pooled), `inUse` = W's
currently-running bodies. A **body is not a node** — it holds a `Permit` (an occupation,
one per limiter), backed by whichever cache it acquired from. So the L-forest mirrors the
**wave** nesting, not the body nesting, and hooks into the existing wave lifecycle
(`ensureInit`/drain) rather than per-body machinery.

The ancestry plumbing already exists: `ctxMeta.parent` is the driving-derivation chain and
`currentHeldRequest()` (`ctxmeta.go:89`) already walks it to find the enclosing held
permit. That becomes `currentHeldCache(L)` — the same walk, returning the parent wave's
`C_^L`. The genuinely new piece is the **refcount edge** (`NewChild` → `parent.refs++`,
child cascades `ReleaseRef`): caches outliving bodies, which the eager `limiter.go` lacks.

### Lazy "mkdir -p" of the ancestor chain

At a wave's **first admission for limiter L**, ensure the ancestor L-cache chain exists:
walk W's wave ancestry and create any missing L-caches from the **nearest already-existing
`C_^L` (or the Pool root) down to W** — intermediate ancestor waves that hold no L-permit
get a **`held=0` pass-through** node — then acquire into `C_W^L`. So the chain is built
lazily but always mirrors the wave nesting.

Worked example (W → body A → drives wave V → body B, only B carries L): B's first acquire
mkdir-p's `C_W^L` (held=0 pass-through, since A doesn't carry L) and `C_V^L`, then checks
out into `C_V^L` (`held=1, inUse=1`). B's permit lives at V and is taken-from / returned-to
`C_V^L` for its whole life; it **never touches `C_W^L`** (which stays a pure structural
parent) — *because* W holds no L-permit. If W *did* hold an idle L-permit, B would instead
inherit it (occupy `C_W^L`) via the up-walk.

**Create the pass-through, do not skip non-L ancestors.** Skipping (hanging `C_V^L`
directly off a higher cache) would force a **concurrent re-parent** of `C_V^L` the moment
W — or a skipped intermediate — later gained its own L-demand. The held=0 pass-through
keeps V permanently in the right place, so any ancestor's idle L-permit (present or future)
is inheritable via the plain `Cache.parent` up-walk with no re-parenting.

### Acquire / inherit / suspend / reclaim

- **Fresh checkout** (step 3): `held++, inUse++` on the **acquirer's own** wave cache
  `C_W^L`. So a wave's own permits live in its node and same-wave siblings reuse them with
  a local step-1 hit.
- **Inheritance** (step 2): **occupy-in-place** — `inUse++` on the ancestor that holds the
  idle permit; the permit **does not move** (`permit-core.md`). The descendant's own cache
  is untouched. Re-finding it on the next acquire is a short, lock-free up-walk
  (O(nesting depth) atomic loads).
- **Suspend / reclaim is COARSE, per *drive call* — a body holds its permit only while
  running *its own user code*.** When a body enters a drive (`Skim`/`SkimAll`/
  `CloseAndSkimAll`) it `Permit.Release`s its permit(s) to **whatever cache backs them**
  (its own wave cache for a checked-out permit, an ancestor cache for an inherited one) and
  **stays lent for the entire drive — including while running that sub-wave's skim
  handlers**, because handlers are *drain*, not the driving body's own code (they're
  limiter-free w.r.t. the driver). It reacquires (`C_W^L.AcquireWait`, the full
  locality-ordered search from its wave cache outward — usually a cheap step-1 hit on the
  permit it released, may block/steal under contention) **only when the drive call returns
  control to the body's own code**. So bracket the *whole* `SkimAll`, not each internal
  `Skim` (a standalone manual `Skim()` keeps its per-call bracket — control does return to
  the body after it). **NOT a per-handler alternation** — that contradicts both "limiters
  gate intake, not drain" and the `ΣinUse` bound's correct meaning (running = executing own
  code, *not* a skim handler). No "suspended" state in the cache — a parked body just isn't
  occupying its permit; "suspended" is only the body's record of *its own* backings to
  reacquire (a subset of what its sub-waves acquired). **`permit-core.md` needs a fix here:
  its "Driving is an alternation → reacquire before each skim handler" and the
  concurrency-bound's "or a skim handler" both contradict this and must be struck.**
- **Multi-limiter (`WithLimits`):** one held cache per limiter; joint acquire in the
  **global canonical order** at admission; on **any** miss, **release the partial and
  postpone the whole** body (so a postponed body holds no `inUse` permit — preserving the
  deadlock-freedom assumption).

### Drain limiting — Skimmer `WithLimits` / Funnel `WithFlushLimits` (opt-in)

The intake-vs-drain dichotomy *for limiting* dissolves: **every op may carry limits, and
limited drain is allowed** — `NewSkimmer(h, WithLimits(...))` limits the skim handler, and
`NewFunnel(factory, WithLimits(...), WithFlushLimits(...))` limits accumulate (`WithLimits`,
intake) and flush (`WithFlushLimits`, drain) **separately**. **Opt-in:** with no such opt,
a handler/flush is limiter-free drain (holds no permit, lends the parent nothing) — the
unchanged common path.

A limited handler/flush acquires its **own** limiters (its own cache(s) in the forest,
parented by the wave nesting), **not** the driving body's permit — so it's just another
limited body, and the forest construction above covers it with no new machinery; a free
handler/flush gets no cache. This revises the locked "limiters gate intake, not drain"
principle to "**drain depends on a permit only if it opted in, and then it's deadlock-free
by the same per-limiter machinery**" (a limited handler lends when it parks; others inherit
its idle permit — e.g. it inherits the *driving body's* lent permit when same-limiter — and
steal across limiters; the "can't skim a wave you're part of" rule, now *more* load-bearing,
closes the self-cycle; always-live-managers is untouched since managers don't run handlers).
**This lifts a conservative restriction *because* the foundation got stronger, so limited
drain MUST be exercised in the permit-core model-check** (a parked holder whose drain itself
needs a permit — same-limiter-inherit and cross-limiter cases). It's also a **surface
change** (`WithLimits` on `NewSkimmer`, `WithFlushLimits` on `NewFunnel`) to ratify.

### Lifetime / refcount

The cache refcount is **separate** from the wave's other refcounts (in-flight / barrier),
because its reference owners differ (notably sub-sub-waves that outlive a sub-wave). A wave
node's **self-ref drops when the wave is complete** (gated on the wave's *own* Done, per
its other refcounts); descendant node refs (`NewChild`) keep it alive past that until every
descendant cache has drained. So `C_W^L` is destroyed only when W is Done **and** its
subtree's permits have all drained, at which point its `held` returns to the Resource
(CAS-drain).

### Across the scheduler/executor split

`C_W^L` lives on the wave; `ctxMeta` (the ancestry) rides B's dispatch ctx, so both cross
the handoff intact:
- **Scheduler (admission):** mkdir-p the ancestor chain + non-blocking `Acquire`; postpone
  on miss.
- **Executor (run):** the park/resume alternation (`Release` / `AcquireWait`), then final
  `Release` + the body's contribution to the wave/refcount on completion.

### Alternatives considered and rejected

- **Per-body cache nodes.** Needs a lazy body node + a permit **transfer** primitive to get
  per-body base reservation. Rejected: wave-level pooling is *coarser* → **fewer** steals
  (which is permit-core's own goal), the Resource still enforces the global limit, and it
  drops the transfer entirely. Per-body reservation buys nothing here.
- **Hoist-on-inherit** (move an inherited permit down into the user's wave cache). Rejected:
  it **churns the driving alternation** — the parent's per-skim-handler reacquire would have
  to steal the permit back down-tree, and the sub-wave would hoist it back, ping-ponging on
  the hottest pattern. Occupy-in-place keeps the permit at its home so the parent's frequent
  reacquires stay local.
- **Skip non-L ancestors** when building the chain. Rejected: forces concurrent re-parenting
  when an ancestor later gains L-demand (see "mkdir -p" above).
- **`Cache` == a single permit / `held` replicated down the path (hierarchical
  reservation).** Rejected: the `(held, inUse)` counts *are* the semaphore (compact
  N-concurrency + sibling-sharing by occupation); one-cache-per-permit either explodes node
  count or forces siblings to steal instead of inherit, and held-replication needs
  loaned-down accounting (`borrowable = held − inUse − Σchildren.held`) plus double-use /
  return-up rules — strictly more than the flat counter.

## Open / next — more detail to settle

- **Mode mapping — SETTLED.** Three submit kinds → three modes: nested same-wave →
  scheduler **non-blocking `Cache.Acquire`, postpone on miss**; mid-body reacquire →
  **blocking `Cache.AcquireWait`**; top-level → inline **skim-retry** (non-blocking
  `Acquire` + skim-own-wave on miss = backpressure; the block-and-help now bounded to
  `wv.skim`). The drive suspend/reclaim is the **coarse per-drive bracket** (see "Acquire /
  inherit / suspend / reclaim").
- **Governor placement — SETTLED.** The per-wave `Governor` + `downstream` counter +
  `DownstreamWork` mechanism is unchanged in purpose; the gate is checked on **both
  admission paths** against the admitted item's wave-governor — top-level **skims** if
  clogged, the scheduler **postpones** if clogged. `decrementDownstream`'s relief notify
  becomes a **wake of the scheduler** (not a spawn). So the scheduler has **two retry
  triggers** — permit-free (`permits.Pool`) and governor-clear (`Governor`) — feeding the
  one `Accepted` waiter set; a woken scheduler just re-runs admission (re-checks both gates).
- **Inbox-stack lock-freedom — DEFERRED** (measurement-gated; the `inboxStack` mutex works).
  See note below.

## Migration sequencing (no flag day, gut-before-removing)

Every step builds and passes its gate; old and new coexist; any regression falls back to
the prior green commit. **De-risking key:** C1 (permit core into the live limiter) *is* the
deadlock fix and is validated by `TestBySimulation` **before** any pool-split risk, so the
pool split lands on a known-good base.

0. **Permit-core hardening** (`internal/permits` + docs, isolated). Pool `Cache` via
   omnipool; add model-check coverage for **limited drain** + **multi-limiter joint
   admission**; fix `permit-core.md` (strike the per-handler alternation + the bound's "or a
   skim handler"). *Gate: permits rapid + `-race`.*
1. **C1 — permit core into the live limiter, single pool. ✅ DONE (2026-06-27).** Wave gains
   per-limiter `C_W^L` (lazy mkdir-p along the driving chain, `ReleaseRef` at wave-Done via
   a new `wavestate.onDone` hook); `ctxMeta.heldRequest` → `held *heldPermit`; `acquireOrWait`
   → the three-mode `gateAcquire`; `suspendForEpisode`/`reclaimRequest` → coarse per-drive
   `suspendHeldPermit`/help-shaped `reclaim`. The eager machinery was **removed** (native
   replacement — see "Removal happened IN C1"); `wv.block` retained, retargeted onto the
   Pool's waiters. Lost-wakeup fixed via `Pool.WakeAll`-on-release. Single `worker.Pool`,
   admission inline. *Gate MET: full suite + `-race` + `TestBySimulation` reliably green
   (≥300 `-race` runs) + lint.*
2. **B — dispatch infra** (independent of C1; can overlap). The new unbuffered rdvq
   primitive (`inboxOnlyQueue` + `inboxWaiters`, blocking `PushBack`, no `TryPopFront`);
   refactor `worker.Pool` into the generic lifecycle + pluggable per-worker loop. *Gate:
   rdvq + worker unit tests, in isolation (not yet wired).*
3. **C2 — the pool-split cutover.** Two pool instances (scheduler + executor); move
   admission off the executor — scheduler drains `Accepted`, admits (`Acquire` non-blocking
   + governor, postpone), hands the body to the executor via the unbuffered primitive
   (block-as-demand); top-level admission stays inline on the driver (skim-retry); executors
   run bodies (the drive alternation). *Gate: full suite + `-race` + `TestBySimulation` +
   the **latency/alloc benchmarks** (use the real methodology — heavy-tailed blocking-I/O
   work, P99/max, swept P:D ratios — not a throughput microbench) — the architecture +
   latency milestone.*
4. **C3 — drain limiting** (anytime after C1). `WithLimits` on `NewSkimmer`,
   `WithFlushLimits` on `NewFunnel`; limited handlers/flushes acquire their own permits
   (forest bodies); default = free drain. *Gate: suite + `-race` + the model-check now
   exercising limited drain.*
5. **C4 — strip the dead eager code. ~Absorbed into C1.** The eager request machinery was
   removed in C1 (native replacement); the `applicant` sizing went with it. What remains for
   a later pass: the thundering-herd wake efficiency and any `BlockBehavior`/`shouldBlock`
   plumbing once C2 reshapes dispatch. *Gate: suite + `-race`.*

## C1 implementation mapping (live limiter → native permits)

The concrete wiring from `limiter.go`'s eager path onto `internal/permits`. **Ratified in
design discussion (2026-06-27):** replace the `request`/`limiterImpl`/`resource` interfaces
with a **native** permits integration (no in-place reimplementation behind the old seam);
go **straight to the forest** (no flat-topology intermediate); keep the **single
`worker.Pool`** with admission inline (no pool split — that is C2).

### Confirmed simplification: single limiter per op

`opConfig.singleLimiter()` (`opoption.go:33`) **panics on >1 limiter**, so every op carries
exactly one `Limiter`. C1 is strictly single-limiter; multi-limiter joint admission is a
later concern (C3 / follow-up). This removes the whole partial-acquire / canonical-order
axis from C1.

### Type correspondence

| Eager (`limiter.go`) | Native permit-core |
| --- | --- |
| `Limiter.impl` / `directScheduler{res, notify}` | one `*permits.Pool` per Limiter (+ a `permits.Resource`) |
| `semaphoreResource` (atomic counter) | a `permits.Resource` impl — near-direct (`TryAcquire(n)`/`Release(n)`) |
| `directScheduler.notify` (`workq.Notifier`) | `Pool.waiters` (`rdvq.Waiters`) — internal to `Acquire`/`AcquireWait` |
| `request` handle (PENDING→HELD→SUSPENDED→POSTPONED→DONE) | a held `Permit` + the body's own cache `C_W^L` |
| `ctxMeta.heldRequest request` | a held-permit handle `{pool, ownCache, permit}` (below) |
| `currentHeldRequest()` walk | `currentHeldCache(pool)` walk (same `ctxMeta.parent` chain) |
| `newRequest(applicant)` + the gate's `tryAcquire` | `C_W^L.Acquire()` (own → ancestor inherit → free → steal) |
| `req.suspend()` (lend to resource) | `permit.Release()` to its backing cache (own or inherited ancestor) |
| `reclaimRequest` (help-shaped loop) | `C_W^L.AcquireWait(ctx)` — **plain park, no help loop** |
| `req.release()` / `freeRequest` | `permit.Release()` final; cache `ReleaseRef` at wave-Done |
| *(no analog — flat)* | **the forest**: `C_W^L` parented per driving ancestry; inheritance |

### The three load-bearing seams

1. **Lifecycle owner: per-work handle → per-wave cache + transient permit.** Today one
   `request` is *shared* by the gate and the body. Natively there is no per-admission object:
   the gate does `C_W^L.Acquire()` yielding a `Permit` that must reach the body. The held
   handle becomes `{pool, ownCache, permit}` where `ownCache` is `C_W^L` (reacquire up-walks
   from it) and `permit.backing` is `ownCache` or an inherited ancestor. The gate stamps the
   acquired `Permit` onto `bodyMeta` *after* acquire (eager stamped at borrow, pre-acquire).

2. **The forest is net-new and is *the* deadlock fix.** Nothing eager parents one admission
   to another. The fix needs `C_V^L.parent → … → C_W^L` so a parked driver that `Release`s to
   `C_W^L` is *inherited* by its sub-wave's `C_V^L.Acquire()` up-walk. Requires per-Wave lazy
   `C_W^L`, **mkdir-p of the ancestor chain** keyed on the driving `ctxMeta.parent` chain,
   refcount edges, `ReleaseRef` at wave-Done.

3. **The `workq.Notifier` wait surface is replaced, not bridged.** Eager postpone/block wait
   on `req.notifier()` (`workq.Notifier`); permits parks on `rdvq.Waiters` inside
   `AcquireWait`. Native integration drops `acquireOrWait` and routes each mode to permits
   directly (below).

### The native gate (three modes) — block-and-help RETAINED

**Implementation note (revises the earlier "no help loop" sketch).** `acquireOrWait`,
`reclaimRequest`, and the `request`/`directScheduler` machinery are removed, but the
block-and-help primitive `wv.block` is **kept and retargeted** onto the permit Pool's
waiters. The plan originally expected the forest's inheritance to let both the top-level
gate and the mid-body reclaim be plain parks (a bounded `wv.skim` retry / `AcquireWait`).
That is **wrong**: validated against `TestBySimulation`, two help requirements survive —

- **Mid-body reclaim MUST stay help-shaped.** A plain-wait reclaim deadlocks under shared
  limiters exactly as the eager `reclaimRequest` warned: the permit-holder the reclaimer
  waits on can be blocked posting a result to the skim queue, and only the reclaimer's
  help-drain consumes it. Inheritance fixes the *acquire* side (a sub-wave inherits a
  parked parent's idle permit), not this *reclaim* side. So reclaim ports the eager
  help-loop verbatim — help-drain `wv` while waiting, fall back to a plain park on
  `ErrWaveDone` (help domain exhausted).
- **Top-level gate is block-and-help, not bounded skim-retry.** A bounded `wv.skim` loop
  blocks in `skim` waiting for *work* and never sees a *permit-free* wake (they arrive on
  the Pool's waiters), so a top-level submit blocked on a contended limiter hangs even
  after the permit frees. `wv.block` already integrates both — it waits on the given
  waiters *while* help-draining — so the gate waits on `Pool.Waiters()` with `h.acquire`
  as the confirm.

So the three modes are: **nested/queued** → non-blocking `Acquire`, on miss register on
`Pool.ListenersFor()` and re-check (manager postpone); **top-level** → block-and-help on
`Pool.Waiters()`; **mid-body reclaim** → help-shaped loop on `Pool.Waiters()`. The
suspend/reclaim bracket is **coarse, per drive call** (whole `SkimAll`).

### Lost-wakeup: a freed permit needs renotify conservation, not a single bare wake

A residual `-race` `TestBySimulation` hang during the cutover turned out to be a **lost
wakeup**, not a permit-accounting bug (forcing unlimited permits made 120/120 `-race` runs
pass; the hang is purely in the wait/wake). The cause was a regression *introduced* by the
cutover: the Pool's wake was split into two bare `Listeners.Notify(nil)` +
`Waiters.Notify(nil)` calls, dropping the **renotify conservation** the eager scheduler's
single `rdvq.Notifier` provided. A freed permit can be claimed by any waiter (acquire is a
forest-wide search), so waking *one* is correct — *provided* a consumer that cannot use the
wake re-delivers it. A postponed manager registers its idempotent, shared controller
listener, then re-checks and succeeds, leaving a **stale listener**; a bare `Notify(nil)`
that wakes it re-drives work which doesn't consume the permit and reports the wake
delivered, so a genuinely-waiting executor is never notified and a borrowable permit sits
idle forever.

The fix is to keep the Pool's wait/wake as a single `rdvq.Notifier` and wake with
`notify.Notify(nil)` — which hands the woken consumer a **wrapped renotify**, so a consumer
that can't use the wake (a worker re-driven for the wrong queue, a stale listener)
re-delivers it down the chain (`controller.go` forwards a permit listener to its Accepted
queue's waiters and re-fires the renotify when the postponed work wasn't executed) until a
real waiter takes the permit. Single wake + conservation — no thundering herd. (The forest's
inheritance does not eliminate the postpone+wake dependency: a sub-wave body gates *before*
its parent enters the drive and lends, so it postpones and relies on the lend's wake.)
`WakeAll` (NotifyAll both) is reserved for events that free *several* permits at once — a
`destroy` returning held to the Resource, or a `SetMaxConcurrency` raise.

### The held-permit handle

Replaces `ctxMeta.heldRequest request`:

```go
type heldPermit struct {
    pool     *permits.Pool   // which limiter (nil for unlimited ops)
    ownCache *permits.Cache  // C_W^L — reacquire target (up-walks to inherit)
    permit   permits.Permit  // current occupation; zero-value backing == not occupying
}
```

`permit.backing == nil` *is* the suspended/not-yet-acquired state — so the suspend
re-entrancy no-op (nested brackets) falls out: `suspend` Releases only if `backing != nil`;
`reclaim` is `ownCache.AcquireWait`. No separate state enum.

### `ensureCache` / mkdir-p

A Wave lazily owns one `C_W^L` per distinct Pool used by ops dispatched into it (a
`map[*permits.Pool]*permits.Cache`, guarded). At W's first admission for pool P:

1. If `wv.cacheFor(P) != nil`, return it.
2. Otherwise walk the **driving** `ctxMeta.parent` chain collecting the distinct ancestor
   *waves*; find the nearest ancestor wave that already has a `C_^P` (or the Pool root if
   none). mkdir-p a `C_^P` for **each** intermediate ancestor wave that lacks one (held=0
   pass-through — *don't* skip, else concurrent re-parent), chained parent→child, then create
   `C_W^P` as the deepest child. Refcount edges come free from `Pool.NewCache`/`Cache.NewChild`.

The driving ancestry is the *same* chain `currentHeldRequest` walks today, so this is
consistent with existing inheritance scoping (async worker borrows sever `parent`; the
synchronous `ensureCtxMeta` derivations rebuild it for sub-wave drives).

### Lifetime

`C_W^L`'s self-ref drops at wave-Done (gated on the wave's own completion); descendant
`NewChild` refs keep it alive until the subtree drains. Wire `ReleaseRef` into the wave
drain (alongside the existing barrier/in-flight bookkeeping).

### Removal happened IN C1 (revises the gut-don't-remove plan)

The native replacement was decided (not a reimplementation behind the old seam), so the
eager `request`/`directScheduler`/`directRequest`/`requestState`/`acquireOrWait`/
`reclaimRequest`/`applicant`/`resource`-interface code had **zero callers** the moment the
call sites flipped, and it would not even compile against the renamed `ctxMeta.held`. So C1
**removed** it rather than leaving vestigial delegators — there was no seam left to wrestle.
`NewSemaphore`/`SetMaxConcurrency` stay, re-pointed at the `permits.Pool` + the
`semaphoreResource` (now a `permits.Resource`). `wv.block` and the `blockingWorkAdder`
machinery are **kept** (retargeted onto the Pool's waiters — see the native gate). Net: C4
is largely absorbed into C1; what remains for a later pass is the thundering-herd wake
efficiency and any residual `BlockBehavior`/`shouldBlock` plumbing once C2 reshapes
dispatch.

## Note: the inbox stack is not lock-free

`inboxStack` (`internal/rdvq/inboxonly.go:259`) is a `sync.Mutex`-protected slice with an
atomic empty flag: `empty.Load()` is a lock-free fast path, but `Push` and a non-empty
`TryPop` take the mutex. Under the high-throughput executor/scheduler handoff this mutex
is a real contention candidate.

- **Aspiration:** a lock-free stack primitive alongside `nbcq`. The natural form is a
  **Treiber stack** — an intrusive per-`inbox` `next` pointer + a `CAS` on the head —
  which needs ABA protection like `nbcq`'s generation-tagged pointers.
- **Simpler alternative to weigh:** a lock-free fast path for the top inbox (the LIFO
  hot slot) with the mutex'd slice as the deeper fallback — a partial optimization rather
  than a full lock-free stack. Measurement-gated.
- **Accuracy note:** the `next`-pointer floated in the `permits` design discussion was
  the lock-free *fast lane*, which we **dropped** in favor of the locked DLL
  (move-to-back); `permits.Cache` itself uses no `next`-pointer. The carryover here is
  only the *concept* — an intrusive single `next` per node as a lock-free shortcut —
  which is exactly what a Treiber stack uses.
