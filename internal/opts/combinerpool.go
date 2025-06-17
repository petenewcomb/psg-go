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

// ConfigChanges holds configuration changes for a CombinerPoolState.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type CombinerPoolConfigChanges struct {
	MinConcurrency           *int
	MaxConcurrency           *int
	RetentionPeriod          *time.Duration
	HighUtilThreshold        *float64
	MinThroughputROI         *float64
	AggressiveGrowthFactor   *float64
	ConservativeGrowthFactor *float64
	IdleTimeout              *time.Duration
	MeasurementTimeConstant  *time.Duration
}

type combinerPoolConfig interface {
	Update(changes CombinerPoolConfigChanges)
}

func ApplyToCombinerPool(c combinerPoolConfig, options ...CombinerPoolOption) {
	var changes CombinerPoolConfigChanges
	for _, opt := range options {
		opt.applyToCombinerPool(&changes)
	}
	c.Update(changes)
}

// ConcurrencyBounds sets both min and max concurrency for CombinerPool.
type ConcurrencyBounds struct {
	Min, Max int
}

func (o ConcurrencyBounds) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.MinConcurrency = &o.Min
	c.MaxConcurrency = &o.Max
}

// MinConcurrency sets the minimum concurrency for CombinerPool.
type MinConcurrency struct {
	Min int
}

func (o MinConcurrency) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.MinConcurrency = &o.Min
}

// IdleTimeout sets the idle timeout for CombinerPool.
type IdleTimeout time.Duration

func (o IdleTimeout) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.IdleTimeout = (*time.Duration)(&o)
}

// MeasurementTimeConstant sets the measurement time constant for CombinerPool.
type MeasurementTimeConstant time.Duration

func (o MeasurementTimeConstant) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.MeasurementTimeConstant = (*time.Duration)(&o)
}

// HighUtilizationThreshold sets the high utilization threshold for CombinerPool.
type HighUtilizationThreshold float64

func (o HighUtilizationThreshold) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.HighUtilThreshold = (*float64)(&o)
}

// HistoryRetentionPeriod sets the history retention period for CombinerPool.
type HistoryRetentionPeriod time.Duration

func (o HistoryRetentionPeriod) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.RetentionPeriod = (*time.Duration)(&o)
}

// MinThroughputROI sets the minimum throughput ROI for CombinerPool.
type MinThroughputROI float64

func (o MinThroughputROI) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.MinThroughputROI = (*float64)(&o)
}

// GrowthFactors sets both growth factors for CombinerPool.
type GrowthFactors struct {
	Aggressive, Conservative float64
}

func (o GrowthFactors) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.AggressiveGrowthFactor = &o.Aggressive
	c.ConservativeGrowthFactor = &o.Conservative
}

// AggressiveGrowthFactor sets the aggressive growth factor for CombinerPool.
type AggressiveGrowthFactor float64

func (o AggressiveGrowthFactor) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.AggressiveGrowthFactor = (*float64)(&o)
}

// ConservativeGrowthFactor sets the conservative growth factor for CombinerPool.
type ConservativeGrowthFactor float64

func (o ConservativeGrowthFactor) applyToCombinerPool(c *CombinerPoolConfigChanges) {
	c.ConservativeGrowthFactor = (*float64)(&o)
}
