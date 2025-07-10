// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/gcok"
)

// JobOption is a configuration option that can be applied to Job.
type JobOption interface {
	applyToJob(c *JobConfigChanges)
}

// JobConfigChanges holds configuration changes for a Job.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type JobConfigChanges struct {
	TaskWorkerIdleTimeout *time.Duration
	FlushListener         *func()
	GCConfig              gcok.ConfigChanges
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

// MaxGCTimeRatioThreshold sets the GC time ratio threshold for backpressure.
type MaxGCTimeRatioThreshold float64

func (o MaxGCTimeRatioThreshold) applyToJob(c *JobConfigChanges) {
	c.GCConfig.BusyThreshold = (*float64)(&o)
}

// GCTimeUpdateInterval sets the GC time monitoring update interval.
type GCTimeUpdateInterval time.Duration

func (o GCTimeUpdateInterval) applyToJob(c *JobConfigChanges) {
	c.GCConfig.UpdateInterval = (*time.Duration)(&o)
}

// GCBackpressureSettings sets both the GC time ratio threshold and monitoring interval.
type GCBackpressureSettings struct {
	Threshold float64
	Interval  time.Duration
}

func (o GCBackpressureSettings) applyToJob(c *JobConfigChanges) {
	c.GCConfig.BusyThreshold = &o.Threshold
	c.GCConfig.UpdateInterval = &o.Interval
}

// FlushListener sets the flush listener callback.
type FlushListener struct {
	Callback func()
}

func (o FlushListener) applyToJob(c *JobConfigChanges) {
	c.FlushListener = &o.Callback
}
