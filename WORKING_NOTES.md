# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

**►►► B LANDED — DISPATCH INFRA (ISOLATED, NOT WIRED) (Phase 2b, 2026-06-27).** The two
building blocks the pool-split (C2) needs, both standalone with no live consumer yet:
- **B1: `rdvq.Handoff[T]`** (`internal/rdvq/handoff.go`) — the lock-free **unbuffered**
  rendezvous = the existing `inboxStackQueue` (inbox tier, LIFO warmest-first consumer
  selection) + a new sender-side `inboxWaiters`. Blocking `PushBack(ctx,value)` (park on
  `inboxWaiters` via the standard register-then-recheck confirm); receiver wakes one parked
  sender after registering its inbox; **no `TryPopFront`** (no outbox tier to poll). Senders
  are never stale (each actively sends), so NO renotify conservation is needed (unlike the
  permit pool). Tested: concurrent exactly-once, sender-blocks-then-delivers, ctx-cancel
  both sides; 20× `-race` + full rdvq `-race`.
- **B2: generic `worker.Core[E]`** (`internal/worker/pool.go`) — the demand-spawn +
  idle-exit + refcount/`Wait` lifecycle factored out of `worker.Pool`, with a **pluggable
  `WorkerLoop[E]`** and the demand source decoupled (`TrySpawn`). `worker.Pool` (the
  scheduler pool, `NewPool`) is rebuilt as `Core + sharedQueue + driveQueue` (the
  workq.Worker loop) — `defaultPool` and all its promoted methods unchanged. Isolated `Core`
  tests added (demand-spawn, Wait-join+reuse, scale-to-zero). Live path unchanged: full
  suite + 60× `-race` sim green.
- **Deferred to C2:** the **block-as-demand** hook (a `Handoff.PushBack` park → spawn an
  executor) is intentionally NOT in B1 — it lands when the executor pool is wired. The
  executor pool itself = `Core[E] + Handoff + a PopFront→Run loop` (C2).

**►►► C1 LANDED — NATIVE PERMIT CORE IN THE LIVE LIMITER; THE DEADLOCK IS FIXED
(Phase 2b, 2026-06-27).** The eager `limiter.go` request machinery (`directScheduler`/
`directRequest`/`request`/`acquireOrWait`/`reclaimRequest`/`applicant`/the `resource`
interface) is **replaced** by a native `internal/permits` integration on the **single**
`worker.Pool` (no pool split — that is C2). Surface unchanged. This is the
deadlock-fix milestone: **`TestBySimulation` is reliably green, including ≥300 `-race`
runs** (the pre-existing ~1/120 `-race` hang is gone). Verified: full `./...` `-short`
suite + `-race` + lint (golangci 0 issues) + `internal/permits` rapid/race.

- **Forest construction** (`wavepermits.go`): one `permits.Cache` per `(wave, Limiter)`
  = `C_W^L`. A Wave lazily owns `map[*permits.Pool]*Cache` (guarded), mkdir-p'd along the
  driving `ctxMeta.parent` chain at dispatch (`ensureCache`/`ensureCacheChain`/
  `createCache`); the immediate forest parent is the dispatcher's wave (`M.wave`), so the
  canonical "parked parent lends to sub-wave" case inherits directly and deeper gaps fall
  back to a forest-wide steal (still correct). Self-ref dropped at **wave-Done** via a new
  `wavestate` **`onDone`** hook → `releaseCaches`; descendant `NewChild` refs outlive.
- **Native handle** (`permithandle.go`): `ctxMeta.heldRequest request` → `ctxMeta.held
  *heldPermit` `{ownCache, permit}`; `currentHeldRequest` → `currentHeldPermit`. A zero
  `permit` IS the suspended state (re-entrancy no-op falls out). Created at dispatch
  (launcher `newScatterWork` / funnel `Init`), stamped at `borrowBodyContext`, acquired at
  the gate, released at completion (pooled via `heldPermitPool`).
- **Gate = three modes** (`gateAcquire`): nested/queued → non-blocking `Acquire` +
  `Pool.ListenersFor()` postpone; top-level → **block-and-help** on `Pool.Waiters()`
  (`blockAcquire`); mid-body reclaim (the suspend brackets) → **help-shaped** loop
  (`reclaim`). **`wv.block` is RETAINED and retargeted onto the Pool's waiters** — the
  earlier "no help loop / bounded skim-retry" sketch was WRONG (a plain reclaim deadlocks
  vs a result-poster; a `wv.skim` loop never sees a permit-free wake). Suspend/reclaim is
  **coarse per drive call**.
- **Lost-wakeup fix (a cutover regression):** a residual `-race` hang root-caused NOT to
  permit accounting (forcing unlimited permits → 120/120 `-race` pass) but to a **lost
  wakeup** the cutover introduced by splitting the Pool's wake into two bare `Notify(nil)`
  calls, dropping the **renotify conservation** the eager scheduler's single `rdvq.Notifier`
  had. A stale postpone listener swallows a bare wake without re-delivering it. Fix: keep the
  Pool's wait/wake as ONE `rdvq.Notifier`; `Release` wakes with `notify.Notify(nil)` (wrapped
  renotify → a consumer that can't use the wake re-delivers it down the chain to a real
  waiter). `WakeAll` (NotifyAll) only for multi-permit events (destroy / `SetMaxConcurrency`).
  Single wake + conservation, no thundering herd. (An earlier `WakeAll`-on-every-release was
  rejected as a band-aid.) Also fixed a reuse data race: `wavestate` `onDone` now runs BEFORE
  `close(doneChan)` so cache teardown completes before a re-arm can race it.
- **Deferred to C4 / not yet done:** the `applicant` sizing (`Processor`/`Value`/`Err`)
  was removed (re-derive natively when weighted resources land); multi-limiter still panics
  at construction (`opConfig.singleLimiter`); drain limiting (C3) untouched.

**►►► PERMIT CORE: HYBRID (LOCK-FREE HOT PATH, LOCKED FOREST) + WAIT/WAKE —
`internal/permits` (Phase 2a, 2026-06-26).** The concurrent core is a hybrid: the hot
acquire path is lock-free (atomic128 counter), the forest structure is an intrusive
doubly-linked list guarded by per-`Cache` mutexes. This **supersedes the fully-lock-free
`nbcq` forest** (`6ba8833`): that port had to drop the original move-to-back LRU
(reordering a shared lock-free queue isn't possible) and grew a sentinel-cycle steal +
lazy reaping + (planned) gen-tagged entries / a `next`-pointer fast lane just to claw the
LRU back. A per-`Cache` mutex around a DLL gives **O(1) interior move-to-back** (the
original LRU, restored), **exact removal** (no lazy reaping), and **direct front-to-back
traversal** (order-based camping, no sentinel/fast-lane) — far simpler. The lock is OFF
the common hot path; if a specific list ever shows up as a tail-latency bottleneck, swap
that list internal for lock-free without touching `Acquire`/`Release` (localized, gated on
measurement). This is essentially `e5b20b0`'s "lock-free hot path, locked steal/destroy",
chosen deliberately over the lock-free redux.
- **2a-i** packed `(held, inUse)` into one 128-bit atomic word (`atomic128`; no GC-shadow
  since both halves are scalars). Both halves are `uint64` amounts — a weighted Resource
  (memory limiter >4 GiB) is representable; weight-1 ops now, the width is headroom. Gated
  CAS transitions keep `0 ≤ inUse ≤ held` atomic. **(Unchanged by the hybrid pivot.)**
- **2a-ii (hybrid)** the acquire up-walk (steps 1–2) stays lock-free, ancestors **pinned
  by refcounts**. Each cache's children and the Pool roots are an intrusive `cacheList`
  (DLL) under a per-list mutex, kept coldest-first by **`touch`** (an acquire up-walk that
  passes a cache *unsatisfied* moves it to the back — O(1) relink under one lock; a
  satisfied hit pays nothing). The **steal** is a front-to-back DFS taking the first
  borrowable cache (the coldest), left in place so a still-borrowable victim is re-picked
  (order-based camping). **Deadlock-free:** the steal is the only op holding two list locks
  at once and always descends root→leaf; every other op (touch/pushBack/remove) takes a
  single list lock, so no cycle can form. `destroy` **unlinks exactly** then CAS-drains
  held to the Resource (`counts.drain` still coordinates with a concurrent `stealOut`).
- **2a-iii** the rdvq wait/wake (unchanged): non-blocking `Acquire` (manager admit) +
  blocking `AcquireWait(ctx)` (executor reacquire — parks, re-searches on each freed
  permit, confirm callback re-runs Acquire after registering = the lost-wakeup guard).
  Every `Release` and the capacity a `destroy` returns wake parked waiters (gated by a
  waiter count so the uncontended release is one atomic load).
- Validated: 50k `rapid` (algorithm, sequential) + `-race` stress ×10 (contended
  inherit/delta/steal, structural churn vs steal, `AcquireWait` liveness) + order-camping /
  `touch`-redirect / exact-removal unit tests; build + `golangci-lint` clean. The
  fully-lock-free `nbcq` forest, sentinel-cycle, lazy reap, and fast-lane are GONE.
- Committed `9af3d13` (includes the `docs/permit-core.md` reconciliation to the hybrid).

**►►► PHASE 2b DESIGN IN PROGRESS (2026-06-27) → `docs/plan/dispatch-execution-split-phase2b.md`.**
The dispatch-side design is being worked out in discussion; the plan doc holds the current
state. Converged so far: **one generic worker pool** (the current `worker.Pool` lifecycle —
demand-spawn + idle-exit + refcount/`Wait` — with a pluggable per-worker loop) instantiated
**twice** — an **executor pool** (runs user bodies, may block; simple `PopFront`→`Run` loop)
and a **scheduler pool** (`workq.Worker`-style over a shared `workq.Accepted`; non-blocking
permit acquire + governor, then hands off). The **scheduler→executor handoff is a NEW
unbuffered rdvq primitive** = `inboxOnlyQueue` + a sender-side `inboxWaiters` (= today's
`rdvq.Queue` minus the whole outbox tier, plus blocking `PushBack`; **no `TryPopFront`**) —
pure rendezvous, zero buffer dwell. The **body→scheduler intake stays the buffered
`Accepted`** (nested submit is non-blocking drop-and-go; postponed/scheduled are necessary
buffering). Key invariants: **LIFO consumer selection is load-bearing for scale-to-zero**
(FIFO would pin the pool); **a buffer-push and a spawn are the same event** (block-as-demand
→ P99 win); **demand fires only for spawn-gap buffering, never for backpressure**
(permit-free/governor-clear *wake* a parked scheduler, never spawn).
**FOREST CONSTRUCTION SETTLED:** one `permits.Cache` per `(wave, limiter)` (`C_W^L`),
**bodies are occupants** (a `Permit`, not a node); the L-forest mirrors wave nesting. At a
wave's first L-admission, **lazily mkdir -p the ancestor L-cache chain** (held=0
pass-throughs up to the nearest existing L-cache or Pool root — *don't* skip non-L
ancestors, else concurrent re-parenting), then acquire into the wave's own `C_W^L`.
Inheritance is **occupy-in-place** (`inUse++` on the ancestor, permit doesn't move);
**suspend = `Release` the body's own permit to its backing cache** (own wave for
checked-out, ancestor for inherited), **reclaim = `AcquireWait` from the wave cache
outward**. Cache refcount is **separate** (self-ref dropped at wave-Done; descendant refs
keep ancestors alive for sub-sub-waves). Multi-limiter: joint acquire in canonical order,
partial-miss → release+postpone. Rejected: per-body nodes+transfer, hoist-on-inherit
(alternation churn), skip-ancestors (re-parenting), Cache-as-single-permit/held-replication.
Pool all `Cache` allocs. Full write-up in the plan doc's "Forest construction" section.
**DRIVE RULE (settled):** a body holds its permit ONLY while running *its own user code*;
it lends for the **whole drive** (incl. running that sub-wave's skim handlers — handlers are
drain) and reacquires only when the drive call returns to its own code — **coarse, per
drive-call, NOT per-handler**. (Fix `permit-core.md`: strike "reacquire before each skim
handler" + the bound's "or a skim handler".) **DRAIN LIMITING (settled, opt-in):** dissolve
intake-vs-drain for limiting — `NewSkimmer(h, WithLimits)` limits a handler;
`NewFunnel(factory, WithLimits, WithFlushLimits)` limits accumulate (intake) and flush
(drain) separately. Default = limiter-free drain (common path unchanged). A limited
handler/flush acquires its OWN limiters (own cache, not the driver's permit) → just another
forest body. Revises "limiters gate intake, not drain"; deadlock-free by the per-limiter
machinery + "can't skim a wave you're part of" — **but MUST be model-checked** (parked
holder whose drain needs a permit, same-limiter-inherit + cross-limiter). Surface change to
ratify (WithLimits on Skimmer, WithFlushLimits on Funnel).
**GOVERNOR PLACEMENT (settled):** the per-wave `Governor`+`downstream` mechanism is
unchanged in purpose; the gate is checked on **both admission paths** (top-level skims if
clogged, scheduler postpones if clogged); `decrementDownstream` relief **wakes the
scheduler, never spawns**. Two retry triggers — permit-free + governor-clear — feed the one
`Accepted` waiter set.
**MIGRATION SEQUENCE (settled, no flag day, full detail in the plan doc):** 0) permit-core
hardening (pool `Cache`, model-check limited-drain + multi-limiter, fix `permit-core.md`);
1) **C1** permit core into the live limiter on the **single pool** (gut, don't remove) —
gated on **`TestBySimulation` reliably green = the deadlock-fix milestone**; 2) **B** the
dispatch infra (unbuffered rdvq primitive + generic-pool refactor), isolated; 3) **C2** the
pool-split cutover — gated on suite + sim + **latency benchmarks (real methodology)** = the
architecture+latency milestone; 4) **C3** drain limiting; 5) **C4** strip the dead eager
code. Key insight: C1 fixes the deadlock and is validated **before** any pool-split risk.
DESIGN COMPLETE — only the inbox-stack lock-freedom is deferred (measurement-gated).
**STEP 0 IN PROGRESS:** the steal now **ref-pins its victim** across `stealOut`
(`tryPin` — a conditional CAS that refuses to resurrect a cache committed to destroy;
`searchList` skips dying caches) — fixing a real near-bug (the victim is cross-subtree,
so only GC kept it alive across the take) and unblocking pooling; **`Cache` is now pooled**
via omnipool (recycled in `destroy`, safe only because of the pin); `permit-core.md`'s
"Driving is an alternation" + the concurrency-bound invariant are corrected. Validated:
`-race` ×10 + 50k rapid + lint. Remaining step-0 model-check items (limited drain,
multi-limiter) fold into C1/C3 where those patterns get wired. **NEXT = C1** (permit core
into the live limiter, single pool). See the plan doc.

**►►► PERMIT CORE SKETCH BUILT + MODEL-CHECKED — `internal/permits` (2026-06-26).**
Phase 1 of the dispatch/execution split: the isolated, model-checked hierarchical permit
cache that both `docs/permit-core.md` and `docs/dispatch-execution-split.md` mandate
building **before any cutover**. Isolated — nothing imports it yet, so zero risk to the
live limiter (the eager `directRequest`/`suspendForEpisode`/`reclaimRequest` in
`limiter.go` stay load-bearing until Phase 3). Four types with a real behavioral split:
`Resource` (pluggable accounting — the only thing that knows capacity; `TryAcquire(n)`/
`Release(n)`, weight-1 for now), `Pool` (the Resource boundary + forest root — the only
place permits cross in/out of the Resource; owns the steal; never caches), `Cache`
(per-unit forest node, cache-don't-return; `Acquire` does steps 1–2, delegates 3–4 to the
Pool), `Permit` (transient run-segment handle, alloc-free). Steal telemetry is structural
sibling-list order (**move-to-back**, no logical clock); `touch` fires only on an
*unsatisfied* pass (a hit pays nothing). Validated: 5 deterministic anchors (incl. the
canonical `limit==1` parked-parent-lends-to-sub-wave hang, dissolved) + 100k `rapid`
adversarial sequences + `-race`; `CheckInvariants` triangulates Σheld across caches / Pool
mirror / Resource in-flight, and an *independent* `HasBorrowable` oracle asserts liveness
vs the guided steal search. This is the structural fix for the pre-existing ~1/120 `-race`
`TestBySimulation` nested-drain hang: a parked holder's permit is idle hence borrowable,
so its sub-wave inherits it instead of livelocking.

  **PHASE 2 ENTRY POINT (next):** map the **manager and executor pools** onto
  `internal/worker.Pool` + `internal/workq.Queue`, with `internal/permits` as the
  foundation — managers admit *non-blocking* (acquire steps 1–4, postpone on miss),
  executors reacquire *blocking* (steps 1–5, wait). **The permit core's one open piece —
  the step-5 wait/wake trigger (event-based: wake when a contended permit frees) — is
  *defined by* those two callers, so co-design it in Phase 2, not standalone.** Place the
  governor's per-wave gate on the manager admission path. Phase 3 then sequences the
  migration off the eager `limiter.go` code (no flag day). Deferred: weighted amounts (the
  `Resource` `n` param already allows it) and cross-limiter joint admission. Spec +
  sequencing: `docs/dispatch-execution-split.md` and `docs/permit-core.md` "Open / next".

  Supporting refactors landed alongside: `ctxMeta.job` retired + the `wv`(`*Wave`)/
  `wk`(Work) naming convention applied package-wide, with redundant/dead params pruned
  across the dispatch + skim paths; `streampool.Wait` now clears the `internal/ctxpool`
  reuse caches after the worker join.

**►►► FUNNEL ENGINE REMOVED — FLUSHES ON THE SHARED POOL (2026-06-24).** The per-Wave
`funnelEngine` + flusher goroutine + `cpworker.go` are gone. `Funnel[T]` is now a plain
value `{wave, factory, limiter, id, instancePool, workPool}` (no inner heap object);
accumulator instances live in a per-Wave `funnelInstances sync.Map` keyed by funnel id.
Deadline flushes ride the global `defaultPool`'s scheduled queue; the end-of-work sweep is
a synchronous **enqueue-only** `wavestate.onFlushing` callback (replacing the `FlushChan`
close) that `ClaimForFlush`+`ForceFresh`es each live instance — the flush itself runs on a
pool worker via the same `funnelInstance.Execute` path as a deadline flush. No-deadline
instances are not scheduled (no 24h placeholder). Recycle rides the pop (rule R2);
`initState` clears the map. **Two spawn regressions found+fixed during verification:**
(1) DECISION B's deadline-parked worker pinned the `spawnConcurrencyLimit` token →
release it at the park point (`workq.WithOnWait`); (2) `ForceFresh`'s `Notify(demand)` was
a no-op with no parked waiter → spawn directly when `Notify` finds none. After both, the
`-race` `TestBySimulation` hang rate is **~1/120 — baseline parity**; the residual is a
**pre-existing** nested-drain deadlock (the dispatch/execution conflation, the split's
domain), not introduced here. Plan + full write-up: `docs/plan/funnel-engine-removal.md`.
Verified: full `./...`, `-race` suite, 120× `-race` `TestBySimulation` (parity),
`reuse_test.go`, alloc tests. (`Example_observable` is independently flaky — known.)

**►►► WAVE-SCOPED FUNNELS, NO EXPLICIT LIFECYCLE LANDED (2026-06-23).** Funnel has no
`Close`/`Dup` and no teardown: `Funnel[T]` is a plain value (no leakguard), and
`AccumulatorFactory` has no `Close` either. The whole mechanism is a contract — the
framework never touches an instance after `Flush`, and flushes every outstanding
instance before the wave drains (the per-instance wave-barrier ref + the end-of-work
flush sweep) — which lets users pool their own state against well-defined lifetimes.
`internal/leakguard` deleted. (An earlier cut, `a2fceba`, kept `factory.Close` +
wave-driven finalization; we then dropped `factory.Close` as unnecessary.) Plan:
`docs/plan/funnel-lifecycle.md`.

**►►► ZERO-VALUE WAVE LANDED (2026-06-23).** The 2026-06-21b Wave lifecycle (below)
is now implemented. `NewWave`/`Cancel`/`CancelAndWait` are gone; a zero-value
`var w streampool.Wave` self-inits on first use (`ensureInit`) and re-arms after a
drain (`ensureArmed`, dispatch-only) so a `*Wave` is reusable/poolable; the Wave owns
no ctx (flusher roots at Background, exits on `state.Done()`); top-level dispatch binds
via `op.In(&w)`; dispatch is unified through `topLevelCtxMeta` (cross-wave = redirect).
Plan + the two bugs found in verification: `docs/plan/zero-value-wave.md`. Verified:
full suite + `-race` suite + `reuse_test.go` + 40× `-race` `TestBySimulation`, all green.
Key subtlety: re-arm must be **dispatch-only** — a `CloseAndSkimAll` drives an empty
wave to Done during `Close`, so if skim re-armed on Done it would block forever.

**►►► B3 CUTOVER INTERMITTENT HANG — ROOT-CAUSED AND FIXED (2026-06-23).**
The ctxpool body+meta cutover (`e740d33`) intermittently wedged a wave at
`stage=Flushing inFlightWork=0 totalRefs=1` — one funnel instance never flushed, so its
per-instance barrier reference never dropped and the wave never reached Done (`SkimAll`
parked on `state.Done()`).
- **Root cause — a flush-signal subscription race in the funnel flusher.** The
  per-wave flusher goroutine read its end-of-work flush channel *inside* the goroutine
  (`worker.nextJobFlushCh = j.state.FlushChan()` at `funnelengine.go:161`). The wave's
  `Closed→Flushing` transition (`wavestate.noMoreWork`) *rotates* that channel — closes
  the old one (the flush signal) and installs a fresh one. When a funnel is created very
  late and its wave reaches Flushing within ~microseconds, the flusher goroutine can be
  scheduled to run its body *after* the rotation, so it subscribes to the **post-rotation**
  channel (never closed again) and parks forever, missing the wave's one end-of-work
  flush signal. The cutover didn't introduce the race but **widened the window**: the
  flusher's startup now does more work (`ensureCtxMeta` via ctxpool, ~29µs in the trace).
- **Proof (execution trace `trace.out`):** Wave `0x3c80014e2708` did its Flushing CAS at
  t=`005605053696`; its flusher `G=25814` didn't begin executing until `005605111488`
  (~58µs later) and ended its entire trace parked in `popSelect` on the post-rotation
  `nextJobFlushCh=0x3c8001c9e070`. Global tally: 4287 Accumulate vs 4286
  "received job flush signal" — exactly one instance's signal lost.
- **Fix:** capture `FlushChan()` **synchronously in `newFunnelEngine`** (which always
  runs during the wave's Open phase, strictly before any Flushing rotation) and pass it
  into the flusher. The pre-rotation channel is exactly the one closed at the first
  Flushing transition, and a closed channel always fires in `select` — so the signal
  can't be missed no matter how late the goroutine is scheduled.
- **Verification:** 300/300 plain + 40/40 `-race` of the reduced zero-delay repro (was
  reliably hanging pre-fix; pre-cutover baseline 0/150), full `./...` suite + linter
  green. All `TEMP B3.hang` diagnostics reverted.

**►►► SURFACE REDESIGN + DOC-ORG TARGET (LOCKED, 2026-06-21, design review w/ PN).**
A deep design pass converged the user-facing `streampool` surface and the target doc
organization. These SUPERSEDE earlier surface notes (incl. the 2026-06-20 "SURFACE
PINNED" line below) where they conflict. Authoritative until reflected into the docs.

**Surface (locked):**
- **Wave construction + lifecycle:** see the **WAVE LIFECYCLE FINALIZED (2026-06-21b)**
  block below — it SUPERSEDES the value-handle / `NewWave` / `Cancel`-`CancelAndWait`
  thinking that earlier versions of this bullet described. Net: **no constructor**
  (zero-value `var w Wave`, `*Wave`, lazy `ensureInit`), **drain-only** lifecycle
  (`Skim`/`SkimAll`/`CloseAndSkimAll` → `ErrWaveDone`; `CloseAndSkimAll` = terminal
  seal+drain, `SkimAll` = drain without sealing), no `Dup`. Surface evolution +
  rationale: `docs/decisions/surface-lineage.md`.
- **Ops are wave-agnostic** — `NewLauncher(h)` / `NewSkimmer(h)` / `NewFunnel(factory)`,
  no construction wave, no sentinel. Reusable specs definable before any wave (no
  wave-lifetime/creation-order coupling).
- **Routing = ambient + `op.In(wave)`.** In-body `op.Submit(ctx, v)` uses the body's
  ambient (framework-stamped) wave; `op.In(wave)` returns a cheap wave-bound value
  handle for top-level, redirect, or bind-once reuse. `In` (membership: the op's work
  is *part of* the wave) chosen over For/To; routing is handle-level so it never
  perturbs the ctx → Flow/trace propagate across redirects untouched.
- **Dispatch verb `Submit` kept** + **naming convention**: name ops as agent/role
  nouns distinct from their outputs — Launcher→`fetcher`; Funnel→`aggregator`
  (+`totals`); Skimmer→`collector` (+`results`). Rule: "-er for the op, plain noun
  for the output." `Start` = void-Launcher sugar. (Considered `Do`/asymmetric verbs;
  the naming convention makes `Submit` read right and keeps the clean
  `SubmitErr`/`SubmitResult` family + `ants`/`pond` familiarity.)
- **Funnel = wave-scoped**: per-(funnel,wave) accumulator instances owned by the wave,
  force-flushed at wave drain (the per-wave flusher). `Flush(ctx)` (outputs → ambient
  wave) + **`FlushTo(ctx, wave)`** (one-shot; outputs → given wave; FlushFn stays
  wave-agnostic, its ambient overridden — enables snapshot/staged capture, e.g.
  timer-driven). **NO Close/Dup** — finalization is wave-driven (in-flight==0 ∧
  sealed), which subsumes the old Funnel.Dup refcount and counts *all* feeders.
- **Limiters minimal**: standalone composable values — `NewSemaphore(n)`,
  `NewRateLimit(n, d)`; `WithLimits(...)` AND-composition, **jointly admitted in a
  global canonical order** (deadlock-free by lock-ordering, automatic, no object).
  **NO user-facing Coordinator/Scheduler, no `.Under`/grouping.** Ordered joint
  admission needs only the global order; the prioritized discipline (the only thing
  needing a central arbiter) is a single *internal* global arbiter, not exposed.
- **Flow kept, value handle**, `ctx, flow := NewFlow(parent)` — two-return (Flow *is*
  ctx-borne propagation, the deliberate exception to "no ctx from constructors").
  **Keeps Dup/Close** — the one legitimate refcount survivor (spans multiple waves; no
  single wave bounds it). Framework auto-ref/unrefs per work item; `FlowFromContext` =
  non-counting view; propagation requires ctx hygiene.
- **Three-type framing fixed**: user-facing types are **Wave + Flow** (+ ops as
  verbs); **Pool is internal** (auto-sized), mentioned only to explain sizing.
- **Deferred (no API named — own design effort)**: a declarative op-and/or-wave
  **scheduling priority** feeding work-dispatch ordering *and* the internal permit
  arbiter; anti-starvation-tempered; intake-side only; one internal global arbiter,
  no user grouping.

**Doc organization (target; strict only on release-able branches — refactor branches
may have docs lead code):**
- User-facing = **current state**: README, `doc.go` (absorbs `programming-model`),
  per-symbol API comments, `example_*_test.go`.
- **`docs/` root** = maintainer, current-state design.
- **`docs/decisions/`** = target-state design + rationale + superseded designs.
- **`docs/plan/`** = path-to-target (migration/sequencing); empty/deleted at rest.
- Positioning: concise comparison in README; deep evidence (ARCHITECTURE_COMPARISON
  source analysis, POSITIONING_RESEARCH) → `docs/decisions/`.
- `API_DESIGN.md` → reborn as the `docs/decisions/` target-surface record;
  `programming-model.md` → folds into `doc.go` (deferred to the code migration).

**►►► WAVE LIFECYCLE FINALIZED (2026-06-21b) — supersedes the Wave bullet above**
(the single-return / value-handle / adopt-parent-from-first-use thinking). Strict
stance: **ctx is DRIVER-SPECIFIC.** Like the internal Pool, a Wave owns NO ctx.
- **No constructor.** Ditch NewWave AND NewChild. `var w streampool.Wave` (zero
  value usable). A sub-wave is just a zero-value Wave first-used inside a body.
- **No Wave Cancel / Wait / CancelAndWait.** The only lifecycle op is the drain:
  `Skim` / `SkimAll` / `CloseAndSkimAll`, returning **ErrWaveDone** when
  in-flight==0 ∧ sealed. `SkimAll(ctx)` IS the structured scope; ctx is the driver's.
- **Cancellation = the drive ctx (pure).** Cancelling the SkimAll ctx → it returns
  ctx.Err(); in-flight work keeps running under its own submit ctxs (cancel those —
  usually the same ctx — to stop it). NO framework force-abort. No goroutine leak:
  workers belong to the GLOBAL pool (not per-wave); `streampool.Wait()` stops idle
  workers and joins them.
- **Lazy init + reuse.** A zero Wave self-inits its substrate on first ctx-bearing
  use (op dispatch into it, or Skim/SkimAll) via a race-safe `ensureInit(ctx)`, and
  is reusable after the drain returns (no explicit teardown call). The creation-time
  `parentJobs`/`shells.Init` stamping (was in NewWave) MOVES to `ensureInit`, keyed
  on the first-use ctx = the driving body's ctx → captures the DRIVING ancestry.
- **Driving ancestry only.** Parentage for limiters (permit inheritance) and the
  "can't skim a wave you're part of" guard is the DRIVING goroutine's ctxMeta
  nesting, captured at drive — never a creation/cancellation parent.
- **Funnel flusher re-homed** to exit on wavestate→Done (in-flight==0 ∧ sealed),
  not a CancelAndWait join.
- Wave stays `*Wave` (no value conversion); bind with `op.In(&w)`. The user owns
  pooling (`sync.Pool[*Wave]` or reuse a var). 3b (single-return NewWave) and 3c
  (value handle) are MOOT.
- **Context mechanism CONVERGED (2026-06-21/22, w/ PN) → `docs/decisions/body-context-pool.md`.**
  How "Wave owns no ctx" actually works. **The model converged on TWO decoupled pools**
  (a deliberate shift away from the single fused `{ctxMeta, childCtx}` unit the note's
  body still describes — reconcile the note on the next doc pass):
  1. **`internal/ctxpool`** reuses the **child `context.Context`** objects: a
     process-wide map of parent ctx → `childPool`, each handing out reusable
     `WithValue`-descendants of the parent (found by direct `ctx.Value`), auto-evicted
     via `AfterFunc` on parent cancel. Children pooled per-`childPool` (`nbcq`); the
     `childPool` struct itself is GC'd, not pooled (cold-path alloc; safe recycle would
     need a hot-path refcount — see the in-code comment).
  2. **A separate `*ctxMeta` pool** (streampool layer, B3) reuses the **values**:
     borrow a meta, stamp it (wave/ctxType/parent/heldRequest/parentJobs via
     `parentJobsFor`), set as the child ctx's value; return to its own pool on `Free`.
  Values pooled INDEPENDENTLY of contexts. Cancellation = pure source-ctx ancestry.
  Borrow → stamp → run → return. Three disciplines: **A** per-execution (async:
  work-item `Free`; inline: scope `defer`), **B** per-drive (skim), **C** per-lifetime
  (flusher). Collapses `waveCtx`/`execShell`/`Cancel`/`CancelAndWait` + the per-wave
  ctxMetaMaps. Plain ownership + GC, no refcount. Borrow-site map (#1–#4) in the note.
  - **IMPL STATUS (2026-06-22):**
    - **`ctxpool` LANDED** (`51554d5`) — generic child-ctx reuse + eviction; tested,
      unadopted. (Caught+fixed a draft bug: embedded zero-value omnipool silently
      defeated ctx reuse → per-`childPool` `nbcq`.)
    - **`bodyCtxPool` DROPPED** (`b7c132d`) — the fused single-pool design (`c159d25`);
      reverted the `ctx`/`pool` fields on `ctxMeta`. Stamp logic (`parentJobsFor`)
      preserved in history at `c159d25` for B3 re-derivation.
    - **`metaFromContext` read-seam LANDED** (`a8f05e8`) + **body-context borrow
      primitive LANDED** (`1a8d404`, `bodyMetaPool`/`borrowBodyContext`, unadopted).
    - **B3 SCOPE DISCOVERY + DESIGN CONVERGED → `docs/decisions/meta-context-migration.md`
      (rev 2, `c7abcdc`).** Migrating bodies to ctxpool is inseparable from migrating the
      meta-derivation machinery (ensureCtxMeta family): it finds source metas via
      ctxMetaValueKey (ctxpool bodies use childKey) and caches by ctx identity (ctxpool
      reuses ctxs). Design CONVERGED (w/ PN) to ONE unified model — **no lifecycle fork**:
      every meta is a ctxpool borrow differing only in hold scope (per-execution bodies /
      **per-call drivers** — NOT singletons; pooled N-at-once under concurrent driving)
      and ctxType. One lookup (`metaFromContext`=`GetValue`); `ctxMetaValueKey`/
      `ctxMetaMap`/`skimCtxMetaMap` all retire; cross-job derivation dissolves into the
      borrow. Rule: **borrow-at-entry, read-while-nested**. Decided: top-level
      Start/Submit drives a backpressure trySkim → **two nested borrows** (top-level ⊃
      skim) + the submitted work's separate body meta. `topLevelExEnv.Lock` removable
      (per-call metas are single-threaded — verify).
    - **CUTOVER LANDED (`e740d33`, 2026-06-23):** B3.meta + B3.A/B in one step —
      `metaFromContext` is ctxpool-aware; task and funnel bodies borrow via
      `borrowBodyContext`; `ensureCtxMeta` stamps a ctxpool child (no `AfterFunc(j.ctx)`).
      The cutover took the incremental path (dual-lookup read seam); the legacy
      `ctxMetaValueKey` read branch + `ctxMetaMap`/`skimCtxMetaMap` have since been
      RETIRED (2026-06-23, with the zero-value Wave cleanup): `metaFromContext` is now
      pure `ctxpool.GetValue`, and `internal/ctxmap` (the maps' backing) is deleted.
    - **POST-CUTOVER HANG FIXED (`00f7abc`, 2026-06-23):** flush-signal subscription race
      in the funnel flusher (see the top banner). 300/300 + 40/40 -race green.
    - **B3.C LANDED (2026-06-23):** runtime force-abort gutting was already in the cutover
      (Wave owns no `waveCtx`/`execShell` pool; `Cancel`/`CancelAndWait` no longer
      force-abort bodies). This pass removed the residue: the dead `worker.Pool.PoolCtx()`
      method (zero callers — it existed only so waves could derive a teardown `waveCtx`),
      the stale `Cancel` doc (it claimed it cancels task contexts), and `execShell`/
      `wave-5b` comment references across pool.go/job.go/ctxmeta.go/ctxpool.go.
    - **B3.D LANDED** (in the zero-value Wave migration, 2026-06-23): examples + sim
      reconciled to the new cancellation model (cancel the submit/drive ctx to stop a
      body; the Wave owns no ctx). The B3.meta unified-model retirement is also DONE
      (`metaFromContext` → pure `GetValue`; `ctxMetaValueKey` + the two maps removed).

**►►► DOC CONSISTENCY SWEEP (in progress, 2026-06-20).** Bringing all docs in line
with the converged target design. Committed so far this session: permit-core.md (new
spec); reconciled dispatch-execution-split.md, limiter-suspend-resume.md,
global-substrate-activation.md (banners), backpressure-and-reentrancy.md,
programming-model.md (reentrancy/scatter rule); trimmed dispatch.go; deleted
REVIEW_FINDINGS.md (code citations repointed to limiter-suspend-resume.md).

Root-doc triage done (no deletions needed beyond REVIEW_FINDINGS): CHANGELOG /
POSITIONING_RESEARCH / ARCHITECTURE_COMPARISON keep-as-is.

**DONE this session (also):** programming-model.md refactored into a focused
streampool guide (732→221 lines); its comparison section relocated into
ARCHITECTURE_COMPARISON.md. README swap done (old README deleted, README-proposed →
README, updated to the pinned surface). API_DESIGN.md reconciled (top banner + inline
fixes: Pool un-exposed, permit cache not global scheduler, reentrancy rule,
principle 7).

**REMAINING (process/roadmap docs — lower stakes):**
- **REFACTOR_PLAN.md** (now `docs/plan/`) — reconciled 2026-06-21: candidate-waves
  marked landed/superseded, the REVERSED Pool↔Wave direction fixed (Pool folded
  INTO Wave, internal), op-names refreshed, and the missing dispatch/execution-split
  + permit-core architecture wave added. Live status still lives in WORKING_NOTES.

**DONE since (this block was itself stale):**
- TODO.md "global permit scheduler" — already reads "no global scheduler" in the
  REFRAMED banner; op-names refreshed; the wave-5b section rewritten to the
  2026-06-21b finalized lifecycle (force-abort design retired).
- ARCHITECTURE_COMPARISON.md / API_DESIGN.md / POSITIONING_RESEARCH.md already live
  under `docs/decisions/`; REFACTOR_PLAN under `docs/plan/`. (The relocated
  comparison may still carry minor generic "scatter-gather-combine" phrasing.)

**SURFACE PINNED this session:** `streampool` package; no user-facing `Pool` (sizing
automatic; concurrency via Limiters); drop scatter/gather/PSG vocabulary; dispatch
verb `Submit`; `Flow` kept as the optional third concept. Go-forward calls made in
the guide/README/API_DESIGN — ratify or correct.

**►►► DEVELOPMENT HISTORY (excised 2026-06-23).** The dated implementation/design
journal that used to live here — the architecture pivot, the `psg.Pool`→`Wave` fold,
rdvq/workq consolidation, limiter suspend/resume, and earlier hang root-causes — now
lives in **`docs/decisions/development-history.md`**. This file keeps current status
only. (Relative "above"/"below" cross-references in the sections below that point into
that journal now resolve in the history doc.)

### Next session pickup

**►►► NEXT = C2 — the pool-split cutover.** Phase 2b migration steps 0/C1/B are landed
(commits `ae6339f`, `551f4e6`, `cfdb039`); the example fix is `565b3b6`. C2 is the big one
and reshapes the live dispatch path. Full design in
`docs/plan/dispatch-execution-split-phase2b.md` ("The pool model", "The queue model",
mode mapping, governor placement, migration sequencing). Shape:
- **Executor pool** = `worker.Core[E]` (from B) + an `rdvq.Handoff[T]` (from B) + a simple
  `PopFront → Run` per-worker loop (no `workq.Worker`, no `TryPopFront`). Its demand is
  **block-as-demand**: a scheduler's `Handoff.PushBack` that finds no parked executor parks
  and triggers `Core.TrySpawn` — wire this `PushBack`-park→spawn hook on the `Handoff`
  (deferred from B; B1 left `Handoff` pure).
- **Scheduler pool** = the existing `worker.Pool` over `workq.Accepted`; per item:
  non-blocking `permits.Cache.Acquire` + governor check, then **blocking `Handoff.PushBack`**
  the admitted body to the executor; postpone on a permit miss. Top-level admission stays
  inline on the driver (skim-retry / block-and-help).
- **Gate:** full suite + `-race` + `TestBySimulation` + the **latency/alloc benchmarks**
  with the REAL methodology (heavy-tailed blocking-I/O work, P99/max, swept P:D ratios —
  see `[[feedback_bench_methodology]]` / BENCHMARKING.md), not a throughput microbench.
- **Recommended:** map it first (like C1/B did) before touching code — it's the
  architecture+latency milestone.

**Deferred backlog (after / alongside C2):**
- **C3 — drain limiting** (`WithLimits` on `NewSkimmer`, `WithFlushLimits` on `NewFunnel`);
  needs the limited-drain model-check (parked holder whose drain needs a permit).
- **C4 — residual cleanup**: any dead `BlockBehavior`/`shouldBlock` plumbing once C2
  reshapes dispatch. (No wake-efficiency item — the `Notifier` single-wake + renotify
  conservation already wakes exactly one consumer, so there's no thundering herd to fix.)
- **Multi-limiter** joint admission (currently panics at `opConfig.singleLimiter`); **weighted
  resources** (re-derive the removed `applicant` sizing natively).
- **Thread C** — `Try*` honoring non-zero non-Forever deadlines via bounded-wait
  (`Forever` sentinel + `dispatch (bool, error)` foundation at `5dc49c7`).
- **psgwf legacy-name retirement**; **bench.txt regeneration + chartgen alignment** (new
  metric names, e.g. `funnelLimit`).

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
