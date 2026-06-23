# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

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
      Note this took the INCREMENTAL path, not the full unified-model retirement: the
      legacy `ctxMetaValueKey` read branch + `ctxMetaMap`/`skimCtxMetaMap` REMAIN (the
      read seam is a dual lookup, ctxpool-first), staged to retire once every write path
      is on ctxpool. `metaFromContext` is not yet pure `GetValue`.
    - **POST-CUTOVER HANG FIXED (`00f7abc`, 2026-06-23):** flush-signal subscription race
      in the funnel flusher (see the top banner). 300/300 + 40/40 -race green.
    - **B3.C LANDED (2026-06-23):** runtime force-abort gutting was already in the cutover
      (Wave owns no `waveCtx`/`execShell` pool; `Cancel`/`CancelAndWait` no longer
      force-abort bodies). This pass removed the residue: the dead `worker.Pool.PoolCtx()`
      method (zero callers — it existed only so waves could derive a teardown `waveCtx`),
      the stale `Cancel` doc (it claimed it cancels task contexts), and `execShell`/
      `wave-5b` comment references across pool.go/job.go/ctxmeta.go/ctxpool.go.
    - **NEXT — B3.D:** reconcile examples + sim to the new cancellation model (cancel the
      submit ctx to stop a body; `Cancel` only tears down wave machinery). Later/optional:
      the full B3.meta unified-model retirement (`metaFromContext`→pure `GetValue`, drop
      `ctxMetaValueKey` + the two maps).

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

### Next session pickup (in rough priority order)

1. **Pool / workq consolidation** — see "Pool consolidation — foundational
   analysis (2026-06-06)" above. Checkpoint 1 progress: **1a, 1b-i, 1b-ii,
   rename, and 1c-i (per-instance barrier ref) are DONE** (committed). 1c-ii
   foundation (`6df4218`) + `delayUntil→at`/`timed→scheduled` rename (`2759116`)
   committed. **NEXT = finish 1c-ii per the "1c-ii CONSOLIDATED DESIGN
   (2026-06-07)" section above** — the design that fixes the **pre-existing
   deadlock** (bisected to `63a4d57`, a test-only sim commit) via three
   orthogonal concerns: heap-position under `delayq.mu`; atomic `admitted`
   (+ `Schedule(w,0)` indefinite, reversing the zero-`at` panic); per-instance
   lifetime ref with op-liveness-dropped-at-flush + `funnelOp.unref` draining
   `instanceQueue`; then live-set/`forceAll`; then `accumulate()`/`Dispatch`
   renames. **CAUTION:** the full `-race` sim was never reliably green
   (intermittent pre-existing hang); it becomes the gate only after the
   deadlock fix. Uncommitted WIP in the tree (`ScheduledWorkItem` embed +
   `flushAll` orphan-hang fix) folds into sub-steps 1 and 5.
   After 1c: checkpoint 2 (delete funnel `maxConcurrency` → demand-driven) then
   3/4/5.
2. **Thread C completion** — Try* honoring non-zero non-Forever deadlines via bounded-wait. Falls out of the consolidation; pick up the `Forever` sentinel and `dispatch (bool, error)` foundation from `5dc49c7`.
3. **psgwf legacy-name retirement** — `psgwf.GenericTaskRunner` and friends still use pre-rename vocabulary. Done as a stand-alone pass or rolled into a broader psgwf migration.
4. **bench.txt regeneration + chartgen alignment** — re-run benchmarks under the new metric names (`funnelLimit` instead of `combinerLimit`), then update chartgen to parse the new names. Required before the legacy bench file can come back online for chart generation.
5. **CombinerPool retirement** — once Pool consolidation lands, the CombinerPool→FunnelPool transitional name can go away. Stand-alone follow-up if not folded into the consolidation pass.

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
