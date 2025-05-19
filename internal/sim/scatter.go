// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// Scatter represents the launching of a task from within a simulated combine or
// gather
type Scatter struct {
	Task *Task
}

var _ Step = Scatter{}

func (s Scatter) Duration() time.Duration {
	return 0 // It takes "no" time to launch a task
}

func (s Scatter) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	_, _ = fmt.Fprintf(fs, "Scatter{%v}", s.Task)
}

func (s Scatter) Dump(fs fmt.State, indent string) {
	taskIndent := indent + "  "
	_, _ = fmt.Fprintf(fs, "scatter:\n%s", taskIndent)
	s.Task.Dump(fs, taskIndent)
}
