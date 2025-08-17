// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// DefaultTaskWorkerIdleTimeout is the default task worker idle timeout for [github.com/petenewcomb/psg-go.Job]
// unless overridden with [WithTaskWorkerIdleTimeout]. Controls how long task workers
// wait for new work before exiting. Empirically determined; subject to change.
const DefaultTaskWorkerIdleTimeout = 100 * time.Millisecond

// JobOption is a configuration option that can be applied to Job.
//
// Available Job configuration options:
//   - [WithTaskWorkerIdleTimeout] - Sets task worker idle timeout
//   - [WithSchedulerBackpressureSettings] - Sets both scheduler latency threshold and max age
//   - [WithSchedulerLatencyThreshold] - Sets scheduler latency threshold for backpressure
//   - [WithSchedulerLatencyMaxAge] - Sets max age for scheduler latency measurements
//   - [WithFlushListener] - Registers callback for when all tasks complete
type JobOption = opts.JobOption

// WithTaskWorkerIdleTimeout sets the duration that idle task workers in [github.com/petenewcomb/psg-go.Job] wait for
// new work before exiting. This controls how aggressively workers scale down
// when load decreases.
//
// A shorter timeout reduces resource usage during idle periods but may increase
// overhead when load patterns are bursty. A longer timeout keeps workers alive
// longer, reducing spawn/teardown overhead but potentially wasting resources.
//
// The default value is [DefaultTaskWorkerIdleTimeout].
//
// This setting is safe to change at any time via SetOptions, but only affects
// workers that begin waiting after the change. Workers already in their idle
// timeout will use the previous value.
func WithTaskWorkerIdleTimeout(timeout time.Duration) TaskWorkerIdleTimeoutOption {
	return opts.TaskWorkerIdleTimeout(timeout)
}

type TaskWorkerIdleTimeoutOption interface {
	JobOption
}

// WithFlushListener registers a callback function that will be called each time
// all tasks have completed and [github.com/petenewcomb/psg-go.Job] is waiting
// for combiners to emit their results. After the callback returns, the job
// signals any Combiner that has received inputs but hasn't yet emitted its
// combined results to do so immediately. The callback may be invoked multiple
// times during a job's lifecycle if a Gather directly or indirectly launches
// new tasks while processing the flushed results.
//
// The callback function is called synchronously from a goroutine calling a
// gather method (Job.Gather, Job.TryGather, Job.GatherAll, Job.TryGatherAll,
// Job.CloseAndGatherAll), Gather.Scatter, or Job.Close if no tasks are in
// flight at the time of closing.
//
// No default callback is registered.
//
// If called multiple times via SetOptions, each call replaces any previously
// registered callback. Passing nil removes any existing callback.
func WithFlushListener(callback func()) FlushListenerOption {
	return opts.FlushListener{Callback: callback}
}

type FlushListenerOption interface {
	JobOption
}
