// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"github.com/petenewcomb/streampool/internal/opts"
)

// WithMaxConcurrency is an advanced-tuning knob that caps the number
// of funnel goroutines a [github.com/petenewcomb/streampool.FunnelPool]
// will run. Use -1 to indicate unlimited (subject to other
// backpressure constraints and scaling decisions); the default is
// unlimited.
//
// For per-Funnel concurrency control — the more common case — pass
// [github.com/petenewcomb/streampool.WithLimits] with a
// [github.com/petenewcomb/streampool.NewSemaphore]-backed Limiter to
// [github.com/petenewcomb/streampool.NewFunnel]. That caps how many
// funnel work items execute concurrently for one Funnel, which is
// usually the bound users actually want to express. The FunnelPool-
// wide cap survives as a way to bound the goroutine count when
// several Funnels share a pool.
//
// As of Wave 4 the equivalent TaskPool concurrency limit has been
// replaced entirely by the framework's
// [github.com/petenewcomb/streampool.Limiter] system on [Launcher];
// TaskPool is no longer a user-facing type.
func WithMaxConcurrency(maxConcurrency int) FunnelPoolOption {
	return opts.MaxConcurrency{Max: maxConcurrency}
}
