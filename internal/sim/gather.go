// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

var defaultGatherConfig = GatherConfig{
	Count: BiasedIntConfig{Min: 1, Med: 5, Max: 20},
	Func: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 10 * time.Microsecond, Max: 10 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnError: BiasedBoolConfig{Probability: 0.05},
	},
	ScatterCount: BiasedIntConfig{Min: 1, Med: 2, Max: 10},
	Concurrency:  BiasedIntConfig{Min: 1, Med: 1, Max: 3},
}

type GatherConfig struct {
	Count        BiasedIntConfig
	Func         FuncConfig
	ScatterCount BiasedIntConfig
	Concurrency  BiasedIntConfig
}

// Gather represents a simulated gather
type Gather struct {
	ID           int
	Index        int
	Func         *Func
	pathDuration time.Duration
}

var _ ResultHandler = &Gather{}

func (g *Gather) PathDuration() time.Duration {
	return g.pathDuration
}

// Format implements fmt.Formatter for pretty-printing a gather hierarchy.
func (g *Gather) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		g.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Gather#%d", g.ID)
	}
}

func (g *Gather) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(g)
	_, _ = fmt.Fprintf(fs, "%s: index=%d\n%s", name, g.Index, indent)
	g.Func.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, g.PathDuration())
}
