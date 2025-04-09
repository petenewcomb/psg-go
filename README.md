[![Go Reference][godev-badge]][godev]
[![Go Report Card][goreport-badge]][goreport]
[![License][license-badge]][license]

# Pipelined Scatter-Gather in Go

`psg` is a Go library that implements a pipelined variant of the scatter-gather
concurrency pattern. It provides an API that simplifies management of
asynchronous tasks and their results, even ones that are heterogeneous,
recursive, or interdependent.

## Features

Pipelined scatter-gather, as defined here, comprises three key features:
 1. User code processes task results as they arrive (i.e., in incremental or
    streaming fashion)
 2. Result processing code can launch new tasks as part of the same job (e.g.,
    recursive crawling or multi-stage operations)
 3. Concurrency of different groups of tasks can be independently controlled
    (e.g., compute- vs I/O-bound tasks)

Like [`errgroup`][errgroup], `psg` "provides synchronization, error propagation,
and Context cancelation for groups of goroutines working on subtasks of a common
task." Unlike `errgroup`, `psg` goes beyond error and goroutine lifetime
management to incrementally propagate subtask results and errors back to the
common task without requiring the user to employ additional channels or
synchronization constructs.

Additional features:
  - Type-safe API using Go generics
  - Dynamic concurrency limits
  - Optional parallel result gathering

See the [reference documentation][godev] for a technical overview, complete examples,
and API details.

## License

Copyright (c) Peter Newcomb. All rights reserved.

Licensed under the MIT License.

## Contributing

Contributions, including feedback, are welcome! Please feel free to submit an
[issue][issues] or [pull request][pull requests].

[godev-badge]: https://pkg.go.dev/badge/github.com/petenewcomb/psg-go.svg
[godev]: https://pkg.go.dev/github.com/petenewcomb/psg-go
[goreport-badge]: https://goreportcard.com/badge/github.com/petenewcomb/psg-go
[goreport]: https://goreportcard.com/report/github.com/petenewcomb/psg-go
[license-badge]: https://img.shields.io/github/license/mashape/apistatus.svg
[license]: https://opensource.org/licenses/MIT
[errgroup]: https://pkg.go.dev/golang.org/x/sync/errgroup
[issues]: https://github.com/petenewcomb/psg-go/issues
[pull requests]: https://github.com/petenewcomb/psg-go/pulls
