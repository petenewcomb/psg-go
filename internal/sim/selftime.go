// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// SelfTime represents time spent processing or waiting within a body.
// Dist captures a distribution for per-invocation duration draws. In
// Deterministic mode the distribution collapses to a fixed value
// (Min = Med = Max); in probabilistic mode each invocation draws a
// fresh duration, exercising timing variance for race exposure.
//
// SelfTime has no Prob field — it is the only Step variant that always
// fires. A SelfTime of zero duration (or near-zero) is the equivalent
// of a "skipped" step.
type SelfTime struct {
	Dist BiasedDurationConfig
}

var _ Step = SelfTime{}

// Duration returns the median (Med) duration — the value the v1
// runtime uses for SelfTime sleeps. Path-budget accounting and
// MaxPathDuration assertions are pinned to this value. When the
// runtime gains per-invocation Dist draws (probabilistic mode), this
// should switch to Min and the path-duration assertion will need to
// allow per-invocation undershoot.
func (st SelfTime) Duration() time.Duration {
	return st.Dist.Med
}

func (st SelfTime) Dump(fs fmt.State, indent string) {
	if st.Dist.Min == st.Dist.Max {
		_, _ = fmt.Fprintf(fs, "%v self time", st.Dist.Med)
	} else {
		_, _ = fmt.Fprintf(fs, "self time ~[%v,%v,%v]", st.Dist.Min, st.Dist.Med, st.Dist.Max)
	}
}
