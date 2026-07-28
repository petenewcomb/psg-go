// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

//nolint:mnd // default configuration
var defaultLauncherConfig = LauncherConfig{
	Body: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 10 * time.Microsecond, Max: 10 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.05},
		},
		ReturnErrorProb: 0.05,
	},
	ScatterCount: BiasedIntConfig{Min: 0, Med: 1, Max: 2},
}

type LauncherConfig struct {
	Body FuncConfig
	// ScatterCount is how many terminal fan-out tasks a launcher body dispatches
	// via StartTask (task-to-task scatter into the same wave). Targets are
	// Submit-only leaves, so the graph stays acyclic; sharing a task limiter with
	// the dispatching launcher exercises the self-acquisition shape.
	ScatterCount BiasedIntConfig
}

// Launcher represents a simulated stateless dispatch op. Its Body is
// invoked once per Start dispatch; the body may execute SelfTime,
// Subjob, StartTask, and Submit steps. Each Launcher binds to zero
// or more TaskLimiters.
type Launcher struct {
	ID             int
	Depth          int   // higher than any Launcher this body may StartTask, prevents cycles
	LimiterIndexes []int // indexes into Plan.TaskLimiters; joint AND-composed at dispatch
	// LimiterWeights[i] is the per-dispatch weight for LimiterIndexes[i]: in [1, permits]
	// for a weighted limiter, 1 for a plain one. Parallel to LimiterIndexes.
	LimiterWeights []int
	Body           *Func
	pathDuration   time.Duration
}

func (r *Launcher) PathDuration() time.Duration {
	return r.pathDuration
}

// Format implements fmt.Formatter for pretty-printing.
func (r *Launcher) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		r.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Launcher#%d", r.ID)
	}
}

func (r *Launcher) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(r)
	_, _ = fmt.Fprintf(fs, "%s: depth=%d limiters=%v\n%s", name, r.Depth, r.LimiterIndexes, indent)
	r.Body.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, r.pathDuration)
}
