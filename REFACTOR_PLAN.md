# Refactor Plan

The plan for moving from the current psg-go codebase to the streampool
design captured in `API_DESIGN.md`. This doc tracks status, sequences
remaining work, and flags decisions and design sessions that gate
specific waves.

Companion docs:
- `API_DESIGN.md` — destination: final naming and API surface
- `docs/permit-core.md` — the permit allocation model (the hierarchical cache)
- `docs/dispatch-execution-split.md` — the dispatch/execution architecture
- `POSITIONING_RESEARCH.md` — outward-facing audience research
- `ARCHITECTURE_COMPARISON.md` — source-level competitive analysis

This doc describes the *journey*. API_DESIGN describes the *destination*.

> **PARTLY STALE (2026-06-20).** Two things have moved since this plan was written.
> (1) The **op-trio names** in the candidate-wave prose are the older Gatherer /
> TaskRunner / Combiner; the pinned names are **Skimmer / Launcher / Funnel** (with
> `Handler` / `Accumulator` bodies, verb `Submit`) — see `API_DESIGN.md` /
> `CHANGELOG.md`. (2) **`Pool` is no longer user-exposed** (internal, auto-sized;
> concurrency via Limiters), which reframes the Wave 4 / Wave 5 entries below — and a
> **major architecture wave is missing**: the dispatch/execution split + permit-core
> migration (`docs/dispatch-execution-split.md`, `docs/permit-core.md`) — map the
> manager/executor pools onto `worker.Pool` + `workq`, place the governor admission
> gate, and cut over off the eager `limiter.go` (`directRequest` / `reclaimRequest` /
> `suspendForEpisode`). Sequence it around the Pool/workq consolidation. Completed
> Wave 1/2 history below is accurate as-of-then and left as-is.

---

## Status

### Wave 1: Mechanical renames + dead-code drop (complete)

Six commits on `combiner`, all green through the pre-commit hook
(tests + vet + golangci-lint + license check):

| Commit | Subject |
|---|---|
| `1010cb9` | Drop dead options that have no consumer |
| `b6641cc` | Rename Job to Pool |
| `e487474` | Rename GatherOp to Gatherer |
| `eaba0fc` | Rename CombineOp to Combiner |
| `3e84954` | Rename Scatter to Start |
| `a5405f7` | Rename Integrate/TryIntegrate to Submit/TrySubmit |

### Sim refactor (complete)

Sim rewritten to the destination-API vocabulary (Pool / Wave / Flow /
Limiter / TaskRunner / Combiner / Gatherer) against the current API
via an adapter, so subsequent reshape waves can ride on top without
further sim design sessions:

| Commit | Subject |
|---|---|
| `3b05e1a` | rdvq: drain outbox listeners before recycling |
| `6ce2f07` | docs: split Pool into Pool+Wave+Flow three-type model |
| `63a4d57` | sim: rewrite for Pool/Wave/Flow vocabulary against current API |
| `d931052` | sim: enrich generator with fan-in and scatter-from-gather/combine |
| `ac0f3ea` | sim: multi-hop Gatherer chains and multiple terminal Gatherers |
| `3c8aead` | docs: capture Sender-shutdown listener-notify semantics |

### Wave 2: Drop Combiner output type (complete)

`Combiner[I, O]` → `Combiner[T]`, `CombinerFactory[I, O]` →
`CombinerFactory[T]`, renamed to `Accumulator[T]` internally.
Accumulator bodies Submit downstream rather than returning a value.
The sim adapter's `struct{}`-dummy-gatherer trick dissolved cleanly:
`NewCombiner` now creates an internal `errSink` Gatherer[struct{}]
that routes Accumulator errors through Pool.GatherAll.

| Commit | Subject |
|---|---|
| `0655312` | wave 2: drop Combiner output type, rename to Accumulator |

### Intentional gaps after Wave 1

Deferred in Wave 1 per the "leave the old name where it clashes"
strategy. Each will be addressed in a later wave once the clash
condition no longer applies.

- **Lowercase variables/fields not renamed** where they'd collide with
  pre-existing identifiers:
  - `job` (variable/field of type `*Pool`) — collides with omnipool's
    `pool` fields.
  - `combineOp` (variable name and lowercase internal struct type) —
    collides with `combiner` variables referencing `psgfn.Combiner`.
  - `gatherOp` (variable usage in some places, though most got
    renamed to `gatherer`).
- **psgwf's `CombineOp` alias** kept its name (vs being renamed to
  `Combiner`) to avoid clashing with psgwf's own `Combiner` interface
  alias.
- **Internal `jobstate` package** untouched. Its `JobState` type and
  related identifiers are internal infrastructure, not user-facing.
- **`AnyPoolOption`** in psgopt — temporary name for what was the
  multi-pool `PoolOption` interface. Will be removed when TaskPool /
  CombinerPool consolidate into Pool.
- **`internal/sim.Scatter`** (simulation step type) untouched.
  Domain-modeling for simulated scatter actions, not the user-facing
  API verb.
- **Doc comments referring to "Scatter", "Job" etc. as concepts** mostly
  updated, but prose may still reference old names in narrative
  context. Comment overhaul deferred to a documentation pass.

---

## The dominant constraint: tests and benchmarks

The biggest practical challenge for the reshape waves below is **not**
the API changes themselves — it's redesigning the test suites and
benchmarks to:

1. Exercise the new API correctly
2. Generalize their domain models so they cover the *intent* of the API
   rather than its current specific shape
3. Provide coverage that survives future API evolution without
   constant churn

The major test/benchmark suites that need design work before (or
alongside) the reshape waves:

### `internal/sim/` — simulation framework

- Has a plan-based domain model (`Plan`, `Task`, `Gather`, `Combine`,
  `Scatter` step type) that mirrors the current API.
- Plans are constructed via `rapid` property-based generators.
- The framework drives the runtime through every scenario its planner
  can construct and asserts behavioral invariants.
- **Design session complete (2026-05-25).** Decisions:
  - New vocabulary, anchored to destination API (Pool/Wave/Flow/
    Limiter/TaskRunner/Combiner/Gatherer with no FlushHandler field),
    implemented against current API via a small adapter layer.
  - Sim terminology: **Plan** is the static (rapid-generated)
    description of a unit of work; **Wave** is the runtime entity
    that executes it. One Plan corresponds to one Wave at execution
    time. The Plan tree (top-level + nested Subjobs) describes the
    sim's structure; the Wave tree (top-level + child Waves)
    represents the runtime instantiation. Sim files keep the
    `Plan` name — it's the static-description concept distinct from
    the runtime Wave.
  - Adapter uses `struct{}` as combiner output type + a singleton
    dummy `Gatherer[struct{}]` to satisfy current API; real data
    routes via `Submit`/`TrySubmit` from within bodies.
  - Plan generators produce the full destination-API expressive
    range (multi-sink, multi-StartTask, zero-output paths) from day 1
    via the dummy-sink trick. Two narrow gates: cross-kind shared
    Limiters and RateLimit-style Limiters deferred to post-Wave-4.
  - Steps carry `Prob float64` for probabilistic execution;
    SelfTime carries a `BiasedDurationConfig` for per-invocation
    duration draws; Func carries `ReturnErrorProb`. A
    `Deterministic` config mode forces all to 1.0/fixed for
    exact-bound assertions.
  - **Subjob model — phased.** In v1 (against current API): Subjob
    creates its own `psg.Pool` (current behavior; maps cleanly onto
    the no-Wave-type-yet current API), with bidirectional Submit via
    `ParentExposedOps` registration. This exercises cross-Pool
    boundary code (race-heavy coverage). After Wave 5 (Pool/Wave
    split) lands, the adapter switches the default to "Subjob =
    child Wave sharing parent Pool" (typical user pattern), keeping
    cross-Pool as a per-Subjob config knob for exotic coverage.
    Generator code is unchanged; only the adapter's runtime
    interpretation evolves.
  - **Flow not exercised in sim v1.** Flow lifecycle (refcount,
    afterFn, cross-Wave span) is covered by dedicated tests at Wave
    7 landing time, not by the sim's plan generator. Keeps the sim
    focused on op composition / concurrency / error propagation;
    avoids coupling sim assertions to Flow refcount machinery.
  - Per-Limiter pool mapping in the adapter; generator never shares
    Limiters across op kinds (TaskRunner vs Combiner) or uses
    non-Semaphore kinds in v1.
  - **Assertion contract.** In Deterministic mode (all probs 1.0,
    SelfTime distributions fixed, ReturnErrorProb ∈ {0,1}): exact
    Min=Max sink-invocation bounds and exact concurrency-limit
    assertions, matching today's behavior. In probabilistic mode:
    Max-only sink-invocation bounds (≤ theoretical max), best-effort
    concurrency assertions. TestBySimulation runs both modes.

### `combiner_test.go` and combiner-specific benchmarks

- Direct tests of combiner behavior — flush triggers, deadline-driven
  emission, error propagation, factory invocation patterns.
- Benchmarks measure combiner pool sizing, allocation profiles, P99
  latency under varied load.
- **Design questions:** How do we benchmark the same throughput
  characteristics when the API shape changes? Should we keep the old
  shape as a comparison baseline during the transition?
- **Requirements session needed** for the benchmark suite — what are
  we trying to measure and how does that translate to the new API.

### `gather_test.go`

- Gather-specific behavior, recursion through gathers, error
  propagation.
- Less domain-model-heavy than sim; mostly direct API exercise.
- **Likely lighter design lift** but still needs explicit attention
  for the recursive-gather-via-Submit pattern in the new model.

### `psgwf` examples and tests

- Currently demonstrate the workflow-context-propagation pattern via
  the alternate API surface.
- When psgwf consolidates into `Flow` (Wave 7), all these need
  migration. The new ctx-propagation contract (Wave 6) covers the
  bulk of psgwf's value-add automatically; remaining tests focus on
  Flow lifecycle (refcount, parent-child, afterFn) and Flow ctx
  cancellation across Wave boundaries.
- **Design questions:** Which scenarios *are* psgwf's value-add vs.
  ones that fall out of ctx-propagation automatically?

### `otpsg` examples and tests

- Demonstrate OpenTelemetry integration via the result-type-wrapping
  approach.
- When otpsg becomes a doc page, these become doc snippets.
- **Likely lighter design lift** — the goal is "show that standard
  ctx-borne trace propagation works"; specific assertions are minimal.

### Example tests in the main package

- `example_hello_test.go`, `example_combiner_test.go`,
  `example_pipeline_test.go`, etc. — documentation-via-test.
- These set user expectations. They need to be rewritten in the new
  idiom *after* the API has reshaped.
- **Lower priority** — they're consequence of the API, not driver of
  it.

### Cross-cutting: what design sessions actually need to produce

Each suite's session should produce:

- A statement of the suite's *intent* (what behavior is it asserting?
  what performance is it measuring?)
- A target structure under the new API (domain model, assertion
  shape, fixture style)
- A migration approach (rewrite in place? port to new structure first
  then change API? throw away and start over?)
- A list of behavioral invariants that must continue to hold after
  the reshape (regression-safety contract)

These outputs become inputs to the corresponding reshape waves.

---

## Candidate waves (post-Wave 1)

The order below is *not* committed — these are candidates. The actual
sequencing depends on the test-design sessions and on dependency
analysis between waves. Some can probably move in parallel.

### Wave 3 candidate: Reshape Task functions (implementation in progress)

**Shape change**: `func(ctx) (T, error)` → `func(ctx, T) error` where
T is the task's input arg. Body explicitly Submits its result(s) to
downstream sinks. Tasks become argument-taking, not return-routed.

**Scope:**
- Add `TaskRunner[T]`, `TaskRunner0`, `TaskRunner2[T1, T2]` types
  with `Start(ctx, ...)` method.
- Add `Task[T]`, `Task0`, `Task2` interfaces with `Run` method;
  `TaskFunc[T]` wrappers.
- Migrate Gatherer.Start / Combiner.Start callers to TaskRunner.Start
  + sink.Submit.
- Remove Gatherer.Start / Combiner.Start (the dispatch methods, not
  Submit).
- Update sim's task domain model.
- Update tests and examples.

**Gating design sessions:**
- sim framework's task domain model (interacts with combine reshape;
  may want a joint session)
- benchmark suite restructure (touches the task-driven workloads)

**Estimated effort:** Large.

### Wave 4 candidate: Consolidate TaskPool/CombinerPool into Pool

**Shape change:** No user-facing TaskPool or CombinerPool. Pool is the
worker pool. Per-op concurrency control via `Limiter` bound to ops
through `WithLimits(...)`.

**Scope:**
- Add `Limiter` (sealed type) and built-ins (`NewSemaphore`,
  `NewRateLimit`).
- Migrate Pool's internal worker management to subsume what TaskPool
  and CombinerPool currently do separately.
- Migrate all WithMaxConcurrency / pool-construction call sites to
  Limiters.
- Delete `TaskPool` and `CombinerPool` types.
- Delete `AnyPoolOption` (no longer needed).
- Update sim, tests, benchmarks.

**Gating design sessions:**
- sim framework's pool domain model
- limiter semantics under load — what does "shared limiter across N
  ops" look like in the sim?

**Estimated effort:** Large. Touches internals deeply.

### Wave 5 candidate: Split Pool into Pool + Wave (three-type model)

Per API_DESIGN.md (2026-05-25 update, refined same day): the current
`Pool` conflates two roles — fungible worker container and
user-facing batch-of-work. Split into `Pool` (workers only, fungible,
typically implicit via package default) and `Wave` (batch lifecycle
+ op ownership, user-primary type). Sets up Flow consolidation in
Wave 7.

**Shape change:**
- `Pool` becomes purely a worker container. No drain methods, no
  Shutdown, no Wait. Refcount-driven lifecycle: workers exit
  synchronously when the last Wave referencing the Pool completes
  its drain.
- Package-level default Pool exists implicitly; `NewPool` only
  needed for custom ctx or tuning.
- `Wave` is new: hosts ops (op constructors take `*Wave`), exposes
  Gather/GatherAll/Close/CancelAndWait. Nestable via `Wave.NewChild`.
  References a Pool (default or via `WithPool` option).
- `WithPool` option on both `NewWave` and `Wave.NewChild`. Child
  Wave inherits parent's Pool unless overridden.

**Scope:**
- Add `Wave` type. Migrate op-ownership semantics (op-registry,
  drain machinery) from Pool to Wave.
- Reshape op constructors: `NewTaskRunner(wave, ...)`,
  `NewCombiner(wave, ...)`, `NewGatherer(wave, ...)`.
- Strip Pool to just worker management + ctx. Add refcount tracking.
- Implement synchronous worker termination on refcount=0 (signal
  workers, wait for exit, return).
- Establish package-level default Pool (lazy init, background ctx).
- Add `Wave.NewChild`, `WithPool` option.
- Implement cross-Wave Submit (an op's Submit accepts values from
  any Wave's worker; the work item belongs to the target op's Wave).
- Update sim, tests, benchmarks to use Wave-based construction.

**Gating design sessions:**
- (None remaining — the three-type model is resolved in API_DESIGN.md.)

**Estimated effort:** Large. Significant internal refactor of
Pool's responsibilities, plus new Wave type.

### Wave 6 candidate: ctx propagation enhancement

Submit ctx becomes load-bearing across cross-op boundaries (per
API_DESIGN.md #9 resolution). Foundation for Flow consolidation in
Wave 7.

**Scope:**
- Add Submit-ctx field on work items.
- Change combineop's invocation to use Submit ctx with `ensureCtxMeta`
  layered.
- Add Gather-boundary ctx-merging (stdlib WithCancel + AfterFunc for
  v1; pooled-goroutine optimization deferred).
- Test trace context propagation across boundaries (add an
  OpenTelemetry noop-tracer test).

**Gating design sessions:**
- Probably none — this is implementation work. The contract is
  resolved.

**Estimated effort:** Medium.

### Wave 7 candidate: Flow replaces psgwf

With ctx propagation in place, psgwf's Workflow concept consolidates
into a `Flow` type in the main package.

**Shape change:** `psgwf.Workflow` → `streampool.Flow`. Naming: Flow
rather than Workflow (concrete instance reading; avoids abstract/
concrete ambiguity) and Flow rather than Stream (Stream's
multiple-items connotation mismatches Flow's singular-instance
semantics; Stream reserved for future observability concept).

**Scope:**
- Add `streampool.Flow` type with refcounted lifecycle, parent-child
  hierarchy, afterFn.
- Add `streampool.NewFlow(parent context.Context, ...)` returning
  `(context.Context, *Flow)`. Add `FlowFromContext(ctx)` helper.
- Framework auto-Dup/Close around work item lifecycle via ctx
  inspection.
- Flow can span Waves: lifecycle is determined by refcount across
  all work items, not by any single Wave's drain.
- Migrate psgwf's tests and examples.
- Delete psgwf package.

**Gating design sessions:**
- psgwf migration — what scenarios survive, what's redundant.

**Estimated effort:** Medium.

### Wave 8 candidate: Drop otpsg

OpenTelemetry integration becomes a doc page.

**Scope:**
- Write doc page demonstrating Flow + `trace.ContextWithSpan` pattern.
- Migrate otpsg's tests/examples to use the standard pattern.
- Delete otpsg package and module.

**Estimated effort:** Small.

### Wave 9 candidate: Module rename

`github.com/petenewcomb/psg-go` → `github.com/petenewcomb/streampool`.

**Scope:**
- `go.mod` change.
- All import paths.
- Badges, CI workflow references.
- Replace `README.md` with `README-proposed.md`.
- Move design docs to a `docs/` subfolder or archive.

**Estimated effort:** Small but invasive (touches every file).

### Wave 10 candidate (optional): Naming cleanup pass

Sweep up the intentional gaps from earlier waves:
- Lowercase `job` → `pool`, `combineOp` → `combiner`,
  remaining `gatherOp` → `gatherer`
- Internal package `jobstate` → `poolstate`
- Internal struct field renames where the type changed
- File renames (`job.go` → `pool.go`, etc.)
- Doc comment narrative updates

Could be merged into one of the earlier reshape waves if the changes
align, or stand alone as a cleanup commit.

**Estimated effort:** Medium. Mechanical but extensive.

---

## Cross-cutting open questions

Some decisions span multiple waves and need answers before the
relevant waves proceed:

1. **Test framework abstraction strategy.** Should the sim framework
   be refactored to a more API-agnostic shape *before* the reshape
   waves (so it can survive future API evolution), *during* the
   reshape (alongside each change), or *after* (one big rewrite)?
   This is the largest open question and probably the first design
   session.

2. **Benchmark continuity.** Do we keep the old API as a parallel
   target during the transition so benchmarks can compare
   old-vs-new? Or do we cut over and accept that benchmark history
   has a discontinuity?

3. **Wave ordering with respect to test design.** Should reshape
   waves be paused while test design sessions happen, or can the
   test work happen in parallel branches?

4. **Whether to do the lowercase cleanup as a single sweep or
   distributed across the reshape waves.** Per-wave is cleaner
   commit-wise but slows momentum; one sweep is faster but harder to
   bisect.

5. **`psg-go.test` and similar built artifacts** at the repo root —
   should these be added to `.gitignore`? They're not part of the
   refactor but the working tree has accumulated them.

---

## Next concrete step

The sim design session is complete (2026-05-25). Decisions captured
in the sim section above and in API_DESIGN.md's three-type model:

- New sim vocabulary anchored to the destination API (Pool / Wave /
  Flow / Limiter / TaskRunner / Combiner / Gatherer).
- Adapter against current API uses `struct{}` as a dummy output type
  for Combiner and a singleton dummy `Gatherer[struct{}]` to satisfy
  current API's structural requirements; all real data propagation
  routes through `Submit`/`TrySubmit`.
- Per-Limiter pool mapping; generator restricts to single-kind
  Limiter sharing and Semaphore-only Limiters for now.
- Probabilistic Steps (Prob field on Submit/StartTask/Subjob),
  per-invocation SelfTime distribution draw, probabilistic
  ReturnErrorProb on Func — gated by a `Deterministic` config mode
  for exact-bound assertions.
- Subjob keeps its own Pool by default (exercises cross-Pool
  boundary code), supports bidirectional Submit via
  ParentExposedOps registration.

**Next concrete step**: implement the new sim vocabulary in
`internal/sim/` against the current API via the adapter. This
provides the regression-coverage substrate that subsequent reshape
waves (Wave 2 onward) ride on top of without needing further sim
design sessions.

Following the sim implementation, the reshape waves can proceed in
their numbered order: Wave 2 (combiner reshape) → Wave 3 (task
reshape) → Wave 4 (limiter consolidation) → Wave 5 (Pool/Wave
split) → Wave 6 (ctx propagation) → Wave 7 (Flow consolidation) →
Wave 8 (drop otpsg) → Wave 9 (module rename) → Wave 10 (naming
cleanup).

Other test suites (combiner-specific, gather-specific) likely follow
similar design-then-implement patterns, but the sim is the heaviest
and its decisions ripple into the others.
