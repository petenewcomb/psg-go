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
	controllerConfig
	RetentionPeriod         time.Duration
	IdleTimeout             time.Duration
	MeasurementTimeConstant time.Duration
}

func (c *Config) Update(changes opts.CombinerPoolConfigChanges) {
	if changes.MinConcurrency != nil {
		c.MinConcurrency = *changes.MinConcurrency
	}
	if changes.MaxConcurrency != nil {
		c.MaxConcurrency = *changes.MaxConcurrency
	}
	if changes.RetentionPeriod != nil {
		c.RetentionPeriod = *changes.RetentionPeriod
	}
	if changes.HighUtilThreshold != nil {
		c.HighUtilThreshold = *changes.HighUtilThreshold
	}
	if changes.MinThroughputROI != nil {
		c.MinThroughputROI = *changes.MinThroughputROI
	}
	if changes.AggressiveGrowthFactor != nil {
		c.AggressiveGrowthFactor = *changes.AggressiveGrowthFactor
	}
	if changes.ConservativeGrowthFactor != nil {
		c.ConservativeGrowthFactor = *changes.ConservativeGrowthFactor
	}
	if changes.IdleTimeout != nil {
		c.IdleTimeout = *changes.IdleTimeout
	}
	if changes.MeasurementTimeConstant != nil {
		c.MeasurementTimeConstant = *changes.MeasurementTimeConstant
	}
}

// validate checks that the configuration is internally consistent.
// Panics with a descriptive message if the configuration is invalid.
func (c *Config) validate() {
	// Validate concurrency limits
	if c.MaxConcurrency >= 0 && c.MinConcurrency > c.MaxConcurrency {
		panic(fmt.Sprintf("minimum concurrency (%d) cannot exceed maximum concurrency (%d)", c.MinConcurrency, c.MaxConcurrency))
	}
	if c.MinConcurrency < 0 {
		panic(fmt.Sprintf("minimum concurrency (%d) must be >= 0", c.MinConcurrency))
	}
	if c.MaxConcurrency < -1 {
		panic(fmt.Sprintf("maximum concurrency (%d) must be >= -1", c.MaxConcurrency))
	}

	// Validate idle timeout
	if c.IdleTimeout < -1 {
		panic(fmt.Sprintf("idle timeout (%v) must be >= -1", c.IdleTimeout))
	}

	// Validate measurement time constant
	if c.MeasurementTimeConstant <= 0 {
		panic(fmt.Sprintf("measurement time constant (%v) must be > 0", c.MeasurementTimeConstant))
	}

	// Validate utilization threshold
	if c.HighUtilThreshold < 0 || c.HighUtilThreshold > 1 {
		panic(fmt.Sprintf("high utilization threshold (%f) must be between 0 and 1", c.HighUtilThreshold))
	}

	// Validate history retention period
	if c.RetentionPeriod <= 0 {
		panic(fmt.Sprintf("history retention period (%v) must be > 0", c.RetentionPeriod))
	}

	// Validate minimum throughput ROI
	if c.MinThroughputROI < 0 || c.MinThroughputROI > 1 {
		panic(fmt.Sprintf("minimum throughput ROI (%f) must be between 0 and 1", c.MinThroughputROI))
	}

	// Validate growth factors
	if c.AggressiveGrowthFactor <= 1 {
		panic(fmt.Sprintf("aggressive growth factor (%f) must be > 1", c.AggressiveGrowthFactor))
	}
	if c.ConservativeGrowthFactor <= 1 {
		panic(fmt.Sprintf("conservative growth factor (%f) must be > 1", c.ConservativeGrowthFactor))
	}
}
