// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

//nolint:mnd // default configuration
var defaultFunnelConfig = FunnelConfig{
	Count: BiasedIntConfig{Min: 1, Med: 5, Max: 20},
	Accumulate: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 1 * time.Microsecond, Max: 1 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnErrorProb: 0.05,
	},
	Flush: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 1 * time.Microsecond, Max: 1 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnErrorProb: 0.05,
	},
	ScatterCount: BiasedIntConfig{Min: 0, Med: 1, Max: 3},
}

type FunnelConfig struct {
	Count        BiasedIntConfig
	Accumulate   FuncConfig
	Flush        FuncConfig
	ScatterCount BiasedIntConfig
}

// Funnel represents a simulated stateful aggregation op. Accumulate
// is invoked per input via the Pool's funnel-pool workers; Flush is
// invoked on op-close (and may also be invoked mid-stream if Accumulate
// requests it through the API). No FlushHandler field — downstream
// routing happens via explicit Submit steps inside Accumulate's or
// Flush's body.
type Funnel struct {
	ID             int
	Depth          int
	LimiterIndexes []int // indexes into Plan.FunnelLimiters (v1: at most one)
	Accumulate     *Func
	Flush          *Func
	pathDuration   time.Duration
}

func (c *Funnel) PathDuration() time.Duration {
	return c.pathDuration
}

// Format implements fmt.Formatter for pretty-printing.
func (c *Funnel) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		c.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Funnel#%d", c.ID)
	}
}

func (c *Funnel) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(c)
	_, _ = fmt.Fprintf(fs, "%s: depth=%d limiters=%v\n%s", name, c.Depth, c.LimiterIndexes, indent)
	c.Accumulate.Dump(fs, indent, name+".Accumulate")
	_, _ = fmt.Fprintf(fs, "\n%s", indent)
	c.Flush.Dump(fs, indent, name+".Flush")
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, c.pathDuration)
}
