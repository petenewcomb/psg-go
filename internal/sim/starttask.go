// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// StartTask represents the act of dispatching a TaskRunner with a
// freshly-constructed argument value. The runner is identified by
// RunnerIndex into Plan.TaskRunners. Prob is the probability of firing
// per body invocation; in Deterministic mode it is 1.0.
//
// StartTask is the new vocabulary's replacement for the old Scatter
// step. It also corresponds to the new-API TaskRunner.Start(ctx, arg)
// call. To prevent cycles, the generator only emits StartTask steps
// pointing to TaskRunners whose Depth is strictly greater than the
// containing body's owning op's Depth.
type StartTask struct {
	Prob        float64
	RunnerIndex int
}

var _ Step = StartTask{}

func (s StartTask) Duration() time.Duration {
	return 0
}

func (s StartTask) Dump(fs fmt.State, indent string) {
	probPart := ""
	if s.Prob != 1 {
		probPart = fmt.Sprintf(" p=%.3f", s.Prob)
	}
	_, _ = fmt.Fprintf(fs, "start TaskRunner[%d]%s", s.RunnerIndex, probPart)
}
