// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// DefaultCombinerPoolIdleTimeout is the default goroutine idle timeout for
// [github.com/petenewcomb/psg-go.CombinerPool] unless overridden with
// [WithIdleTimeout]. Empirically determined; subject to change.
const DefaultCombinerPoolIdleTimeout = 1 * time.Second

// DefaultCombinerPoolIdleJitter is the default jitter added to combiner goroutine idle timeouts
// to spread mutex contention when multiple workers timeout. Empirically determined; subject to change.
const DefaultCombinerPoolIdleJitter = 10 * time.Millisecond

// CombinerPoolOption is a configuration option that can be applied to CombinerPool.
//
// Available CombinerPool configuration options:
//   - [WithMaxConcurrency] - Sets maximum concurrency only
//   - [WithIdleTimeout] - Sets goroutine idle timeout before termination
//   - [WithIdleJitter] - Sets idle jitter to spread out mutex contention
type CombinerPoolOption = opts.CombinerPoolOption

// WithIdleTimeout sets how long excess combiner goroutines in
// [github.com/petenewcomb/psg-go.CombinerPool] can remain idle before being
// terminated. Use -1 to disable idle timeout.
//
// The default value is [DefaultCombinerPoolIdleTimeout].
func WithIdleTimeout(timeout time.Duration) IdleTimeoutOption {
	return opts.IdleTimeout(timeout)
}

type IdleTimeoutOption interface {
	CombinerPoolOption
}

// WithIdleJitter sets the random jitter added to combiner goroutine idle timeouts.
// This spreads out mutex contention when multiple workers timeout simultaneously.
//
// The default value is [DefaultCombinerPoolIdleJitter].
func WithIdleJitter(jitter time.Duration) IdleJitterOption {
	return opts.IdleJitter(jitter)
}

type IdleJitterOption interface {
	CombinerPoolOption
}
