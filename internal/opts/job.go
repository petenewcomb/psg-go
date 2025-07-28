// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"time"
)

// JobOption is a configuration option that can be applied to Job.
type JobOption interface {
	applyToJob(c *JobConfigChanges)
}

// JobConfigChanges holds configuration changes for a Job.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type JobConfigChanges struct {
	TaskWorkerIdleTimeout     *time.Duration
	FlushListener             *func()
	SchedulerLatencyThreshold *time.Duration
	SchedulerLatencyMaxAge    *time.Duration
}

type jobConfig interface {
	Update(changes JobConfigChanges)
}

func ApplyToJob(c jobConfig, options ...JobOption) {
	var changes JobConfigChanges
	for _, opt := range options {
		opt.applyToJob(&changes)
	}
	c.Update(changes)
}

// TaskWorkerIdleTimeout sets the idle timeout for task workers.
type TaskWorkerIdleTimeout time.Duration

func (o TaskWorkerIdleTimeout) applyToJob(c *JobConfigChanges) {
	c.TaskWorkerIdleTimeout = (*time.Duration)(&o)
}

// SchedulerLatencyThreshold sets the scheduler latency threshold for backpressure.
type SchedulerLatencyThreshold time.Duration

func (o SchedulerLatencyThreshold) applyToJob(c *JobConfigChanges) {
	c.SchedulerLatencyThreshold = (*time.Duration)(&o)
}

// SchedulerLatencyMaxAge sets the max age for scheduler latency measurements.
type SchedulerLatencyMaxAge time.Duration

func (o SchedulerLatencyMaxAge) applyToJob(c *JobConfigChanges) {
	c.SchedulerLatencyMaxAge = (*time.Duration)(&o)
}

// SchedulerBackpressureSettings sets both the scheduler latency threshold and max age.
type SchedulerBackpressureSettings struct {
	Threshold time.Duration
	MaxAge    time.Duration
}

func (o SchedulerBackpressureSettings) applyToJob(c *JobConfigChanges) {
	c.SchedulerLatencyThreshold = &o.Threshold
	c.SchedulerLatencyMaxAge = &o.MaxAge
}

// FlushListener sets the flush listener callback.
type FlushListener struct {
	Callback func()
}

func (o FlushListener) applyToJob(c *JobConfigChanges) {
	c.FlushListener = &o.Callback
}
