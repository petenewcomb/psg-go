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
1. **C1 — permit core into the live limiter, single pool (gut, don't remove).** Wave gains
   per-limiter `C_W^L` (lazy mkdir-p at first L-admission, `ReleaseRef` at wave-Done);
   `ctxMeta.heldRequest` → a `Permit`; `acquireOrWait` → `Cache.Acquire` (the modes);
   `suspendForEpisode`/`reclaimRequest` → coarse per-drive `Release`/`AcquireWait`. Keep the
   call sites and the single `worker.Pool` (admission still inline in
   `limiterScatterWork.Execute`); gut the eager `requestState`/`suspend`/`tryResume` to
   vestigial delegators — don't delete. *Gate: full suite + `-race` + **`TestBySimulation`
   reliably green** — the deadlock-fix milestone.*
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
5. **C4 — strip the dead eager code.** Delete `requestState`/`suspend`/`tryResume`/
   `reclaimRequest` residue + superseded block-and-help + vestigial `directRequest`.
   Mechanical. *Gate: suite + `-race`.*

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
