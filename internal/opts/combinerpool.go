// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"time"
)

// CombinerPoolOption is a configuration option that can be applied to CombinerPool.
type CombinerPoolOption interface {
	applyToCombinerPool(c *CombinerPoolConfigChanges)
}

// CombinerPoolConfigChanges holds configuration changes for a CombinerPoolState.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type CombinerPoolConfigChanges struct {
	MaxConcurrency *int
	IdleTimeout    *time.Duration
	IdleJitter     *time.Duration
}

type combinerPoolConfig interface {
	Update(changes *CombinerPoolConfigChanges)
}

func ApplyToCombinerPool(c combinerPoolConfig, options ...CombinerPoolOption) {
	var changes CombinerPoolConfigChanges
	for _, opt := range options {
		opt.applyToCombinerPool(&changes)
	}
	c.Update(&changes)
}

// IdleTimeout sets the idle timeout for CombinerPool.
type IdleTimeout time.Duration

func (o IdleTimeout) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.IdleTimeout = (*time.Duration)(&o)
}

// IdleJitter sets the idle jitter for CombinerPool.
type IdleJitter time.Duration

func (o IdleJitter) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.IdleJitter = (*time.Duration)(&o)
}
