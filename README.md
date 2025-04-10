[![Go Reference][godev-badge]][godev]
[![Go Report Card][goreport-badge]][goreport]
[![Build Status][build-badge]][builds]
[![License][license-badge]][license]

# Pipelined Scatter-Gather in Go

`psg` is a Go library that implements a pipelined variant of the scatter-gather
concurrency pattern. It simplifies management of asynchronous tasks and their
results, even ones that are heterogeneous, recursive, or interdependent.

## Hello world

The below shows basic usage without error checking.
([playground][helloworld-play])

``` go
ctx := context.Background()
pool := psg.NewPool(2)
job := psg.NewJob(ctx, pool)
defer job.Cancel() // hygiene

newTask := func(s string) psg.TaskFunc[string] {
	return func(context.Context) (string, error) {
		time.Sleep(1 * time.Millisecond)
		return s, nil
	}
}

var results []string
gather := func(ctx context.Context, result string, err error) error {
	results = append(results, result)
	return nil
}

psg.Scatter(ctx, pool, newTask("Hello"), gather)
psg.Scatter(ctx, pool, newTask("world!"), gather)

job.GatherAll(ctx)
fmt.Println(strings.Join(results, " "))
```

For more detailed demonstrations of how `psg` works, see the Observable example
([source][observable-source], [playground][observable-play]) and others in the
[reference documentation][godev].

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

Contributions, including feedback, are welcome! Please feel free to start or
join a [discussion][discussions], create an [issue][issues], or submit a [pull
request][pull requests].

[godev-badge]: https://pkg.go.dev/badge/github.com/petenewcomb/psg-go.svg
[godev]: https://pkg.go.dev/github.com/petenewcomb/psg-go#section-documentation
[goreport-badge]: https://goreportcard.com/badge/github.com/petenewcomb/psg-go
[goreport]: https://goreportcard.com/report/github.com/petenewcomb/psg-go
[build-badge]: https://github.com/petenewcomb/ci-go/actions/workflows/build.yml/badge.svg
[builds]: https://github.com/petenewcomb/ci-go/actions/workflows/build.yml
[license-badge]: https://img.shields.io/github/license/mashape/apistatus.svg
[license]: https://opensource.org/licenses/MIT
[helloworld-play]: https://go.dev/play/p/JTt6gWNNIIV
[observable-source]: ./example_observable_test.go
[observable-play]: https://go.dev/play/p/rJMfZAS468b
[errgroup]: https://pkg.go.dev/golang.org/x/sync/errgroup
[discussions]: https://github.com/petenewcomb/psg-go/discussions
[issues]: https://github.com/petenewcomb/psg-go/issues
[pull requests]: https://github.com/petenewcomb/psg-go/pulls
