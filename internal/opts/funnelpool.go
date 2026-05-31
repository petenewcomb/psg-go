// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"time"
)

// FunnelPoolOption is a configuration option that can be applied to FunnelPool.
type FunnelPoolOption interface {
	applyToFunnelPool(c *FunnelPoolConfigChanges)
}

// FunnelPoolConfigChanges holds configuration changes for a FunnelPoolState.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type FunnelPoolConfigChanges struct {
	MaxConcurrency *int
	IdleTimeout    *time.Duration
	IdleJitter     *time.Duration
}

type funnelPoolConfig interface {
	Update(changes *FunnelPoolConfigChanges)
}

func ApplyToFunnelPool(c funnelPoolConfig, options ...FunnelPoolOption) {
	var changes FunnelPoolConfigChanges
	for _, opt := range options {
		opt.applyToFunnelPool(&changes)
	}
	c.Update(&changes)
}

// IdleTimeout sets the idle timeout for FunnelPool.
type IdleTimeout time.Duration

func (o IdleTimeout) applyToFunnelPool(c *FunnelPoolConfigChanges) {
	c.IdleTimeout = (*time.Duration)(&o)
}

// IdleJitter sets the idle jitter for FunnelPool.
type IdleJitter time.Duration

func (o IdleJitter) applyToFunnelPool(c *FunnelPoolConfigChanges) {
	c.IdleJitter = (*time.Duration)(&o)
}
