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
}

type LauncherConfig struct {
	Body FuncConfig
}

// Launcher represents a simulated stateless dispatch op. Its Body is
// invoked once per Start dispatch; the body may execute SelfTime,
// Subjob, StartTask, and Submit steps. Each Launcher binds to zero
// or one TaskLimiter for v1 (multi-limiter binding lifts post-Wave-4).
type Launcher struct {
	ID             int
	Depth          int   // higher than any Launcher this body may StartTask, prevents cycles
	LimiterIndexes []int // indexes into Plan.TaskLimiters (v1: at most one)
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
