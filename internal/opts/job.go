// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"fmt"
	"time"
)

// JobOption is a configuration option that can be applied to Job.
type JobOption interface {
	applyToJob(c *JobConfigChanges)
}

// JobConfigChanges holds configuration changes for a Job.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type JobConfigChanges struct {
	TaskWorkerIdleTimeout           *time.Duration
	TaskWorkerIdleJitter            *time.Duration
	FlushListener                   *func()
	TaskWorkerSpawnConcurrencyLimit *int
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

// taskWorkerIdleTimeout sets the idle timeout for task workers.
type taskWorkerIdleTimeout time.Duration

func (o taskWorkerIdleTimeout) applyToJob(c *JobConfigChanges) {
	c.TaskWorkerIdleTimeout = (*time.Duration)(&o)
}

// TaskWorkerIdleTimeout creates a task worker idle timeout option.
// Valid values are -1 (disabled, workers never idle-exit) or positive durations.
func TaskWorkerIdleTimeout(timeout time.Duration) taskWorkerIdleTimeout {
	if timeout != -1 && timeout <= 0 {
		panic(fmt.Sprintf("task worker idle timeout must be -1 (disabled) or positive, got %v", timeout))
	}
	return taskWorkerIdleTimeout(timeout)
}

// taskWorkerIdleJitter sets the idle jitter for task workers.
type taskWorkerIdleJitter time.Duration

func (o taskWorkerIdleJitter) applyToJob(c *JobConfigChanges) {
	c.TaskWorkerIdleJitter = (*time.Duration)(&o)
}

// TaskWorkerIdleJitter creates a task worker idle jitter option.
// Jitter must be non-negative.
func TaskWorkerIdleJitter(jitter time.Duration) taskWorkerIdleJitter {
	if jitter < 0 {
		panic(fmt.Sprintf("task worker idle jitter must be non-negative, got %v", jitter))
	}
	return taskWorkerIdleJitter(jitter)
}

// FlushListener sets the flush listener callback.
type FlushListener struct {
	Callback func()
}

func (o FlushListener) applyToJob(c *JobConfigChanges) {
	c.FlushListener = &o.Callback
}

// taskWorkerSpawnConcurrencyLimit sets the maximum number of task workers that can be spawning concurrently.
type taskWorkerSpawnConcurrencyLimit int

func (o taskWorkerSpawnConcurrencyLimit) applyToJob(c *JobConfigChanges) {
	c.TaskWorkerSpawnConcurrencyLimit = (*int)(&o)
}

// TaskWorkerSpawnConcurrencyLimit creates a task worker spawn concurrency limit option.
// Valid values are -1 (unlimited) or positive integers.
func TaskWorkerSpawnConcurrencyLimit(limit int) taskWorkerSpawnConcurrencyLimit {
	if limit != -1 && limit <= 0 {
		panic(fmt.Sprintf("task worker spawn concurrency limit must be -1 (unlimited) or positive, got %d", limit))
	}
	return taskWorkerSpawnConcurrencyLimit(limit)
}
