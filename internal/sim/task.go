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
	Parent                *Task
	SelfTimes             []time.Duration // one more entry than Subjobs
	Subjobs               []*Plan
	GatherTimes           []time.Duration // one more entry than Children
	Children              []*Task
	ReturnErrorFromTask   bool
	ReturnErrorFromGather bool
	PathDurationAtTaskEnd time.Duration
}

func (t *Task) ParentGatherDuration() time.Duration {
	if t.Parent == nil {
		return 0
	}
	var d time.Duration
	for i, gt := range t.Parent.GatherTimes {
		if t.Parent.Children[i] == t {
			break
		}
		d += gt
	}
	return d
}

func (t *Task) TaskDuration() time.Duration {
	var d time.Duration
	for _, st := range t.SelfTimes {
		d += st
	}
	for _, sj := range t.Subjobs {
		d += sj.MaxPathDuration
	}
	return d
}

func (t *Task) GatherDuration() time.Duration {
	var d time.Duration
	for _, gt := range t.GatherTimes {
		d += gt
	}
	return d
}

// Format implements fmt.Formatter for pretty-printing a task hierarchy.
func (t *Task) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		t.formatInternal(f, "")
	} else {
		_, _ = fmt.Fprintf(f, "Task#%d", t.ID)
	}
}

func (t *Task) formatInternal(f fmt.State, indent string) {
	_, _ = fmt.Fprintf(f, "Task#%d: pool=%d minTaskEnd=%v", t.ID, t.Pool, t.PathDurationAtTaskEnd)
	t.formatSteps(f, indent+"  ")
}

func (t *Task) formatSteps(f fmt.State, indent string) {
	if len(t.SelfTimes) > 0 {
		for i, subjob := range t.Subjobs {
			_, _ = fmt.Fprintf(f, "\n%s%v self time\n%s", indent, t.SelfTimes[i], indent)
			subjob.formatInternal(f, indent+"  ")
		}
		_, _ = fmt.Fprintf(f, "\n%s%v self time", indent, t.SelfTimes[len(t.SelfTimes)-1])
	}
	if len(t.GatherTimes) > 0 {
		for i, child := range t.Children {
			_, _ = fmt.Fprintf(f, "\n%s%v gather self time\n%s", indent, t.GatherTimes[i], indent)
			child.formatInternal(f, indent+"  ")
		}
		_, _ = fmt.Fprintf(f, "\n%s%v gather self time", indent, t.GatherTimes[len(t.GatherTimes)-1])
	}
}
