// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// CombineOpOption is a configuration option that can be applied to CombineOp.
//
// Available CombineOp configuration options:
//   - [WithHoldTimes] - Sets both minimum and maximum hold times
//   - [WithMinHoldTime] - Sets minimum time to hold inputs before flushing
//   - [WithMaxHoldTime] - Sets maximum time to hold inputs before flushing
type CombineOpOption = opts.CombineOpOption

// WithHoldTimes sets both the minimum and maximum hold times for [github.com/petenewcomb/psg-go.CombineOp] combiners.
// This is a convenience function for setting both timing constraints at once.
//
// For setting individual times, see [WithMinHoldTime] and [WithMaxHoldTime].
// The defaults are no minimum hold time (0) and no maximum hold time (-1).
//
// Panics if minHoldTime is less than -1, maxHoldTime is less than -1, or
// maxHoldTime is less than minHoldTime (when both are >= 0).
func WithHoldTimes(minHoldTime, maxHoldTime time.Duration) HoldTimesOption {
	return opts.HoldTimes{Min: minHoldTime, Max: maxHoldTime}
}

type HoldTimesOption interface {
	CombineOpOption
}

// WithMinHoldTime sets the minimum time a [github.com/petenewcomb/psg-go.CombineOp] combiner will hold inputs after the last
// combine operation before flushing. This is useful for batching inputs that arrive
// close together in time.
//
// A value of -1 means no idle-based flushing will occur.
// A value of 0 means flush immediately after each combine.
// A positive value means wait at least that duration after the last combine before flushing.
//
// No default minimum hold time is set (equivalent to 0).
//
// This setting is safe to change at any time via SetOptions. However, the timing
// of when the new value takes effect within a running job is undefined.
//
// Related: [WithMaxHoldTime] sets the absolute deadline for flushing, and [WithHoldTimes] sets both at once.
//
// Panics if the value is less than -1 or greater than maxHoldTime (when maxHoldTime >= 0).
func WithMinHoldTime(d time.Duration) MinHoldTimeOption {
	return opts.MinHoldTime(d)
}

type MinHoldTimeOption interface {
	CombineOpOption
}

// WithMaxHoldTime sets the maximum time a [github.com/petenewcomb/psg-go.CombineOp] combiner will hold any inputs before
// flushing, measured from when the first unflushed input was received. This creates
// an upper bound on result latency.
//
// A value of -1 means no absolute deadline for flushing.
// A value of 0 means flush immediately (equivalent to no combining).
// A positive value means wait at most that duration since the first combine before flushing.
//
// No default maximum hold time is set (equivalent to -1).
//
// This setting is safe to change at any time via SetOptions. However, the timing
// of when the new value takes effect within a running job is undefined.
//
// Related: [WithMinHoldTime] sets the minimum idle time before flushing, and [WithHoldTimes] sets both at once.
//
// Panics if the value is less than -1 or less than minHoldTime (when minHoldTime >= 0).
func WithMaxHoldTime(d time.Duration) MaxHoldTimeOption {
	return opts.MaxHoldTime(d)
}

type MaxHoldTimeOption interface {
	CombineOpOption
}
