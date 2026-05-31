// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

type SubjobConfig struct {
	MaxDepth int
}

// SubjobOpRef identifies a Plan op (TaskRunner / Funnel / Skimmer)
// inside a Subjob's nested Plan that the parent body is permitted to
// reference. The runtime adapter registers these into a parent-visible
// handle table when the Subjob step starts, letting parent bodies call
// Start / Submit on the subjob's ops while the Subjob's Pool is alive.
type SubjobOpRef struct {
	Kind  SubjobOpKind
	Index int
}

type SubjobOpKind int

const (
	SubjobOpTaskRunner SubjobOpKind = iota
	SubjobOpFunnel
	SubjobOpSkimmer
)

func (k SubjobOpKind) String() string {
	switch k {
	case SubjobOpTaskRunner:
		return "TaskRunner"
	case SubjobOpFunnel:
		return opNameFunnel
	case SubjobOpSkimmer:
		return opNameSkimmer
	default:
		return fmt.Sprintf("SubjobOpKind(%d)", k)
	}
}

// Subjob represents nested-Plan execution as a Step. The nested Plan
// runs on its own Pool (v1 — exercises cross-Pool boundary code);
// ParentExposedOps lists the subjob's ops whose handles are made
// available to the enclosing controller's parent-visible registry,
// enabling bidirectional cross-Pool Submit between parent bodies and
// subjob ops while the Subjob step is in flight.
//
// Prob is the probability the subjob actually runs on each invocation
// of the enclosing body. In Deterministic mode Prob is 1.0.
type Subjob struct {
	Prob             float64
	Plan             *Plan
	ParentExposedOps []SubjobOpRef
}

var _ Step = Subjob{}

func (sj Subjob) Duration() time.Duration {
	return sj.Plan.MaxPathDuration
}

func (sj Subjob) Dump(fs fmt.State, indent string) {
	planIndent := indent + "  "
	probPart := ""
	if sj.Prob != 1 {
		probPart = fmt.Sprintf(" p=%.3f", sj.Prob)
	}
	_, _ = fmt.Fprintf(fs, "subjob%s exposed=%v:\n%s", probPart, sj.ParentExposedOps, planIndent)
	sj.Plan.Dump(fs, planIndent)
}
