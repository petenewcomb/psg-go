// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package bench holds head-to-head comparison benchmarks of streampool against
// other Go concurrency / worker-pool frameworks (and stdlib baselines).
//
// It is a SEPARATE module (see go.mod) on purpose: the third-party frameworks it
// compares against — and their licenses — stay out of the main streampool
// module's dependency graph. The benchmarks live in _test.go files; this file
// exists only to give the module a buildable package and a home for shared,
// non-test helpers as the suite grows.
//
// # What we measure, and why
//
// Go pool libraries (ants, pond, tunny, …) compete on throughput (tasks/sec) and
// memory (B/op, allocs/op, peak goroutines) — bounding goroutine explosion is
// their whole pitch. None benchmark TAIL LATENCY under heavy-tailed blocking
// work, because the operational pain there ("a slow body backs up the dispatcher
// and amplifies the tail") is exactly what streampool's dispatch/execution split
// is built to avoid. So this suite reports BOTH:
//
//   - competitors' turf — throughput, B/op, allocs/op, peak goroutines, so the
//     numbers are legible next to theirs;
//   - our turf — p50/p99/p99.9/max of dispatch latency and end-to-end latency
//     under heavy-tailed blocking I/O, swept across P:D (offered-load : capacity)
//     regimes, where a naive bounded pool's dispatch tail tracks the body tail
//     but streampool's stays flat.
//
// The baselines are the apples-to-apples controls: unbounded goroutines (memory
// blows up), a channel semaphore, and a naive fixed worker pool (the
// "dispatcher-pinned, no split" stand-in). External frameworks slot in alongside
// them through the same [dispatcher] interface.
package bench
