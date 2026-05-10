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
const DefaultTaskWorkerIdleTimeout = 1 * time.Second

// DefaultTaskWorkerIdleJitter is the default jitter added to task worker idle timeouts to spread
// mutex contention when multiple workers timeout. Empirically determined; subject to change.
const DefaultTaskWorkerIdleJitter = 10 * time.Millisecond

// DefaultTaskWorkerSpawnConcurrencyLimit is the default maximum number of task workers that can
// be spawning concurrently. Empirically determined; subject to change.
const DefaultTaskWorkerSpawnConcurrencyLimit = 1

// JobOption is a configuration option that can be applied to Job.
//
// Available Job configuration options:
//   - [WithTaskWorkerIdleTimeout] - Sets task worker idle timeout
//   - [WithTaskWorkerIdleJitter] - Sets task worker idle jitter
//   - [WithTaskWorkerSpawnConcurrencyLimit] - Sets max concurrent task worker spawns
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
// Valid values are -1 (disabled, workers never idle-exit) or positive durations.
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

// WithTaskWorkerIdleJitter sets the random jitter added to task worker idle timeouts.
// This spreads out mutex contention when multiple workers timeout simultaneously.
//
// Jitter must be non-negative.
// The default value is [DefaultTaskWorkerIdleJitter].
//
// This setting is safe to change at any time via SetOptions, but only affects
// workers that begin waiting after the change.
func WithTaskWorkerIdleJitter(jitter time.Duration) TaskWorkerIdleJitterOption {
	return opts.TaskWorkerIdleJitter(jitter)
}

type TaskWorkerIdleJitterOption interface {
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

// WithTaskWorkerSpawnConcurrencyLimit sets the maximum number of task workers
// that can be spawning concurrently when handling orphaned tasks. This prevents
// thundering herd behavior when many tasks arrive while all workers are busy.
//
// When an orphaned task is detected and no idle workers are available, the
// system will spawn a new task worker only if the number of workers currently
// in the spawning state is below this limit. Once a spawned worker secures a
// task to execute, it releases its spawn slot, allowing additional spawns.
//
// Valid values are -1 (unlimited) or positive integers.
// The default value is [DefaultTaskWorkerSpawnConcurrencyLimit]. Higher values
// may be appropriate for workloads with sustained high task arrival rates and
// very short task durations.
//
// This setting is safe to change at any time via SetOptions.
func WithTaskWorkerSpawnConcurrencyLimit(limit int) TaskWorkerSpawnConcurrencyLimitOption {
	return opts.TaskWorkerSpawnConcurrencyLimit(limit)
}

type TaskWorkerSpawnConcurrencyLimitOption interface {
	JobOption
}
