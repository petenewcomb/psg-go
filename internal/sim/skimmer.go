// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

//nolint:mnd // default configuration
var defaultSkimmerConfig = SkimmerConfig{
	Count: BiasedIntConfig{Min: 1, Med: 5, Max: 20},
	Handle: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 1 * time.Microsecond, Max: 1 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnErrorProb: 0.05,
	},
	ScatterCount: BiasedIntConfig{Min: 0, Med: 1, Max: 3},
	MaxDepth:     2,
}

// SkimmerConfig controls Skimmer generation. MaxDepth bounds the
// Skimmer-cascade chain length: a non-terminal Skimmer's StartTasks
// dispatch fan-out runners targeting Skimmers at strictly greater
// depth, so a value can pass through up to MaxDepth Skimmers before
// terminating. Skimmers at depth == MaxDepth are terminal (no
// StartTasks). MaxDepth=0 disables scatter-from-skim entirely.
type SkimmerConfig struct {
	Count        BiasedIntConfig
	Handle       FuncConfig
	ScatterCount BiasedIntConfig
	MaxDepth     int
}

// Skimmer represents a simulated terminal-sink op. The Handle body is
// invoked when the Pool's drain pulls a value from this Skimmer's
// queue. v1 does not bind Skimmers to Limiters (the post-Wave-4 API
// will support it).
type Skimmer struct {
	ID           int
	Depth        int
	Handle       *Func
	pathDuration time.Duration
}

func (g *Skimmer) PathDuration() time.Duration {
	return g.pathDuration
}

// Format implements fmt.Formatter for pretty-printing.
func (g *Skimmer) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		g.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Skimmer#%d", g.ID)
	}
}

func (g *Skimmer) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(g)
	_, _ = fmt.Fprintf(fs, "%s: depth=%d\n%s", name, g.Depth, indent)
	g.Handle.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, g.pathDuration)
}
