// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Module streampool/bench is an isolated benchmark module for head-to-head
// comparisons of streampool against other concurrency / worker-pool frameworks.
// It lives in its own module so the third-party frameworks it pulls in (and
// their licenses) never enter the main streampool module's dependency graph.
module github.com/petenewcomb/streampool/bench

go 1.25

require (
	github.com/influxdata/tdigest v0.0.2-0.20210216194612-fc98d27c9e8b
	github.com/petenewcomb/streampool v0.0.1
)

require (
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/petenewcomb/atomic128-go v0.0.3 // indirect
	golang.org/x/sys v0.37.0 // indirect
)

replace github.com/petenewcomb/streampool => ../
