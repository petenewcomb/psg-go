// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// SinkKind tells the runtime adapter which Plan-level op a Submit step
// targets — a Combiner or a Gatherer.
type SinkKind int

const (
	SinkCombiner SinkKind = iota
	SinkGatherer
)

// opNameCombiner and opNameGatherer are shared op-kind label constants
// used by SinkKind.String, SubjobOpKind.String, and run.go's
// ExpectedHandlerError plumbing.
const (
	opNameCombiner = "Combiner"
	opNameGatherer = "Gatherer"
)

func (k SinkKind) String() string {
	switch k {
	case SinkCombiner:
		return opNameCombiner
	case SinkGatherer:
		return opNameGatherer
	default:
		return fmt.Sprintf("SinkKind(%d)", k)
	}
}

// Submit represents the act of pushing a value (possibly paired with an
// error) into a downstream sink. The sink is identified by SinkKind +
// SinkIndex into the corresponding Plan.Combiners or Plan.Gatherers
// slice. Prob is the probability of firing per body invocation; in
// Deterministic mode it is 1.0.
type Submit struct {
	Prob      float64
	SinkKind  SinkKind
	SinkIndex int
	WithErr   bool
}

var _ Step = Submit{}

func (s Submit) Duration() time.Duration {
	return 0
}

func (s Submit) Dump(fs fmt.State, indent string) {
	errPart := ""
	if s.WithErr {
		errPart = ", with-err"
	}
	probPart := ""
	if s.Prob != 1 {
		probPart = fmt.Sprintf(" p=%.3f", s.Prob)
	}
	_, _ = fmt.Fprintf(fs, "submit to %s[%d]%s%s", s.SinkKind, s.SinkIndex, errPart, probPart)
}
