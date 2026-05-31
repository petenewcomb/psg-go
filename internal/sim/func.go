// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// FuncConfig controls generation of a Func body (the static description
// of what a task/accumulate/handle execution will do).
type FuncConfig struct {
	SelfTime        BiasedDurationConfig
	Subjob          FuncSubjobConfig
	ReturnErrorProb float64
}

type FuncSubjobConfig struct {
	Add BiasedBoolConfig
}

// Func represents the body of a simulated TaskRunner, Funnel
// Accumulate, Funnel Flush, or Skimmer Handle. ReturnErrorProb is
// the probability that the body returns a non-nil error on each
// invocation; in Deterministic mode it is forced to 0.0 or 1.0.
type Func struct {
	Steps           []Step
	ReturnErrorProb float64
}

// ReturnsError reports whether this body is configured to ever return
// an error. Used by assertion-bound calculations and for routing in
// Deterministic mode.
func (f *Func) ReturnsError() bool {
	return f.ReturnErrorProb > 0
}

func (f *Func) Dump(fs fmt.State, indent, name string) {
	var t time.Duration
	for i, s := range f.Steps {
		_, _ = fmt.Fprintf(fs, "%s step %d/%d (+%v): ", name, i+1, len(f.Steps)+1, t)
		s.Dump(fs, indent)
		_, _ = fmt.Fprintf(fs, "\n%s", indent)
		t += s.Duration()
	}
	var returnValue string
	switch f.ReturnErrorProb {
	case 0:
		returnValue = "nil"
	case 1:
		returnValue = "error"
	default:
		returnValue = fmt.Sprintf("error(p=%.3f)", f.ReturnErrorProb)
	}
	_, _ = fmt.Fprintf(fs, "%s step %d/%d (+%v): return %s", name, len(f.Steps)+1, len(f.Steps)+1, t, returnValue)
}
