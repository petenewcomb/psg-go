// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cpstate

import (
	"fmt"
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// Config holds the complete configuration state for a CombinerPoolState.
type Config struct {
	MaxConcurrency int
	IdleTimeout    time.Duration
}

func (c *Config) Update(changes opts.CombinerPoolConfigChanges) {
	if changes.MaxConcurrency != nil {
		c.MaxConcurrency = *changes.MaxConcurrency
	}
	if changes.IdleTimeout != nil {
		c.IdleTimeout = *changes.IdleTimeout
	}
}

// validate checks that the configuration is internally consistent.
// Panics with a descriptive message if the configuration is invalid.
func (c *Config) validate() {
	// Validate concurrency limits
	if c.MaxConcurrency < -1 {
		panic(fmt.Sprintf("maximum concurrency (%d) must be >= -1", c.MaxConcurrency))
	}

	// Validate idle timeout
	if c.IdleTimeout < -1 {
		panic(fmt.Sprintf("idle timeout (%v) must be >= -1", c.IdleTimeout))
	}
}
