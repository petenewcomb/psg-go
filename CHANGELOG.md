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

### Changed

- TestBySimulation completely refactored to increase correctness, coverage,
  precision, stability, and maintainablility (#3, #6)
- SyncJob merged with Job, because in-flight counters must always be thread-safe
  after all (see below deadlock fix)
- GatherAll now returns without error only after a call to Job.Close

### Fixed

- Require Go 1.24 to avoid need for GOEXPERIMENT=aliastypeparams
- Deadlock during scatter or gather due to race between counter and channel

### Removed

- Job.MultiGatherAll and Job.TryMultiGatherAll

## [0.0.1] - 2025-04-09

### Added

- Initial codebase

[unreleased]: https://github.com/petenewcomb/psg-go/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/petenewcomb/psg-go/releases/tag/v0.0.1
[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
