// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
)

//nolint:mnd // default configuration
var defaultLimiterConfig = LimiterConfig{
	Count:   BiasedIntConfig{Min: 1, Med: 3, Max: 10},
	Permits: BiasedIntConfig{Min: 1, Med: 3, Max: 10},
}

// LimiterConfig controls generation of a kind of Limiter (task-bound or
// funnel-bound). Limiters are semaphore-only in v1 — a Limiter has a
// fixed permit count drawn from Permits, and the generator never shares
// a Limiter across op kinds (a TaskLimiter binds only to Launchers; a
// FunnelLimiter binds only to Funnels). Both restrictions lift
// post-Wave-4.
type LimiterConfig struct {
	Count   BiasedIntConfig
	Permits BiasedIntConfig
}

// Limiter represents a sim-level semaphore-style concurrency limiter
// that binds to one or more ops of a single kind.
type Limiter struct {
	ID      int
	Permits int
}

// Format implements fmt.Formatter for pretty-printing.
func (l *Limiter) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		_, _ = fmt.Fprintf(f, "Limiter#%d: permits=%d", l.ID, l.Permits)
	} else {
		_, _ = fmt.Fprintf(f, "Limiter#%d", l.ID)
	}
}
