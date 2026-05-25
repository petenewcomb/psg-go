// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"github.com/petenewcomb/psg-go/internal/opts"
)

// AnyPoolOption is a configuration option that can be applied to any pool type
// (TaskPool or CombinerPool). Distinct from PoolOption, which applies to the
// top-level Pool (formerly Job).
//
// Available options that work for both TaskPool and CombinerPool:
//   - [WithMaxConcurrency] - Sets maximum concurrency limit
type AnyPoolOption interface {
	CombinerPoolOption
	TaskPoolOption
}

// WithMaxConcurrency sets the maximum concurrency limit for [github.com/petenewcomb/psg-go.TaskPool] or
// [github.com/petenewcomb/psg-go.CombinerPool].
//
// For TaskPool: A negative value means no limit (tasks will always be launched,
// subject to other backpressure constraints). Zero means no new tasks will be
// launched (i.e., Scatter will block indefinitely) until the limit is changed
// to a non-zero value. The new limit takes effect immediately for subsequent
// task launches and may unblock existing blocked Scatter calls.
//
// For CombinerPool: Sets the maximum number of combiner goroutines. Use -1 to
// indicate unlimited (subject to other backpressure constraints and scaling decisions).
//
// The default for both types of pool is unlimited.
func WithMaxConcurrency(maxConcurrency int) AnyPoolOption {
	return opts.MaxConcurrency{Max: maxConcurrency}
}

type MaxConcurrencyOption interface {
	CombinerPoolOption
	TaskPoolOption
}
