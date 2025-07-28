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

// DefaultSchedulerLatencyThreshold is the default scheduler latency threshold for [github.com/petenewcomb/psg-go.Job]
// unless overridden with [WithSchedulerLatencyThreshold]. Triggers backpressure when
// scheduler latency exceeds this threshold. Empirically determined; subject to change.
const DefaultSchedulerLatencyThreshold = 0 // disabled for now

// DefaultSchedulerLatencyMaxAge is the default max age for scheduler latency measurements
// for [github.com/petenewcomb/psg-go.Job]
// unless overridden with [WithSchedulerLatencyMaxAge]. Controls how old scheduler latency
// measurements can be before they are ignored. Empirically determined; subject to change.
const DefaultSchedulerLatencyMaxAge = 0 // disabled for now

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

// WithSchedulerBackpressureSettings sets both the scheduler latency threshold and
// max age for [github.com/petenewcomb/psg-go.Job]. This is a
// convenience function for configuring scheduler-based backpressure settings at once.
//
// For setting individual scheduler parameters, see [WithSchedulerLatencyThreshold] and
// [WithSchedulerLatencyMaxAge]. The default values are
// [DefaultSchedulerLatencyThreshold] and [DefaultSchedulerLatencyMaxAge].
//
// Setting threshold to 0 disables scheduler-based backpressure entirely.
func WithSchedulerBackpressureSettings(threshold, maxAge time.Duration) SchedulerBackpressureSettingsOption {
	return opts.SchedulerBackpressureSettings{Threshold: threshold, MaxAge: maxAge}
}

type SchedulerBackpressureSettingsOption interface {
	JobOption
}

// WithSchedulerLatencyThreshold sets the threshold for scheduler latency that
// triggers backpressure during [github.com/petenewcomb/psg-go.Job] scatter
// operations. When scheduler latency exceeds this threshold, new scatter
// operations will be delayed until scheduler pressure decreases.
//
// The default value is [DefaultSchedulerLatencyThreshold].
//
// Setting this to 0 disables scheduler-based backpressure entirely.
//
// Related: [WithSchedulerLatencyMaxAge] controls how old measurements can be, and
// [WithSchedulerBackpressureSettings] sets both at once.
func WithSchedulerLatencyThreshold(threshold time.Duration) SchedulerLatencyThresholdOption {
	return opts.SchedulerLatencyThreshold(threshold)
}

type SchedulerLatencyThresholdOption interface {
	JobOption
}

// WithSchedulerLatencyMaxAge sets the maximum age for scheduler latency measurements
// used by [github.com/petenewcomb/psg-go.Job] for backpressure decisions.
// Measurements older than this age are ignored.
//
// The default value is [DefaultSchedulerLatencyMaxAge].
//
// Related: [WithSchedulerLatencyThreshold] sets the threshold that triggers
// backpressure, and [WithSchedulerBackpressureSettings] sets both at once.
func WithSchedulerLatencyMaxAge(maxAge time.Duration) SchedulerLatencyMaxAgeOption {
	return opts.SchedulerLatencyMaxAge(maxAge)
}

type SchedulerLatencyMaxAgeOption interface {
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
