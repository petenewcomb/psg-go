// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

//nolint:mnd // default configuration
var defaultGathererConfig = GathererConfig{
	Count: BiasedIntConfig{Min: 1, Med: 5, Max: 20},
	Handle: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 1 * time.Microsecond, Max: 1 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnErrorProb: 0.05,
	},
	ScatterCount: BiasedIntConfig{Min: 1, Med: 2, Max: 10},
}

type GathererConfig struct {
	Count        BiasedIntConfig
	Handle       FuncConfig
	ScatterCount BiasedIntConfig
}

// Gatherer represents a simulated terminal-sink op. The Handle body is
// invoked when the Pool's drain pulls a value from this Gatherer's
// queue. v1 does not bind Gatherers to Limiters (the post-Wave-4 API
// will support it).
type Gatherer struct {
	ID           int
	Depth        int
	Handle       *Func
	pathDuration time.Duration
}

func (g *Gatherer) PathDuration() time.Duration {
	return g.pathDuration
}

// Format implements fmt.Formatter for pretty-printing.
func (g *Gatherer) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		g.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Gatherer#%d", g.ID)
	}
}

func (g *Gatherer) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(g)
	_, _ = fmt.Fprintf(fs, "%s: depth=%d\n%s", name, g.Depth, indent)
	g.Handle.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, g.pathDuration)
}
