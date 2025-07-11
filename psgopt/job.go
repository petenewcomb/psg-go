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

// DefaultMaxGCTimeRatioThreshold is the default GC time ratio threshold for [github.com/petenewcomb/psg-go.Job]
// unless overridden with [WithMaxGCTimeRatioThreshold]. Triggers backpressure when
// GC time ratio exceeds this threshold. Empirically determined; subject to change.
const DefaultMaxGCTimeRatioThreshold = 0.5 // 50% of total CPU time used for GC

// DefaultGCTimeUpdateInterval is the default GC monitoring frequency for [github.com/petenewcomb/psg-go.Job]
// unless overridden with [WithGCTimeUpdateInterval]. Controls how frequently GC time
// ratios are monitored for backpressure decisions. Empirically determined; subject to change.
const DefaultGCTimeUpdateInterval = 1 * time.Second

// JobOption is a configuration option that can be applied to Job.
//
// Available Job configuration options:
//   - [WithTaskWorkerIdleTimeout] - Sets task worker idle timeout
//   - [WithGCBackpressureSettings] - Sets both GC time ratio threshold and monitoring frequency
//   - [WithMaxGCTimeRatioThreshold] - Sets GC time ratio threshold for backpressure
//   - [WithGCTimeUpdateInterval] - Sets GC monitoring frequency
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

// WithGCBackpressureSettings sets both the GC time ratio threshold and
// monitoring frequency for [github.com/petenewcomb/psg-go.Job]. This is a
// convenience function for configuring GC-based backpressure settings at once.
//
// For setting individual GC parameters, see [WithMaxGCTimeRatioThreshold] and
// [WithGCTimeUpdateInterval]. The default values are
// [DefaultMaxGCTimeRatioThreshold] and [DefaultGCTimeUpdateInterval].
//
// Setting threshold to 0 disables GC-based backpressure entirely. Setting
// interval to 0 disables GC monitoring entirely, which also disables GC-based
// backpressure.
func WithGCBackpressureSettings(threshold float64, interval time.Duration) GCBackpressureSettingsOption {
	return opts.GCBackpressureSettings{Threshold: threshold, Interval: interval}
}

type GCBackpressureSettingsOption interface {
	JobOption
}

// WithMaxGCTimeRatioThreshold sets the threshold for GC time ratio that
// triggers backpressure during [github.com/petenewcomb/psg-go.Job] scatter
// operations. When the ratio of GC CPU time to total CPU time exceeds this
// threshold, new scatter operations will be delayed until GC pressure
// decreases.
//
// The threshold must be in the range (0, 1], where 1.0 means 100% of CPU time
// spent on GC. The default value is [DefaultMaxGCTimeRatioThreshold].
//
// Setting this to 0 disables GC-based backpressure entirely.
//
// Related: [WithGCTimeUpdateInterval] controls monitoring frequency, and
// [WithGCBackpressureSettings] sets both at once.
func WithMaxGCTimeRatioThreshold(threshold float64) MaxGCTimeRatioThresholdOption {
	return opts.MaxGCTimeRatioThreshold(threshold)
}

type MaxGCTimeRatioThresholdOption interface {
	JobOption
}

// WithGCTimeUpdateInterval sets how frequently
// [github.com/petenewcomb/psg-go.Job] monitors GC time ratios for backpressure
// decisions. More frequent updates provide more responsive backpressure but
// consume more CPU for monitoring.
//
// The default value is [DefaultGCTimeUpdateInterval].
//
// Setting this to 0 disables GC monitoring entirely, which also disables
// GC-based backpressure.
//
// Related: [WithMaxGCTimeRatioThreshold] sets the threshold that triggers
// backpressure, and [WithGCBackpressureSettings] sets both at once.
func WithGCTimeUpdateInterval(interval time.Duration) GCTimeUpdateIntervalOption {
	return opts.GCTimeUpdateInterval(interval)
}

type GCTimeUpdateIntervalOption interface {
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
