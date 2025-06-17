// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

import (
	"time"
)

// CombineOpOption is a configuration option that can be applied to CombineOp.
type CombineOpOption interface {
	applyToCombineOp(c *CombineOpConfigChanges)
}

// CombineOpConfigChanges holds configuration changes for a CombineOp.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type CombineOpConfigChanges struct {
	MinHoldTime *time.Duration
	MaxHoldTime *time.Duration
}

type combineOpConfig interface {
	Update(changes CombineOpConfigChanges)
}

func ApplyToCombineOp(c combineOpConfig, options ...CombineOpOption) {
	var changes CombineOpConfigChanges
	for _, opt := range options {
		opt.applyToCombineOp(&changes)
	}
	c.Update(changes)
}

// MinHoldTime sets the minimum hold time for a CombineOp.
type MinHoldTime time.Duration

func (o MinHoldTime) applyToCombineOp(c *CombineOpConfigChanges) {
	c.MinHoldTime = (*time.Duration)(&o)
}

// MaxHoldTime sets the maximum hold time for a CombineOp.
type MaxHoldTime time.Duration

func (o MaxHoldTime) applyToCombineOp(c *CombineOpConfigChanges) {
	c.MaxHoldTime = (*time.Duration)(&o)
}

// HoldTimes sets both minimum and maximum hold times for a CombineOp.
type HoldTimes struct {
	Min time.Duration
	Max time.Duration
}

func (o HoldTimes) applyToCombineOp(c *CombineOpConfigChanges) {
	c.MinHoldTime = &o.Min
	c.MaxHoldTime = &o.Max
}
