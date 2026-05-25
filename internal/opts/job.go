// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"fmt"
	"time"
)

// PoolOption is a configuration option that can be applied to Pool.
type PoolOption interface {
	applyToPool(c *PoolConfigChanges)
}

// PoolConfigChanges holds configuration changes for a Pool.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type PoolConfigChanges struct {
	TaskWorkerIdleTimeout           *time.Duration
	TaskWorkerIdleJitter            *time.Duration
	FlushListener                   *func()
	TaskWorkerSpawnConcurrencyLimit *int
}

type poolConfig interface {
	Update(changes PoolConfigChanges)
}

func ApplyToPool(c poolConfig, options ...PoolOption) {
	var changes PoolConfigChanges
	for _, opt := range options {
		opt.applyToPool(&changes)
	}
	c.Update(changes)
}

// taskWorkerIdleTimeout sets the idle timeout for task workers.
type taskWorkerIdleTimeout time.Duration

func (o taskWorkerIdleTimeout) applyToPool(c *PoolConfigChanges) {
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

func (o taskWorkerIdleJitter) applyToPool(c *PoolConfigChanges) {
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

func (o FlushListener) applyToPool(c *PoolConfigChanges) {
	c.FlushListener = &o.Callback
}

// taskWorkerSpawnConcurrencyLimit sets the maximum number of task workers that can be spawning concurrently.
type taskWorkerSpawnConcurrencyLimit int

func (o taskWorkerSpawnConcurrencyLimit) applyToPool(c *PoolConfigChanges) {
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
