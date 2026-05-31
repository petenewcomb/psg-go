// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

// MaxConcurrency sets the maximum concurrency for pools.
type MaxConcurrency struct {
	Max int
}

func (o MaxConcurrency) applyToFunnelPool(c *FunnelPoolConfigChanges) {
	c.MaxConcurrency = &o.Max
}
