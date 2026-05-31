// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"time"

	"github.com/petenewcomb/psg-go/internal/opts"
)

// DefaultFunnelPoolIdleTimeout is the default goroutine idle timeout for
// [github.com/petenewcomb/psg-go.FunnelPool] unless overridden with
// [WithIdleTimeout]. Empirically determined; subject to change.
const DefaultFunnelPoolIdleTimeout = 1 * time.Second

// DefaultFunnelPoolIdleJitter is the default jitter added to funnel goroutine idle timeouts
// to spread mutex contention when multiple workers timeout. Empirically determined; subject to change.
const DefaultFunnelPoolIdleJitter = 10 * time.Millisecond

// FunnelPoolOption is a configuration option that can be applied to FunnelPool.
//
// Available FunnelPool configuration options:
//   - [WithMaxConcurrency] - Sets maximum concurrency only
//   - [WithIdleTimeout] - Sets goroutine idle timeout before termination
//   - [WithIdleJitter] - Sets idle jitter to spread out mutex contention
type FunnelPoolOption = opts.FunnelPoolOption

// WithIdleTimeout sets how long excess funnel goroutines in
// [github.com/petenewcomb/psg-go.FunnelPool] can remain idle before being
// terminated. Use -1 to disable idle timeout.
//
// The default value is [DefaultFunnelPoolIdleTimeout].
func WithIdleTimeout(timeout time.Duration) IdleTimeoutOption {
	return opts.IdleTimeout(timeout)
}

type IdleTimeoutOption interface {
	FunnelPoolOption
}

// WithIdleJitter sets the random jitter added to funnel goroutine idle timeouts.
// This spreads out mutex contention when multiple workers timeout simultaneously.
//
// The default value is [DefaultFunnelPoolIdleJitter].
func WithIdleJitter(jitter time.Duration) IdleJitterOption {
	return opts.IdleJitter(jitter)
}

type IdleJitterOption interface {
	FunnelPoolOption
}
