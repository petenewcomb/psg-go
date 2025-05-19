// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

var defaultTaskConfig = TaskConfig{
	Func: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 100 * time.Microsecond, Max: 100 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.05},
		},
		ReturnError: BiasedBoolConfig{Probability: 0.05},
	},
	UseCombine: BiasedBoolConfig{Probability: 0.3},
}

type TaskConfig struct {
	Func       FuncConfig
	UseCombine BiasedBoolConfig
}

// Task represents a simulated task
type Task struct {
	ID            int
	PoolIndex     int
	Func          *Func
	ResultHandler ResultHandler
	pathDuration  time.Duration
}

type ResultHandler interface {
	PathDuration() time.Duration
	Dump(f fmt.State, indent string)
}

func (t *Task) PathDuration() time.Duration {
	return t.pathDuration
}

// Format implements fmt.Formatter for pretty-printing a task hierarchy.
func (t *Task) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		t.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Task#%d", t.ID)
	}
}

func (t *Task) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(t)
	_, _ = fmt.Fprintf(fs, "%s: pool=%d\n%s", name, t.PoolIndex, indent)
	t.Func.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, t.pathDuration)
	resultHandlerIndent := indent + "  "
	if t.ResultHandler != nil {
		_, _ = fmt.Fprintf(fs, "\n%s", resultHandlerIndent)
		t.ResultHandler.Dump(fs, resultHandlerIndent)
	}
}
