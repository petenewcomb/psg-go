# Refactor Plan

The plan for moving from the current psg-go codebase to the streampool
design captured in `API_DESIGN.md`. This doc tracks status, sequences
remaining work, and flags decisions and design sessions that gate
specific waves.

Companion docs:
- `API_DESIGN.md` — destination: final naming and API surface
- `POSITIONING_RESEARCH.md` — outward-facing audience research
- `ARCHITECTURE_COMPARISON.md` — source-level competitive analysis
- `README-proposed.md` — draft README using the new API

This doc describes the *journey*. API_DESIGN describes the *destination*.

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
- **Design questions for the reshape:** What does a Plan look like
  when:
  - Combiners no longer have an output type (just input)?
  - Tasks return errors rather than (T, error)?
  - TaskRunner exists as a distinct dispatch primitive?
  - Streams (the new Workflow replacement) are first-class lifecycle
    entities?
  - Limiters compose across ops?
- **Requirements session needed** before reshaping anything that the
  sim covers.

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
- When psgwf consolidates into `Stream`, all these need migration.
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

### Wave 2 candidate: Reshape Combiner to drop the output type

**Shape change**: `Combiner[I, O]` → `Combiner[T]`. Accumulator
function body explicitly Submits to downstream rather than returning an
O. `CombinerFactory[I, O]` → `CombinerFactory[T]`. Aligns with
API_DESIGN.md's resolution.

**Scope:**
- Update `psgfn.Combiner` interface (rename to `Accumulator`, drop O,
  change `Combine` signature, drop O from `Flush`).
- Update `psg.Combiner` and `NewCombiner` to match.
- Update all combiner factory implementations across the codebase.
- Update the sim framework's combine domain model.
- Update combiner_test.go benchmarks and tests.
- Update otpsg's `InstrumentedCombiner` (likely deletes the
  PropagatedResult[O] wrapping).
- Update psgwf's combine path similarly.

**Gating design sessions:**
- sim framework's combine domain model
- combiner benchmark intent and structure

**Estimated effort:** Large. The most invasive change in the reshape
path.

### Wave 3 candidate: Reshape Task functions

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

### Wave 5 candidate: ctx propagation enhancement

Submit ctx becomes load-bearing across cross-op boundaries (per
API_DESIGN.md #9 resolution). Foundation for the Stream consolidation
in Wave 6.

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

### Wave 6 candidate: Stream replaces psgwf

With ctx propagation in place, psgwf's workflow concept consolidates
into a `Stream` type in the main package.

**Scope:**
- Add `streampool.Stream` type with refcounted lifecycle, parent-child
  hierarchy, afterFn.
- Add `streampool.ContextWithStream` / `StreamFromContext` helpers.
- Framework auto-Ref/Unref around work item lifecycle via ctx
  inspection.
- Migrate psgwf's tests and examples.
- Delete psgwf package.

**Gating design sessions:**
- psgwf migration — what scenarios survive, what's redundant.

**Estimated effort:** Medium.

### Wave 7 candidate: Drop otpsg

OpenTelemetry integration becomes a doc page.

**Scope:**
- Write doc page demonstrating Stream + `trace.ContextWithSpan` pattern.
- Migrate otpsg's tests/examples to use the standard pattern.
- Delete otpsg package and module.

**Estimated effort:** Small.

### Wave 8 candidate: Module rename

`github.com/petenewcomb/psg-go` → `github.com/petenewcomb/streampool`.

**Scope:**
- `go.mod` change.
- All import paths.
- Badges, CI workflow references.
- Replace `README.md` with `README-proposed.md`.
- Move design docs to a `docs/` subfolder or archive.

**Estimated effort:** Small but invasive (touches every file).

### Wave 9 candidate (optional): Naming cleanup pass

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

Per the user observation that the test/benchmark redesign is the
dominant constraint: the next concrete step is **a design session on
the sim framework's requirements and approach** for the post-reshape
world. That session's output will inform whether Wave 2 (combiner
reshape) can proceed, and what its sim-coverage commitments are.

Other test suites (combiner-specific, gather-specific) likely follow
similar design-then-implement patterns, but the sim is the heaviest
and its decisions ripple into the others.
