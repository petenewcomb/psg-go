// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// Task represents a simulated task and its gathering
type Task struct {
	ID                    int
	Pool                  int
	SelfTimes             []time.Duration // one more entry than Subjobs
	Subjobs               []*Plan
	GatherTimes           []time.Duration // one more entry than Children
	Children              []*Task
	ReturnErrorFromTask   bool
	ReturnErrorFromGather bool
}

// Format implements fmt.Formatter for pretty-printing a task hierarchy.
func (t *Task) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		t.formatInternal(f, "")
	} else {
		fmt.Fprintf(f, "Task#%d", t.ID)
	}
}

func (t *Task) formatInternal(f fmt.State, indent string) {
	fmt.Fprintf(f, "Task#%d: pool=%d", t.ID, t.Pool)
	t.formatSteps(f, indent+"  ")
}

func (t *Task) formatSteps(f fmt.State, indent string) {
	if len(t.SelfTimes) > 0 {
		for i, subjob := range t.Subjobs {
			fmt.Fprintf(f, "\n%s%v self time\n%s", indent, t.SelfTimes[i], indent)
			subjob.formatInternal(f, indent+"  ")
		}
		fmt.Fprintf(f, "\n%s%v self time", indent, t.SelfTimes[len(t.SelfTimes)-1])
	}
	if len(t.GatherTimes) > 0 {
		for i, child := range t.Children {
			fmt.Fprintf(f, "\n%s%v gather self time\n%s", indent, t.GatherTimes[i], indent)
			child.formatInternal(f, indent+"  ")
		}
		fmt.Fprintf(f, "\n%s%v gather self time", indent, t.GatherTimes[len(t.GatherTimes)-1])
	}
}
