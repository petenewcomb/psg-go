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
- psgwf package providing workflow context propagation and lifecycle management utilities
- otpsg module providing OpenTelemetry integration and observability patterns
- Comprehensive options pattern implementation via psgopt package
- SetOptions methods for atomic configuration updates across all components
- `PSGTRACEINTERNALS` environment variable to enable and configure `runtime/trace` instrumentation of PSG internals 

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

### Fixed

- Require Go 1.24 to avoid need for GOEXPERIMENT=aliastypeparams
- Fixed deadlocks that could occur when scattering tasks during gather operations

### Removed

- Job.MultiGatherAll and Job.TryMultiGatherAll

## [0.0.1] - 2025-04-09

### Added

- Initial codebase

[unreleased]: https://github.com/petenewcomb/psg-go/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/petenewcomb/psg-go/releases/tag/v0.0.1
[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
