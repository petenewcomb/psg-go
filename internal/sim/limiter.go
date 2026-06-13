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
	// KEEP 0 until the orphaned-drain-duty deadlock is resolved (see
	// REVIEW_FINDINGS.md Finding 10): with cross-subjob sharing enabled,
	// a hold-through result post into pool A's full skim queue + A's
	// driver parked inside a deeper subjob B + B gated on the shared
	// limiter closes a three-pool cycle the suspend brackets do not
	// dissolve. Trace-confirmed with -rapid.seed=15905911232756343239.
	Inherit: BiasedBoolConfig{Probability: 0},
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
	// Inherit is the probability that a limiter generated for a nested
	// (subjob) Plan aliases a same-kind limiter of the parent Plan
	// instead of being fresh — sharing the parent's psg.Limiter and
	// concurrency tracker across the subjob boundary. Ignored at top
	// level.
	Inherit BiasedBoolConfig
}

// Limiter represents a sim-level semaphore-style concurrency limiter
// that binds to one or more ops of a single kind.
type Limiter struct {
	ID      int
	Permits int
	// InheritFromParent, when >= 0, marks this entry as an alias of the
	// parent Plan's same-kind limiter at that index: the runtime shares
	// the parent's psg.Limiter and its concurrency tracker instead of
	// constructing fresh ones, exercising permit-holding across the
	// subjob boundary (the shared-limiter deadlock witnesses in
	// docs/limiter-suspend-resume.md). -1 means fresh. ID and Permits
	// mirror the parent entry for Dump clarity.
	InheritFromParent int
}

// Format implements fmt.Formatter for pretty-printing.
func (l *Limiter) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		if l.InheritFromParent >= 0 {
			_, _ = fmt.Fprintf(f, "Limiter#%d: permits=%d (inherits parent[%d])",
				l.ID, l.Permits, l.InheritFromParent)
		} else {
			_, _ = fmt.Fprintf(f, "Limiter#%d: permits=%d", l.ID, l.Permits)
		}
	} else {
		_, _ = fmt.Fprintf(f, "Limiter#%d", l.ID)
	}
}
