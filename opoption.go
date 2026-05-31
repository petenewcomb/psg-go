// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

// OpOption configures an op (Launcher, Funnel, Skimmer) at
// construction time. Users obtain OpOption values from framework
// constructors such as [WithLimits]. The interface is closed: future
// option types will live in this package.
type OpOption interface {
	applyToOpConfig(*opConfig)
}

// opConfig accumulates the resolved settings from a constructor's
// opts list before they're stored on the op value.
type opConfig struct {
	limiters []Limiter
}

func resolveOpConfig(opts []OpOption) opConfig {
	var cfg opConfig
	for _, opt := range opts {
		opt.applyToOpConfig(&cfg)
	}
	return cfg
}

// singleLimiter returns the single Limiter from cfg.limiters or the
// zero Limiter if cfg holds none. Panics if cfg holds more than one —
// multi-Limiter composition is a forthcoming Wave 4 follow-up and is
// rejected at construction time so users see the error early instead
// of getting partial enforcement at runtime.
func (cfg opConfig) singleLimiter() Limiter {
	switch len(cfg.limiters) {
	case 0:
		return Limiter{}
	case 1:
		return cfg.limiters[0]
	default:
		panic("multi-Limiter composition is not yet implemented (Wave 4 follow-up)")
	}
}

// WithLimits binds Limiters to an op so each dispatch must acquire a
// permit from each Limiter before the work runs. In Wave 4a only one
// Limiter at a time is supported; the variadic shape exists so future
// Limiter types (NewRateLimit etc.) can be funneld without changing
// this API.
func WithLimits(limiters ...Limiter) OpOption {
	return withLimitsOption{limiters: limiters}
}

type withLimitsOption struct {
	limiters []Limiter
}

func (o withLimitsOption) applyToOpConfig(c *opConfig) {
	c.limiters = append(c.limiters, o.limiters...)
}
