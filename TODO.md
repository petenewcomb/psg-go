# TODO

---

# Phase 3 cleanup audit (2026-07-13) — CURRENT

Compiled from a four-dimension smell audit (naming, public-API consistency, test
structure/coverage, design/vestigial/doc-gaps) run after the DEVELOPMENT.md guideline
tightening + repo-wide comment sweep (commits `5c545be`, `cc43813`). Effort S/M/L; risk
low/med/high. Items tagged `[tracked]` restate/supersede an item in the historical
sections below. Everything below this block predates the combiner reshape and is
partially stale — treat as historical reference.

## Sequencing (decided 2026-07-13, w/ PN)

Order: **Tier 3 → Tier 4 → Tier 5 → D3 → Tier 6**, then (later) **D2, Tier 1, Tier 2**.
D1 is DECIDED (below) and folds into Tier 4. Tier 2 is deferred and reframed — see its note.

### Progress (2026-07-13)
- **Tier 4 doc reconciliation (incl. D1): DONE** (commit `53103a6`) — doc.go/pin/hold/
  wavepermits/wave/funnel/accumulator/sim godoc moved to the shipped surface. Label-anchoring
  sub-item: filed (needs PN — homeless labels, see below). Design-doc (.md) reconciliation
  deferred to the tracked post-consolidation pass.
- **Tier 3 op-receiver rename: DONE** (commit `f5cd6a3`) — g/c/r → s/f/lc (+ funnelInstance
  `fi`, errAccumulator `a`), via gopls.
- **Tier 3 trace-region label fix: DONE** — `funnelInstance.funnel` → `.accumulate`.
- **Tier 3 "Task noun" rename: BLOCKED on D4 (below)** — attempted taskContext→launcherContext
  and ErrTaskPanicked→ErrLauncherPanicked, then REVERTED: `taskContext`'s stringer feeds a
  user-facing panic message ("Start called from task context…", task.go:38), and "Task" is
  LIVE public vocabulary — so this is a UX/vocabulary decision, not a mechanical rename.
- **Remaining Tier 3 (needs naming targets confirmed before mass-rename):** "Scatter"→PostWork;
  the Accumulator Func-naming (exported, exact target names); opaque locals (hbc/protoBB/fn/
  poolWork); D4. **Tier 5 (tests): not started.**

- **D4 — "Task noun" internal vocabulary.** `Task`/`TaskLauncher`/`TaskFunc`/`NewTask*` are
  live, deliberate PUBLIC API, so "task" is NOT a retired name like Gather/Combiner/Runner.
  But internally `taskContext`, `ErrTaskPanicked`, `taskWork`, `taskPostWork`, `boundTask`
  mix "task" with "launcher". `taskContext.String()` and `ErrTaskPanicked` surface in
  user-facing panic/error text ("task context", "task panicked"). **Decide:** keep "task" as
  the internal body-noun (it aligns with the public Task concept), or align internals to
  "launcher" (changes the two user-facing strings, needs the sibling-symmetry with
  skim/funnelContext). PN's call — affects user-visible wording. Effort M, risk med. NEW.

## Decisions

- **D1 — Reconcile the wave-agnostic / zero-value-Wave model with the shipped surface.**
  **[DECIDED 2026-07-13: the shipped code IS the intended target — `NewWave()` is required
  (a zero-value `Wave{}` has a nil handle and every method short-circuits to `ErrWaveDone`),
  `In` is by-value (`op.In(w)`), and the two-op-category split (Launcher/Skimmer
  wave-agnostic; Funnel/Resequencer wave-bound at construction) is intended. Fix the DOCS,
  not the code. Folded into Tier 4.]**
  `doc.go:9-36` (and `pin.go:32`, `hold.go:26`, `wavepermits.go:49`) advertise the
  `docs/plan/zero-value-wave.md` target — "zero-value Wave, no constructor, bind with
  `op.In(&w)`, no `NewWave`" — which that plan marks **LANDED 2026-06-23**. The code
  diverges: `type Wave struct{}` with `func NewWave() Wave` (`wave.go:47`) still present
  and used everywhere, and `In(wave Wave)` is **by value** (`launcher.go:119`,
  `skimmer.go:56`), so the `op.In(&w)` examples do not compile. Separately, the "ops
  carry no wave at construction / wave-agnostic op" claim (`doc.go:25,32`) holds only for
  Launcher/Skimmer — Funnel, Resequencer, RangeResequencer bind their wave at
  construction and have no `In()`. **Decide:** re-land the zero-value/pointer-`In` target
  and make all ops wave-agnostic, OR reconcile docs to the shipped `NewWave()` / value-`In`
  / two-op-categories reality. Gates most of Tier 6 and the doc reconciliation. Effort L,
  risk med. NEW.

- **D2 — [DEFERRED 2026-07-13: dig in later, after Tiers 3-6.] Extract the internal packages the internal-tests are signaling** (the
  DEVELOPMENT.md "complex internals → extract a capability" principle). The 7 in-package
  test files sort as: `internal/wavestate/inflight_internal_test.go` = trivial black-box
  flip (all methods already exported); `limiterset_internal_test.go` = small
  `export_test.go` shim; the rest are extraction candidates — `limiter` +
  `weightedlimiter` reach a shared hidden layer (the `semaphoreResource` /
  `weightedSemaphoreResource` permit adapters + `Overdraft` + raw `maxConcurrency`) that
  wants to become a **permits-backed semaphore package**, leaving `streampool.Limiter` a
  thin typed front; `bodyctx` + `ctxmeta` reach a **context / permit-scope subsystem**;
  `flow` reaches a **coalescing union-find with refcounted shared nodes**. Two audits
  reached the extraction conclusion independently. **Decide:** which extractions to do and
  in what order (limiter-family is the cleanest first). Effort L, risk med. `[tracked]`
  (WORKING_NOTES phase-3 backlog item 1).

- **D3 — [SEQUENCED 2026-07-13: after Tier 5, before Tier 6.] Funnel multi-limiter policy.** `Funnel.WithLimits` panics on more than one
  limiter (`funnel.go:131`, "Only a single limiter is supported") while `Launcher.WithLimits`
  AND-composes N deadlock-free; a funnel body takes weight-1 permits so joint composition
  is mechanically identical. **Decide:** unify (Funnel AND-composes like Launcher, add
  `Funnel.WithLimiterSet`) or keep single and document why. Effort M, risk med. NEW /
  partially `[tracked]` (§5 per-op combiner limits).

## Tier 1 — Pool/workq consolidation (highest leverage; already the branch's next major piece)

One pass retires the most findings. Confirmed scope from the audit:
- `taskPostWork.Execute` (`wave.go:910`) and `funnelPostWork.Execute` (`funnel.go:1036`)
  are now near-identical (differ only by the funnel permit gate + Waiting/releasePermit);
  `skimPostWork.Execute` (`wave.go:691`) is a third variant; none use
  `workq.ExecuteOrWait`. Extract a shared executor-handoff helper. Effort M, risk med.
  `[tracked]` (§Implementation improvements, lines 153/170).
- Vestigial params fall out here: `boundTask.Execute` passes `group` that the sole
  implementor discards (`launcher.go:353` `_ = group`, and it already stores `wk.group`);
  `newTaskPostWork` accepts a `deadline` it never stores (`wave.go:990`). Effort S each.
  (deadline `[tracked]` line 151.)
- File-org sprawl this pass can resolve: `wave.go` (~1056 lines: Wave API + waveImpl +
  taskWork + skim/taskPostWork + poolWork), `funnel.go` (~1118), `ctxmeta.go` (712, mixes
  ctxMeta with an unrelated `executionEnvironment` family → `exenv.go`). Effort M.
  `[tracked]` line 160.
- Fold in the low-risk correctness items: `ctx.Err()` → `context.Cause(ctx)` at
  `wave.go:667`, `workq/accepted.go:314,330,358`, `rdvq/waiters.go:31`, `rdvq/queue.go:102`,
  `rdvq/handoff.go:140` `[tracked]` line 172; `wavestate/inflight.go` `atomic.Int64` →
  `Int32` `[tracked]` line 182.

## Tier 2 — Dead-code disposition pass (deferred to run with D2 / Tier 1)

**REFRAMED 2026-07-13 (PN): "currently dead" does NOT mean "drop."** Each item below needs a
per-case verdict — some are genuinely deletable gut-to-no-op leftovers, but others may be
reserved API to WIRE UP, or a signal that a caller is missing. Do NOT bulk-delete; triage
each. Deferred to run alongside D2 / the Tier 1 consolidation (which will clarify what's
truly orphaned vs pending-integration). Candidates to triage (verify with a deadcode pass):
- `internal/ttrk` — entire package, zero importers. Effort S. NEW.
- `internal/execpool/executor.go:85-86` — `Acquire`/`Release` one-line delegators, zero
  callers. Effort S. NEW.
- `internal/trace/trace.go:89,116,159,168` — `Log`/`LongLogf`/`NewTask`/`Task.End`, no
  callers outside the package (code uses StartRegion/Logf). Effort S. NEW (decide: delete
  vs reserved API).
- `internal/omnipool/struct.go:181` — free `Clone`, unreachable. Effort S. NEW.
- `funnel_legacy_bench_test.go` — does not compile even under its `psg_wave3_legacy_bench`
  tag (`streampool.Task[T]` no longer exists). Port to current API or delete file+tag.
  Effort S. `[tracked]` (lines 140-147). NB the tag name itself is retired terminology.
- `internal/sim/run.go:585-586,612,643`, `plan.go:361` — dead ignored locals/params.
  Effort S, low value (test infra). NEW.

## Tier 3 — Finish the op-trio rename (internal terminology retirement)

The public rename (Gather/Combiner/TaskRunner → Skim/Funnel/Launcher; job → wave) never
reached the internals. Biggest single naming smell:
- **Receiver letters re-encode the retired names**: every Skimmer method uses `g` (Gather),
  every Funnel `c` (Combiner), every Launcher `r` (Runner) — root + `internal/benchapp` +
  `internal/sim`. Rename to `s`/`f`/`l`. Effort M, risk low. NEW.
- **"Scatter" is the internal verb for the public "Submit/dispatch"** and splits the
  decorator family: `launcherScatterWork`/`limiterScatterWork`/`newScatterWork`
  (`launcher.go:288,423`, `limiter.go:157`) vs the sibling `*PostWork` (funnel/skim/task).
  Unify on `*PostWork`. Effort M, risk med. NEW.
- **Accumulator adapter family is incoherent**: `FuncAccumulator*` prefix (`accumulator.go:38,97,150,190`)
  fights the codebase-wide `*Func` suffix (HandlerFunc/TaskFunc/...); `AccumulatorFactoryFunc`
  vs `FuncAccumulatorFactory` are two names for one idea 7 lines apart. Effort M, risk med. NEW.
- **"Task" half-retired body noun breaks trio symmetry**: `ErrTaskPanicked` (vs
  `ErrFunnelPanicked`, `errs.go:8`); enum `taskContext` (vs `skimContext`/`funnelContext`,
  `ctxmeta.go:26`); launcher pipeline mixes `newTask`→`launcherWork`→`taskPostWork`. Effort
  S-M, risk med. NEW.
- Comment/trace leftovers: "gather"=Skim in `ctxmeta.go:210-222` comments; `funnelEngine`
  (removed) in `accumulator.go:61` and public godoc; mislabeled trace region
  `"funnelInstance.funnel"` inside `accumulate` (`funnel.go:594`); `funnelWork.Funnel(ctx)`
  method reads as verb-as-noun. Effort S. NEW.
- Opaque locals/fields: `hbc`→`inst` (`funnel.go:870`), `protoBB`→`blockBehavior`
  (`wave.go:150`), `fn Funnel[T]`→`funnel` (`funnel.go:785`), `poolWork`→`baseWork`
  (`wave.go:959`, "pool" is loaded). Effort S. NEW.
- **Decide fold-in of the already-tracked renames**: `ErrJobDone`→`ErrWaveDone` (line 114),
  `permits`→`pforest` (line 119), `Free`→`Recycle` + Recycler interface (lines 166-167).
  These are the same "finish the rename" theme — do them in this pass or keep separate?
- `internal/sim` is a self-contained pre-rename vocabulary island (subjob/StartTask/scatter).
  Effort L. `[tracked]` (lines 41-43, 156 sim rationalization).

## Tier 4 — Doc reconciliation + label anchoring

- Reconcile `doc.go` + `pin.go`/`hold.go`/`wavepermits.go` to the shipped API (blocked on
  D1): the `op.In(&w)` examples don't compile; "there is no constructor" contradicts
  `NewWave`. Effort M once D1 is decided. NEW (generic reconcile `[tracked]` line 245).
- Public godoc leaks internal vocabulary/mechanics: `waveImpl` doc-links on Wave methods,
  refcount/generation language on `NewWave`, "funnelEngine drain" on Accumulator,
  phantom `[Funnel.Start]`/"Start or TryStart" references on Funnel (`funnel.go:21,29` —
  methods that don't exist). Effort S. NEW / `[tracked]` line 245.
- `internal/sim/doc.go:16` advertises a "probabilistic mode" (per-invocation draws, Max
  bounds) that the runtime doesn't implement — `drawDuration`/`rollProb`/`shouldReturnError`
  are constant/threshold stubs (`run.go:750-764`). Drop the prose or implement. Effort S. NEW.
- `docs/decisions/backpressure-and-reentrancy.md:512` documents an `Accepted{deferred,
  upstream}` struct that no longer matches `accepted.go`. Effort S. `[tracked]` line 243.
- **Label-anchoring pass** (needs PN — requires knowing what each label denotes; NOT
  auto-fixed to avoid fabricating citations). Investigated 2026-07-13:
  - **Genuinely homeless (defined in no doc — write a definition or a labels glossary):**
    "Design B" (used in workq comments; the one docs hit, limiter-resource-classes.md:215,
    only *references* "Design B precedent of the queue owning its deadline timer" — never
    defines it); "resolution (c)" (permithandle/permits, no docs hit); "CP-B1b"
    (`pool.go:29`, no docs hit).
  - **Have a home doc but not cited beside the use (mechanical once confirmed):** "CP-F7",
    "Phase 2b", "Decision 1-4" (weighted-acquisition et al.). Add an inline `(see <doc>)`.
  Effort M, risk low. NEW.

## Tier 5 — Test coverage gaps

- `AccumulatorFactory.Close` firing at funnel refcount zero — **no test** (an orphaned
  comment describing one was deleted from funnel_test.go). Easiest win. Effort S.
  `[tracked]` (WORKING_NOTES backlog).
- Goroutine-leak harness — no `goleak` (or NumGoroutine-delta) anywhere; add to TestMain for
  root + key internal pkgs, gate drain/cancel/shutdown. Effort M. `[tracked]` line 206.
- Wave-level scale-to-zero — only the executor pool is covered
  (`internal/execpool/pool_test.go:102`); no Wave/global-worker/funnel-pool test. Effort M.
  `[tracked]` line 204.
- Panic-through-framework — no test drives a user panic through dispatch; assert it
  propagates verbatim, accounting stays sound, and a user-body `recover()` leaves the wave
  usable. Effort M. `[tracked]` line 207.
- Shutdown-sequence / resource-cleanup asserts (pins/permits/contexts released) after cancel
  and after normal drain. Effort M. `[tracked]` line 209.
- Optional: a `limit ∈ {1,2,3}` deadlock sweep over a fan-out/nested-drain shape as an
  explicit unit test (today only the sim exercises this systematically). Effort S. NEW.
- Test-hygiene: sleep-based synchronization in `limiter_internal_test.go:98,154` (replace
  with an observable parked-count signal); the ~11k-line skipped golden fixture in
  `internal/sim/plan_test.go` (regenerate against current Plan vocab or assert structural
  properties instead). Effort S/M. NEW (fixture `[tracked]` sim rationalization).

## Tier 6 — API completeness (mostly gated on D1/D3)

- Complete the Fn/Err/Task constructor matrix + void-T aliases: it is complete only for
  Launcher — no `NewTaskSkimmer`/`NewTaskFunnel`, no `Err*`/`Try*`/`SubmitErr` on
  Resequencer/RangeResequencer (which expose only Submit + SubmitResult). Consider a
  table-driven/generated per-op surface. Effort M, risk low. NEW.
- Weighted-limiter surface: a paused (ceiling-0) `NewWeightedSemaphore` is constructible but
  has no exported raise; `SetMaxConcurrency` is a panicking free function keyed to plain
  Semaphore only. Give WeightedLimiter a symmetric adjuster; prefer a typed handle over the
  panicking free function. Effort M. `[tracked]` (WORKING_NOTES step-4 typed Semaphore handle).
- Thread-safety contracts: the task-context dispatch-prohibition caveat lives only on
  `Launcher.Submit` (add to Skimmer/Funnel Submit or state "safe here"); `Wave` has no
  type-level thread-safety statement though every op does. Effort S. NEW / `[tracked]` line 135.
- Funnel receiver shape: `*Funnel[T]` pointer receivers on Submit/TrySubmit (no field
  mutation forces it) vs value receivers on Launcher/Skimmer — imposes addressability on
  callers. Convert to value. Effort S, risk low. NEW.
- `Forever` docstring (`forever.go:8`) lists TrySubmitResult twice and names only TryStart;
  generalize to "all Try* methods". Effort S. NEW.
- Label `streamgrpc`/`streamhttp` as example packages so their app-shaped surface isn't read
  as a canonical wrapper API. Effort S. NEW.

---

## Pre-existing nested-drain `-race` hang (~1/120) — needs the dispatch/execution split (2026-06-24)

`TestBySimulation -race` wedges intermittently (~1 in 120 runs at default config) in a
nested-drain deadlock: pool workers all parked inside blocking bodies (nested
`CloseAndSkimAll`, or a funnel accumulate blocked in the wave governor while holding the
instance mutex), so the innermost subwave's relief path (a flush or skim) starves of
workers. **This is pre-existing** — confirmed at the same rate on the pre-`combiner`
baseline (whose dump shows `funnelEngine.flusher`/`cpWorker` + nested `CloseAndSkimAll`).
It is the dispatch/execution **conflation** (a pool worker both dispatches and runs
blocking user code), which `docs/dispatch-execution-split.md`'s manager/executor split is
designed to eliminate. Not introduced by the funnel-engine removal (that cut is at
baseline parity after fixing two spawn regressions; see `docs/plan/funnel-engine-removal.md`).
Resolve as part of the executor/manager split.

## streampool.Wait should clear ctxpool caches (2026-06-23)

`streampool.Wait()` joins the global pool's worker goroutines on definitive teardown.
After that join it should also clear all cached child contexts in `internal/ctxpool`
(the process-wide parent-ctx→childPool map): with no live workers there are no
borrowers, so the reuse caches only pin contexts (and their values) until their parent
ctx is GC'd via AfterFunc. Needs a `ctxpool` "clear all" entry point (it currently only
evicts per-parent on AfterFunc) wired into `Wait` after the worker join.

## Sim generalization / rationalization (post-B3-hang, 2026-06-23)

Surfaced while hunting the B3 cutover hang (`internal/sim`): the harness is hard to
*target* — fine for broad random coverage, awkward for reproducing a specific scenario.
After the hang is fixed, generalize:

- **Op-mix / topology controls.** Generation is `Count`/`MaxDepth` + structural DAG
  wiring only (funnels→shallower sinks, fan-out from bodies). No knob to dial the op
  *mix* or force a specific shape (e.g. a funnel under a subjob, a particular fan-out).
- **No cross-wave op submits.** Subjobs only inherit limiters (`run.go` `ensurePools`);
  the generator never exercises child→parent or parent→child op submits, though the API
  supports it (ParentExposedOps). Coverage gap *and* a missing control.
- **Deterministic hand-built `Plan` path.** Everything routes through `rapid` + the
  generator; add a clean "construct a fixed `Plan` literal and run it via `sim.Run`"
  entry point for repro/debugging (would have saved much of the hang hunt).
- **Stale bits from the rename/cutover:** `extract-sim-*.sh` markers (see the
  sim-trace-debugging skill — they grep pre-rename strings); commented diagnostic knobs
  in `simulation_test.go` use stale names (`planConfig.streampool.Task...`).

## Refactor status (2026-06)

The combiner branch has progressed substantially beyond its original scope. See `WORKING_NOTES.md` for the live status of the in-flight reshape. Highlights of what's complete on the branch as of `aab904c`:

- Thread A: Handler[T] unification + op trio rename (`Gather`→`Skim`, `Combiner`→`Funnel`, `TaskRunner`→`Launcher`) + Launcher arity collapse + Submit/SubmitErr/SubmitResult dispatch family.
- Thread B: wave-at-construction with nil-sentinel resolution + worker plumbing so nil-wave ops dispatch correctly from inside any op body + factory pass through psgwf / otpsg wrappers.
- psgfn folded into top-level psg; AccumulatorFactory is an interface with `Close() error`; full convenience constructor surface (`NewFn*` / `NewTask*` / `NewErr*`) per op; void-T type aliases.
- Thread C v0.1: `Forever` sentinel + dispatch `(bool, error)` refactor. Full Try* honoring deadlines deferred pending Pool/workq consolidation.

The next major piece is **Pool/workq consolidation** (merge TaskPool + FunnelPool into one Pool, rationalize workq integration). Thread C completion falls out of that pass.

### wave-5b: per-wave cancellation/teardown — SUPERSEDED by the 2026-06-21b Wave lifecycle (see WORKING_NOTES top banner)
- **The framework-force-abort design is retired.** The original plan here (Wave
  holds `waveCtx = WithCancel(poolCtx)` + an **nbcq pool of per-execution
  contexts**; workers run user code under a borrowed wave-exec ctx so a cancel
  reaches permit-holding producers) is rejected by the finalized lifecycle:
  **cancellation = the drive ctx, pure — NO framework force-abort.** Its premise
  is also now false — workers belong to the GLOBAL pool, not a per-wave Pool
  (Pool was folded into Wave, `5bf2e7c`), so the "cancelled subwave can't reach a
  producer in another pool → teardown deadlock" path no longer exists. In-flight
  work runs under its own submit ctxs; cancel those (usually the same ctx) to
  stop it. No goroutine leak: `streampool.Wait()` joins idle global workers. The
  reclaim "never abandon / no unpermitted resume" correctness moves to
  **permit-core** (`docs/permit-core.md`, thread C2).
- **Still a live regression target:** the committed sim coverage
  (`Subjob.CancelProb` + shared limiters) that reproduced the old teardown
  deadlock + reclaim over-admission must stay green through the lifecycle (B) and
  permit-core (C2) cutovers.
- **Trim excessive build-up of nbcq reuse caches.** (Independent of the
  cancellation design.) The `instanceQueue` (`funnel.go:318`, an
  `nbcq.Queue[*funnelInstance[T]]` of spent flush shells) leaves a high-water
  pool of idle shells after peak-concurrency bursts; permit-core's per-pool
  permit caches have the same shape. Worth a shared trim/cap mechanism over the
  nbcq reuse-cache pattern rather than bespoke ones.
- **Revisit the joined-context adapter when tackling Flows.** RESOLVED (2026-07-03):
  the converged flow design (`docs/decisions/flow-design.md`) derives no cancellation
  from flow values and never merges cancellation scopes — a request ctx enters as a
  consultative flow *value*, never a parent of framework ctx derivation — so no
  adapter is needed. (Original question: a framework-native alloc-free / mutex-free
  `AfterFunc`-equivalent hook would only have been needed if Flows had to merge two
  *genuinely independent* (non-ancestor) cancellation scopes.)

The sections below are the original pre-refactor TODO. Many items are now stale or superseded; treat them as historical reference and consult WORKING_NOTES + CHANGELOG for current scope.

## Limiter / livelock investigation follow-ups (2026-06-14)

> **REFRAMED (2026-06-20) — see `docs/dispatch-execution-split.md` + WORKING_NOTES.**
> The limiter-livelock approach pivoted: the partial spawn-token fix (`6d77ce2`)
> closed the dominant case, and the recurring deadlock *class* is now targeted by
> the dispatch/execution split + the hierarchical permit cache (eager suspend/reclaim
> → cache-and-steal; deadlock-free per-limiter, no global scheduler — see
> `docs/permit-core.md`). The `acquireOrWait` block-loop follow-up below is in code
> that redesign retires; the `ErrJobDone`→`ErrWaveDone` rename stands regardless.

- **`acquireOrWait` error-path latent permit leak (defensive — NOT the proven
  livelock cause).** In `acquireOrWait`'s block loop, `if err != nil { return
  false, err }` discards a permit that `confirmFn` may have already latched
  (`b.held`). If `blockFn` (`Pool.block`) ever returns a real error
  (`ErrJobDone` / ctx cancel) coincident with a `confirmFn` grant, the caller
  (`limiterScatterWork` / `funnelWork`) returns on the error without running the
  gated work and is requeued (not freed), so the handle's `Free` release
  backstop never runs and the HELD permit leaks. **This is NOT what fails the
  `TestBySimulation -race` gate** — disproven 2026-06-14: a log-only variant
  (postpone-on-latched-error disabled, branch instrumented) hung 4× with
  **zero** occurrences of the latched-error branch, so it never fires in the
  repro. But it's a real latent hazard; close it defensively (postpone the
  latched permit before returning the error) once the actual livelock fix
  lands. See WORKING_NOTES for live root-cause status.

- **Rename `ErrJobDone` → `ErrWaveDone`.** Legacy "Job" vocabulary; the
  user-facing sink is now a Wave. ~12 usages (errs.go, funnelpool.go,
  limiter.go, job.go doc comment). Fold into the broader Job→Pool/Wave naming
  reconciliation with the other deferred combiner-era renames.

- **Rename the `permits` package → something like `pforest` (PN, 2026-07-10).**
  The package is the permit-cache forest, not the permits themselves; fold into
  the same naming-reconciliation pass as the item above.

## Combiner Branch Pre-Merge Tasks (original list — partially stale)

Items to complete before merging to main branch.

### 3. Documentation updates
- Complete review and update of doc comments for all new/modified public APIs
- Update README with information about the new combining architecture
- Add a Combiner example to the README Features section
- Create a playground example for the new combining architecture
- Ensure that examples do not use any internal packages (e.g. exmpclk)

### 5. API finalization
- Review and document thread-safety guarantees for remaining public APIs
- Add a way to force creation of a new work group
- Maybe remove psg prefixes from psg-go subfolders, but leave the prefixes in the package names?
- should combiner concurrency limits be specified per-combineop instead of or in addition to the combiner pool?

### Wave 3 follow-ups
- **Migrate the combiner throughput benchmark to the Wave 3 API.** The
  pre-Wave-3 benchmark in `combiner_test.go` was lifted out to
  `combiner_legacy_bench_test.go` behind the `psg_wave3_legacy_bench`
  build tag because the value-returning Task shape is gone. Per
  REFACTOR_PLAN.md (combiner-benchmark requirements session), this
  needs a dedicated design pass — what metrics we still want to track
  in the new model — before being brought back online.

### 6. Implementation improvements
- **Change `rdvq.RenotifyFunc` to a `Renotifier` interface** so the infrastructure can free pooled renotifier objects in all cases (not just when invoked). The remaining workaround is in `wrappedRenotify` (rdvq/notifier.go) which self-frees inside its renotify callback — works on invocation, leaks on replacement/discard. Less urgent now that the orphan task queue and `orphanedTaskRenotify` are gone. See WORKING_NOTES "Renotifier lifecycle". Files: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.
- **Add deadline field to `taskPostWork`** and use it in the blocking post path. Currently `newTaskPostWork()` receives the parameter but doesn't store or use it; sibling scatter work types do. See WORKING_NOTES "Deadline propagation in taskPostWork".
- **Make sure that calls to Gosched are interleaved with checks for deadline/cancellation**
- **Consider refactoring `taskPostWork.Execute()` to reduce duplication with `workq.ExecuteOrWait`** — about 80 lines of similar try/subscribe/block logic. Has unique requirements (custom TryPushBack, demand-registration side effects, blocking via PushBackFunc + BasicPushSelect) so not trivial. Evaluate if a `TryPostBehavior` abstraction is worth the complexity. See WORKING_NOTES "ExecuteOrWait duplication".
- Improve detection of top-level vs. child tasks to prevent adding new top-level tasks after Close() (use ctxMeta to allow new scatters only to finish workflows already started)
- Rebuild otpsg module as v2 on flows: span carried as a FlowKey value, span.End() as a follow-up firing at the flow's true end; delete propagation.go/tracing.go (subsumed), keep metrics.go/logging.go as op-instrumentation (CP-R7)
- consider removing combiner goroutines' doneCh and dedicated goroutine now that select on it happens only in the slow path
- profile (memory, cpu, blocking) again after all the recent refactoring, see if there are any more obvious targets or low-hanging fruit
- review again for readability
- make sure all exported functions emit trace regions
- reorganize code within large files like job.go
- re-review tracing guidelines in DEVELOPMENT.md
- figure out what to do about trace.IsEnabled everywhere (if, how)
- review and understand processing and waiting aggregation throughput and speedup graphs
- enable cyclo and fix issues
- can we integrate taskWork into combineTask and gatherTask?
- rename Free to Recycle, add Recycler interface from which other things can derive
- Expose all user code integration points as interfaces with Recycle (Recycler), then add convenience functions that use pooled objects to wrap implementation-by-closure; perhaps reserve psgfn for the convenience functions and add a separate package for the integration interfaces?
- Make sure job.governor is really necessary
- Add Deadline to workq.Execution and make sure that it's set and respected everywhere, especially when blocking
- Abstract logic in *PostWork and perhaps make it extend from workq.ExecuteOrWait?
- Consider an addition to omnipool to codify the pattern in which a monotonic ID field is used to guard against reuse of a object that has already been pooled.  it's a form of weak reference.
- Always return context.Cause(ctx) instead of ctx.Err()

## Post-Merge Enhancements

Items that can be deferred to GitHub issues after the combiner branch is merged.

### Implementation improvements
- fix LockAndSetQueueFunc ugliness
- fix addWork ugliness
- fix inconsistencies between refcounting (and pooling) implementations: semantics re locking, naming, etc.
- consider whether any atomic.Int64s should instead be atomic.Int32 (e.g. InFlightCounter, concurrency tracking in sim/run.go)
- consider whether to use hierarchical timing wheels to avoid O(log n) heap overhead of go-native Timers, esp. for Flush.

### Performance Optimizations
- Explore operation affinity for worker goroutines to improve cache-line efficiency (generalizes the older "combiner goroutine ↔ combiner instance affinity" idea to all op types). Motivation sharpened by the ultrapool analysis (ARCHITECTURE_COMPARISON.md §6): its random shard pick deliberately trades cache locality for load spreading; psg could plausibly get both. Today rdvq's LIFO inbox stack gives *worker-temporal* locality (the most recently active worker picks up the next item) but is op-blind — work from different ops interleaves through the same queues. The affinity key already exists: every `workq.Work` carries a `GroupID` (`internal/workq/work.go`), and requeue ordering already compares groups (`accepted.go`); what's missing is a pickup policy that prefers same-group work per worker, with a fallback so affinity never starves throughput. There are several candidate affinity dimensions to weigh, not one: per-instance (the original funnel idea — accumulator state is the hottest win), per-op, per-group (`GroupID` is the key already plumbed), per-wave, and key-based (would compose with the "keyed combine and reduce" enhancement below). Dimensions can conflict with each other and with LIFO scale-down; choose by measurement, not principle. Sequence after the Pool/workq consolidation — the pickup paths are exactly what that pass restructures.
- Adapt psg to ultrapool's cross-library benchmark suite (`maurice2k/ultrapool` `benchmark/`) — ready-made fire-and-forget workloads with adapters for ants/pond/gammazero/fasthttp already written; supplies the cross-library numbers ARCHITECTURE_COMPARISON.md calls for before the README comparison table is published. Extend with latency percentiles (P99/max are primary; the suite measures throughput only). See POSITIONING_RESEARCH.md "Addendum: ultrapool".

### API Enhancements
- Evaluate an `iter.Seq`/`iter.Seq2` interop surface for result consumption (e.g., ranging over skimmed results). The 2026-06-12 competitor sweep (POSITIONING_RESEARCH.md "Competitive landscape sweep") found Go 1.23 iterators becoming the result-streaming substrate across new entrants (rill, firetiger-oss/concurrent, samber/lo) — the most likely leapfrog vector if psg lacks an interop story. Design direction (PN, 2026-06-12): a natural layer built over a Skimmer — range-over-func iterators are push-style (the loop body is the `yield` callback) and a Skimmer's handler is already a serialized push callback, so the adapter is nearly shape-preserving (handler → yield; `iter.Seq2[T, error]` for the error flow). The design crux is the early-termination contract: what breaking out of the range loop means for the wave (stop consuming vs. cancel vs. drain) must be pinned down explicitly.
- Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- Add keyed combine and reduce functionality
- Consider making it possible to positively close and release task and combiner pools without shutting down the overall job?
- Add generic hooks in core PSG for key lifecycle events
- Add metrics hooks for pool resource utilization (in-flight tasks, queue depth)
- Add hooks for job-level monitoring and statistics
- Create standard interfaces for instrumentation providers
- debug mode that runs everything in a way that makes logic easy to debug, ideally in a single goroutine
- consider publishing generally-useful internal packages as standalone projects
- consider adding environment variable-based configuration of PSG default tuning parameters 
- consider adding https://github.com/glycerine/gown annotations and supporting Gown analysis of application code

### Additional Tests and Examples
- Make sure that combiner pools scale down to zero
- Test automatic flushing behavior based on timeout settings somewhere other than just benchmarks
- Ensure no goroutine leaks in any scenario
- Test and ensure correct ongoing behavior when user code recovers from panics that propagated through the framework
- Test edge cases with cross-job context propagation
- Add tests verifying proper shutdown sequence and resource cleanup
- Test multithreaded gathers

### Design Documentation
- Update and refine design docs to make them more readable and less AI-fueled dumps of bullet points
- Add documentation that compares rdvq.Waiters, workq.Watchers, and workq.Waiters with condition variables
- Better establish the theoretical basis of notification conservation and find a way to measure and verify it (the "Formal verification & foundations" items below are the concrete follow-through on this)

### Formal verification & foundations

Defer until after the Pool/workq consolidation lands — formal models rot against a moving design, and these protocols are exactly what that pass restructures. Ordered by ROI.

- **Write up the invariants + happens-before contracts as adversarial prose first** (cheapest, survives refactors better than a model, often finds the bug before any tool). Two highest-value targets:
  - rdvq's register → recheck (`confirmFn`) → block → notify path. Enumerate every interleaving of the race window flagged at `internal/rdvq/queue.go:339` (confirmFn consuming an outbox value while a parallel sender pushes) and argue no item is lost and no waiter blocks forever. Files: `internal/rdvq/queue.go`, `internal/rdvq/waiters.go`.
  - delayq's CAS-min `nextDeadline` with pre-snapshot republish — the "Schedule lowers the deadline between my snapshot and my CAS, so the loser re-surfaces on next Drain" argument. Files: `internal/delayq/delayq.go:172-185, 285-290`.
- **One small TLA+/PlusCal (or Spin/Promela) model of the wakeup protocol**, checking deadlock-freedom + "every pushed item is eventually received." Keep it abstract — model the protocol, not the Go. Be explicit that this proves the *design*, not the binary: translation fidelity and Go's happens-before/weak-memory semantics are the two gaps (default TLA+ assumes sequential consistency).

#### Ranked verification risk targets (where a proof is most likely to surface something or fail to close)

Findings from an adversarial read on 2026-06-06. NB: no concrete bug was proven — these are ranked *risk* assessments, ordered by where to spend verification budget first. Some overlap with the items above/below; treat this as the prioritized "where to look" index.

1. **`aptr.go` `np`/`a128` reconciliation — bespoke, off-paper, highest risk** (`internal/nbcq/aptr.go:45-82`). Two separate obligations, both resting only on a comment:
   - *Progress*: `Load` spins until `addr(np) == a128[0]` and relies on `updateNodePtr` driving `np` to converge to `a128`. Traced interleavings converge (live updater whose `newPair == a128` retries; stale updaters bail on `currentPair != newPair`), but this is exactly where a model checker should confirm no livelock / stuck-`np` interleaving exists. Don't trust it without one.
   - *GC keep-alive*: the invariant "a node held in `a128` only as a raw `uintptr` is also kept alive by `np` or a live `pointer[T]` local" is invisible to the type system; the `:71-72` comment is the entire proof. A refactor that returns from the break path while the node is reachable only via the uintptr is a use-after-free **the race detector will not catch** (reachability bug, not a data race). Existing item below ("Targeted `-race` stress on the aptr.go GC-shadow-pointer seam") is the cheap first cut; this is the case for going further.
2. **Renotify conservation discharge — most likely to fail a *no-deadlock* proof** (`internal/rdvq/notifier.go:49-56, 89-94`). `wrappedRenotify` only fires `wrappedFn` / returns to pool **when `renotify()` is actually invoked**. Benign reading = pool leak (already filed under "Implementation improvements"); malignant reading = if an enqueued listener entry can be discarded without invocation, a real wakeup is **lost** → deadlock. The "if I don't consume, I re-propagate" obligation is *conditional on invocation here*, not unconditional. Open question to settle: is the discard/replace path actually reachable? This is the author's own acknowledged soft spot (`WORKING_NOTES.md:736`) — strong signal.
3. **delayq `wake` ↔ park handshake — cross-module, adversarial-scheduler-sensitive** (`internal/delayq/delayq.go:179-181, 194-196, 285-291` + workq park side). delayq's CAS-min republish is internally clean, BUT in the Schedule-during-Drain race `republishNext` returns the heap min, which can be *later* than the deadline a concurrent `Schedule` just installed in the atomic; the caller arms its timer off that returned value. Correctness then depends entirely on `Schedule`'s `wake()` reaching a worker before it commits to sleeping on the stale-late timer. delayq explicitly punts this ("may or may not be observed… call Drain again"), so **the no-lost-wakeup obligation actually lives in workq, across the wake/park boundary** — classic notify-before-park, split across two modules. Also: `Yield`'s unconditional `Store(MinInt64)` (`:300`) clobbers a concurrent CAS-min — benign for its purpose but widens the interleaving space.
4. **ABA-tag axioms spanning multiple sites — won't "fail," but can't be claimed "proven" without stating them** (`internal/nbcq/nbcq.go:52-55, 75-78, 185-194`). The per-node `next.count` reuse trick deviates from the textbook freelist: `next.count` is **never reset** across pool reuse and the safety claim needs two explicit axioms a checker would force you to state — *never reset* and *never wraps (uint64)*. Both practically unbreakable, but "never reset" is one well-meaning `Reset()` cleanup away from silent breakage. **Action: add guard comments at all four sites** (Init, Reset, D19, and the omnipool-reuse assumption) tying them together so the invariant isn't accidentally severed; and write any eventual theorem as "correct assuming no 64-bit wrap."
5. **rdvq `PopFrontFunc` at-most-one-value invariant — provable, but has a track record** (`internal/rdvq/queue.go:312, 318, 336-342`). The `panic` guards assert `ok` is set at most once across `confirmFn` / `processOrphanFn` / `selectFn`; the waitInbox-before-stack-inbox registration flip is what makes double-delivery impossible. Probably correct as written, but this is the exact spot that already produced a real dropped-notification bug (`WORKING_NOTES.md:59-61`) — delicate enough to model-check rather than trust. (Overlaps with the adversarial-prose item above.)
- **Make the "notification conservation" claim defensible** (currently the boldest unproven foundation; see `docs/backpressure-and-reentrancy.md:469-544`). The doc states it as one invariant but it's really a conjunction of three, and only the first is argued:
  1. *No loss* (safety) — an actionable wakeup is never dropped. The conservation primitive is `if !w.Notify(rf) { rf() }` (`internal/rdvq/waiters.go:93-94`) plus the stranded-renotify handler (`:90-96`).
  2. *No inflation* (safety) — wakeups don't multiply without progress (avoid renotify storms / livelock). Note `NotifyAll`'s mint loop (`waiters.go:145`) and `NoopRenotify` coalescing mean this is NOT a literally conserved quantity.
  3. *Termination / convergence* (liveness) — the cross-system cascade actually stops. **This is the missing piece**: the doc asserts convergence but gives no well-founded variant. "Cross-resource borrowing" (A's notify runs B's work, B's readyFn re-triggers A) is the livelock-prone topology. Find a measure that strictly decreases per round (candidates: total outstanding postponed work; outstanding-token count bounded by waiter count).
  - Scope split: the single-`rdvq.Waiters` version is local and provable; the cross-system version is a *per-node proof obligation* (each integrator must guarantee "if I don't consume, I re-propagate"), only as strong as the weakest integrator. State that contract explicitly so Governor/pools/future systems can be checked against it.
  - Known soft spots / historical counterexamples to address: the fixed drained-as-orphan + outbox-wait lost-notification bug (`WORKING_NOTES.md:61`) and the `wrappedRenotify` leak-on-replace/discard (`WORKING_NOTES.md:736`, conservation violation in the other direction).
- **Fix stale conservation doc before proving anything.** `docs/backpressure-and-reentrancy.md:512-517` documents an `Accepted{ deferred, upstream nbcq.Queue[WorkReadyFunc] }` that no longer exists — current code is `fresh / postponed / waiters / listener / scheduled / unmetDemandFn` (`accepted.go`), and `upstream` moved into `Governor` (`governor.go:16`). The claim can't be proven against a spec that doesn't match the code.
- **Full write-up: the conservation trust boundary as a foundational principle.** API_DESIGN.md principle 7 is only a summary. The full treatment is more fundamental and deserves its own design doc: the conservation invariant is inductive over participating nodes; `internal`-ness closes the induction (clause 1: no user-authored nodes); the bracketed-leaf rule protects users (clause 2: every user callback carries zero propagation duty and is fully bracketed by its surrounding node regardless of blocking/panic/reentry/spawn). Show how the existing reentrancy constraints (task-scatter prohibition, `ctxmeta.ShouldBlock` nil for task contexts, queued-not-recursive skim) are instances of clause 2, and derive the bracketed-leaf test that every future plug-in/hook must pass. The trust boundary is goroutine participation, not module ownership — so otpsg/psgwf hooks are in scope too.
- **Full reconciliation of all documentation against current code** (`doc.go` and everything under `docs/`). The stale `Accepted` struct in `backpressure-and-reentrancy.md` is one symptom; the combiner-branch reshape (op-trio rename, Handler unification, psgfn fold-in, wave-at-construction, pending Pool/workq consolidation) has almost certainly left other docs describing the pre-refactor architecture. Audit each doc for terminology drift (Gather/Combiner/TaskRunner → Skim/Funnel/Launcher), struct/field names, and removed concepts (orphan task queue, etc.). Best done as one pass after the Pool/workq consolidation lands, so docs aren't reconciled twice.
- **Targeted `-race` stress on the aptr.go GC-shadow-pointer seam** (`Load` retry loop + `updateNodePtr` reconciliation, `internal/nbcq/aptr.go:45-82`). This is the one part of nbcq *not* covered by published Michael-Scott linearizability proofs — it's Go-specific glue keeping a GC-visible pointer consistent with the uint128 tagged pointer. Do NOT re-prove MS itself; the version-counter+double-width-CAS ABA scheme is textbook and already proven.
- Note (not a task): "no contention" is a *performance* property — it belongs to benchmarking/profiling (bench.txt, charts), not to a proof. Lock-/obstruction-freedom is the closest provable analogue and is a progress guarantee, not a contention bound.

## Art projects

- Compose a poem of some kind (haiku? limerick? doesn't have to be a specific form) that weaves the purpose and metaphor of the library together to produce a pleasing and memorable feeling of elegant and optimal control of complex, high-volume, and low-latency application flows.
- Design brand assets: a logo, perhaps a mascot, a theme that fits the metaphor and ideally gets usefully woven through examples, tutorials, and presentations.
