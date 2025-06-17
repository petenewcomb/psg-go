// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// DefaultCombinerPoolMeasurementTimeConstant is the default throughput measurement period for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithMeasurementTimeConstant]. Empirically determined; subject to change.
const DefaultCombinerPoolMeasurementTimeConstant = 50 * time.Millisecond

// DefaultCombinerPoolHistoryRetentionPeriod is the default sample retention period for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithHistoryRetentionPeriod]. Empirically determined; subject to change.
const DefaultCombinerPoolHistoryRetentionPeriod = 1 * time.Second

// DefaultCombinerPoolIdleTimeout is the default goroutine idle timeout for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithIdleTimeout]. Empirically determined; subject to change.
const DefaultCombinerPoolIdleTimeout = 100 * time.Microsecond

// DefaultCombinerPoolHighUtilizationThreshold is the default utilization threshold for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithHighUtilizationThreshold]. Empirically determined; subject to change.
const DefaultCombinerPoolHighUtilizationThreshold = 0.6 // Last goroutine must be more than 60% utilized

// DefaultCombinerPoolMinThroughputROI is the default throughput ROI threshold for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithMinThroughputROI]. Empirically determined; subject to change.
const DefaultCombinerPoolMinThroughputROI = 0.01 // Each new goroutine must add at least 1% more throughput

// DefaultCombinerPoolAggressiveGrowthFactor is the default aggressive growth factor for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithAggressiveGrowthFactor] or [WithGrowthFactors]. Empirically determined; subject to change.
const DefaultCombinerPoolAggressiveGrowthFactor = 1.5 // Add 50% more goroutines

// DefaultCombinerPoolConservativeGrowthFactor is the default conservative growth factor for [github.com/petenewcomb/psg-go.CombinerPool]
// unless overridden with [WithConservativeGrowthFactor] or [WithGrowthFactors]. Empirically determined; subject to change.
const DefaultCombinerPoolConservativeGrowthFactor = 1.1 // Add 10% more goroutines

// CombinerPoolOption is a configuration option that can be applied to CombinerPool.
//
// Available CombinerPool configuration options:
//   - [WithConcurrencyBounds] - Sets minimum and maximum allowed concurrency
//   - [WithMinConcurrency] - Sets minimum concurrency only
//   - [WithMaxConcurrency] - Sets maximum concurrency only
//   - [WithIdleTimeout] - Sets goroutine idle timeout before termination
//   - [WithMeasurementTimeConstant] - Sets throughput measurement period
//   - [WithHighUtilizationThreshold] - Sets high utilization threshold
//   - [WithHistoryRetentionPeriod] - Sets performance sample retention period
//   - [WithMinThroughputROI] - Sets minimum throughput return-on-investment threshold
//   - [WithGrowthFactors] - Sets both aggressive and conservative growth factors
//   - [WithAggressiveGrowthFactor] - Sets aggressive growth factor only
//   - [WithConservativeGrowthFactor] - Sets conservative growth factor only
type CombinerPoolOption = opts.CombinerPoolOption

// WithConcurrencyBounds sets the minimum and maximum concurrency bounds for [github.com/petenewcomb/psg-go.CombinerPool].
// Use -1 for maxConcurrency to indicate unlimited. The default minimum is 0 and maximum is unlimited.
//
// For setting only one bound, see [WithMinConcurrency] and [WithMaxConcurrency].
func WithConcurrencyBounds(minConcurrency, maxConcurrency int) ConcurrencyBoundsOption {
	return opts.ConcurrencyBounds{Min: minConcurrency, Max: maxConcurrency}
}

type ConcurrencyBoundsOption interface {
	CombinerPoolOption
}

// WithMinConcurrency sets only the minimum concurrency for [github.com/petenewcomb/psg-go.CombinerPool].
// The default minimum is 0.
//
// To set both bounds at once, use [WithConcurrencyBounds]. To set only the maximum, use [WithMaxConcurrency].
func WithMinConcurrency(minConcurrency int) MinConcurrencyOption {
	return opts.MinConcurrency{Min: minConcurrency}
}

type MinConcurrencyOption interface {
	CombinerPoolOption
}

// WithIdleTimeout sets how long excess combiner goroutines in [github.com/petenewcomb/psg-go.CombinerPool] can remain idle
// before being terminated. Use -1 to disable idle timeout.
//
// The default value is [DefaultCombinerPoolIdleTimeout].
func WithIdleTimeout(timeout time.Duration) IdleTimeoutOption {
	return opts.IdleTimeout(timeout)
}

type IdleTimeoutOption interface {
	CombinerPoolOption
}

// WithMeasurementTimeConstant sets the period over which [github.com/petenewcomb/psg-go.CombinerPool] combiner throughput
// is measured for scaling decisions.
//
// The default value is [DefaultCombinerPoolMeasurementTimeConstant].
func WithMeasurementTimeConstant(d time.Duration) MeasurementTimeConstantOption {
	return opts.MeasurementTimeConstant(d)
}

type MeasurementTimeConstantOption interface {
	CombinerPoolOption
}

// WithHighUtilizationThreshold sets the utilization threshold above which
// a [github.com/petenewcomb/psg-go.CombinerPool] combiner goroutine is considered highly utilized.
//
// The default value is [DefaultCombinerPoolHighUtilizationThreshold].
func WithHighUtilizationThreshold(threshold float64) HighUtilizationThresholdOption {
	return opts.HighUtilizationThreshold(threshold)
}

type HighUtilizationThresholdOption interface {
	CombinerPoolOption
}

// WithHistoryRetentionPeriod sets how long [github.com/petenewcomb/psg-go.CombinerPool] performance samples are retained
// for scaling analysis.
//
// The default value is [DefaultCombinerPoolHistoryRetentionPeriod].
func WithHistoryRetentionPeriod(d time.Duration) HistoryRetentionPeriodOption {
	return opts.HistoryRetentionPeriod(d)
}

type HistoryRetentionPeriodOption interface {
	CombinerPoolOption
}

// WithMinThroughputROI sets the threshold ratio for detecting the [github.com/petenewcomb/psg-go.CombinerPool] throughput knee.
//
// The default value is [DefaultCombinerPoolMinThroughputROI].
func WithMinThroughputROI(ratio float64) MinThroughputROIOption {
	return opts.MinThroughputROI(ratio)
}

type MinThroughputROIOption interface {
	CombinerPoolOption
}

// WithGrowthFactors sets the multipliers used when scaling up [github.com/petenewcomb/psg-go.CombinerPool] combiner goroutines.
//
// For setting individual factors, see [WithAggressiveGrowthFactor] and [WithConservativeGrowthFactor].
// The default values are [DefaultCombinerPoolAggressiveGrowthFactor] and [DefaultCombinerPoolConservativeGrowthFactor].
func WithGrowthFactors(aggressive, conservative float64) GrowthFactorsOption {
	return opts.GrowthFactors{Aggressive: aggressive, Conservative: conservative}
}

type GrowthFactorsOption interface {
	CombinerPoolOption
}

// WithAggressiveGrowthFactor sets only the aggressive growth factor for [github.com/petenewcomb/psg-go.CombinerPool].
//
// To set both factors at once, use [WithGrowthFactors]. To set only the conservative factor, use [WithConservativeGrowthFactor].
// The default value is [DefaultCombinerPoolAggressiveGrowthFactor].
func WithAggressiveGrowthFactor(factor float64) AggressiveGrowthFactorOption {
	return opts.AggressiveGrowthFactor(factor)
}

type AggressiveGrowthFactorOption interface {
	CombinerPoolOption
}

// WithConservativeGrowthFactor sets only the conservative growth factor for [github.com/petenewcomb/psg-go.CombinerPool].
//
// To set both factors at once, use [WithGrowthFactors]. To set only the aggressive factor, use [WithAggressiveGrowthFactor].
// The default value is [DefaultCombinerPoolConservativeGrowthFactor].
func WithConservativeGrowthFactor(factor float64) ConservativeGrowthFactorOption {
	return opts.ConservativeGrowthFactor(factor)
}

type ConservativeGrowthFactorOption interface {
	CombinerPoolOption
}
