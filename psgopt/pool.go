// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"github.com/petenewcomb/psg-go/internal/opts"
)

// WithMaxConcurrency sets the maximum concurrency limit for
// [github.com/petenewcomb/psg-go.CombinerPool] — the maximum number of
// combiner goroutines. Use -1 to indicate unlimited (subject to other
// backpressure constraints and scaling decisions). The default is
// unlimited.
//
// As of Wave 4 the equivalent TaskPool concurrency limit has been
// replaced by the framework's [github.com/petenewcomb/psg-go.Limiter]
// system: pass [github.com/petenewcomb/psg-go.WithLimits] with a
// [github.com/petenewcomb/psg-go.NewSemaphore]-backed Limiter when
// constructing a TaskRunner.
func WithMaxConcurrency(maxConcurrency int) CombinerPoolOption {
	return opts.MaxConcurrency{Max: maxConcurrency}
}
