# Changelog

The `psg` project adheres to [Semantic Versioning]. This file documents all
notable changes to this project and generally follows the [Keep a Changelog]
format.

## [Unreleased]

### Added

- Dependabot configuration
- GitHub workflows based on petenewcomb/ci-go
- .githooks folder and pre-commit script
- Test coverage for various expected panic conditions
- Job.CancelAndWait to ensure that task goroutines have fully shut down
- Job.Close and Job.CloseAndGatherAll
- Combine, et. al. for scalable aggregation of task results before passing to gather operations
- Flow riders (`WithFlow`, `FlowKey`, `FlowTag`, follow-ups) for path- and DAG-scoped context propagation and end-of-flow lifecycle hooks (subsumes the removed psgwf package)
- `OriginFlow`: the composable read of the originating flow — the context of whatever made this body run (the dispatcher, the skim drive, the last accumulate) — one causal branch point per application
- Flow retention: `PinFlow`/`UnpinFlow`, the pooled in-place primitive (extent rules apply), and `HoldFlow`, the GC-owned safe tier — a cancelable held context whose cancel fires follow-ups, merges their errors into the cancellation cause, and leaves the handle fully usable as a snapshot (no undefined behavior, before or after release)
- otpsg module providing OpenTelemetry integration and observability patterns
- Comprehensive options pattern implementation via psgopt package
- SetOptions methods for atomic configuration updates across all components
- `PSGTRACEINTERNALS` environment variable to enable and configure `runtime/trace` instrumentation of PSG internals
- `Wave` type as the user-facing batch primitive, hosting op references; replaces direct Pool ops at the user surface
- Universal `Handler[T]` interface across Launcher and Skimmer (single interface; per-op-type vocabulary distinction)
- Wave-at-construction with nil-sentinel: ops bind to a Wave at construction, nil defers binding to the dispatching ctx (one op reusable across many waves); resolved transparently from inside any op body (task / accumulate / skim handler)
- `AccumulatorFactory[T]` interface with `Close() error`; framework calls Close on the last reference drop of the bound Funnel
- Per-op-type constructor progression: interface form (alloc-free hot path) → closure form (`NewFn*`) → no-arg specialization (`NewTask*`) → err-only specialization (`NewErr*`)
- Void-T type aliases for intent-naming: `Task`, `ErrHandler`, `ErrAccumulator`, `ErrAccumulatorFactory`, `TaskLauncher`, `ErrLauncher`, `ErrSkimmer`, `ErrFunnel`
- `FuncErrAccumulator` and `FuncErrAccumulatorFactory` adapters: err-only direct-fn-storage adapters that avoid framework-added signature-adapter closures
- `Submit` / `SubmitErr` / `SubmitResult` dispatch family with Try variants on all sinks; each name describes its args (frequency-ordered: value-only > err-only > both)
- `psg.Forever` sentinel — `time.Time` value for "block until success" deadline semantics. Used internally by `Submit` / `SubmitErr` / `SubmitResult` non-Try sugars to make the intent explicit at the call site. `Pool.block` treats `Forever` the same as zero (no timer installation; block until ctx cancellation or notification). Full Try*-honoring-deadline behavior is deferred pending Pool/workq consolidation (see WORKING_NOTES "Thread C — blocked on Pool/workq consolidation")

### Changed

- `GatherOne()` → `Gather()` and `TryGatherOne()` → `TryGather()`
- `psg.Gather[T]` → `psg.GatherOp[T]`, `NewGather()` → `NewGatherOp()`
- `Job.Gather()` now returns `error` instead of `(bool, error)`, with `ErrJobDone` indicating job completion
- `Job.TryGather()` and `Job.TryGatherAll()` now return `ErrJobDone` when the job is done
- TestBySimulation completely refactored to increase correctness, coverage,
  precision, stability, and maintainablility (#3, #6)
- SyncJob merged with Job, because in-flight counters must always be thread-safe
  after all (see below deadlock fix)
- GatherAll now returns without error only after a call to Job.Close
- Pool renamed to TaskPool for clarity vs. the new CombinerPool type
- Significant performance improvements: up to 73% throughput increase and 32% memory reduction
- Pool.SetLimit → TaskPool.SetOptions with WithMaxConcurrency option
- Op trio rename: `Gather`/`Gatherer` → `Skim`/`Skimmer`, `Combiner` → `Funnel`, `TaskRunner` → `Launcher`. Coheres with the streampool nautical theme; drops the academic `scatter-gather` vocabulary
- `Launcher` collapses from per-arity types (Launcher0/Launcher[T]/Launcher2) to a single `Launcher[T]` taking `Handler[T]`; zero-arg via `T=struct{}` with `psgfn.Task` adapter; multi-arg via user struct
- `psgfn` package folded into top-level `psg`; all user-facing function types (Handler, HandlerFunc, Task, ErrHandler, Accumulator, FuncAccumulator, NewAccumulator) are now `psg.*`
- Funnel factory becomes interface: `psgfn.FunnelFactory[T] = func() Accumulator[T]` → `psg.AccumulatorFactory[T]` interface with `NewAccumulator() Accumulator[T]` and `Close() error`

### Fixed

- Require Go 1.24 to avoid need for GOEXPERIMENT=aliastypeparams
- Fixed deadlocks that could occur when scattering tasks during gather operations

### Removed

- Job.MultiGatherAll and Job.TryMultiGatherAll
- `psgfn.Task0`, `psgfn.Task[T]`, `psgfn.Task2[T1, T2]` interfaces and their `TaskFunc*` adapters; replaced by the universal `Handler[T]` plus `psg.Task` named adapter (closure form for `T=struct{}` with short-circuit-on-err semantics)
- `psgfn` package (all types moved to top-level `psg`)
- `Launcher0` and `Launcher2[T1, T2]` types (collapsed to single `Launcher[T]`)

## [0.0.1] - 2025-04-09

### Added

- Initial codebase

[unreleased]: https://github.com/petenewcomb/psg-go/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/petenewcomb/psg-go/releases/tag/v0.0.1
[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
