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

// Subjob represents time is spent processing or waiting within a simulated
// task, combine, or gather
type Subjob struct {
	Plan *Plan
}

var _ Step = Subjob{}

func (sj Subjob) Duration() time.Duration {
	return sj.Plan.MaxPathDuration
}

func (sj Subjob) Dump(fs fmt.State, indent string) {
	planIndent := indent + "  "
	_, _ = fmt.Fprintf(fs, "subjob:\n%s", planIndent)
	sj.Plan.Dump(fs, planIndent)
}
