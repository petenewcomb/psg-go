// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

type FuncConfig struct {
	SelfTime    BiasedDurationConfig
	Subjob      FuncSubjobConfig
	ReturnError BiasedBoolConfig
}

type FuncSubjobConfig struct {
	Add BiasedBoolConfig
}

// Func represents the contents of a simulated task, combine, or gather function
type Func struct {
	Steps       []Step
	ReturnError bool
}

func (f *Func) Dump(fs fmt.State, indent string, name string) {
	var t time.Duration
	for i, s := range f.Steps {
		_, _ = fmt.Fprintf(fs, "%s step %d/%d (+%v): ", name, i+1, len(f.Steps)+1, t)
		s.Dump(fs, indent)
		_, _ = fmt.Fprintf(fs, "\n%s", indent)
		t += s.Duration()
	}
	var returnValue string
	if f.ReturnError {
		returnValue = "error"
	} else {
		returnValue = "nil"
	}
	_, _ = fmt.Fprintf(fs, "%s step %d/%d (+%v): return %s", name, len(f.Steps)+1, len(f.Steps)+1, t, returnValue)
}
