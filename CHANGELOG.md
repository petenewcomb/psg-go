# Changelog

The `psg` project adheres to [Semantic Versioning]. This file documents all
notable changes to this project and generally follows the [Keep a Changelog]
format.

## [Unreleased]

### Added

- Dependabot configuration
- GitHub workflows based on petenewcomb/ci-go
- Test coverage for expected panic conditions

### Fixed

- Require Go 1.24 to avoid need for GOEXPERIMENT=aliastypeparams
- Relaxed TestBySimulation minMaxConcurrencyTolerance a bit due to a CI test failure

## [0.0.1] - 2025-04-09

### Added

- Initial codebase

[unreleased]: https://github.com/petenewcomb/psg-go/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/petenewcomb/psg-go/releases/tag/v0.0.1
[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
