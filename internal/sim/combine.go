// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

var defaultCombineConfig = CombineConfig{
	Count: BiasedIntConfig{Min: 1, Med: 5, Max: 20},
	Func: FuncConfig{
		SelfTime: BiasedDurationConfig{Min: 0, Med: 10 * time.Microsecond, Max: 10 * time.Millisecond},
		Subjob: FuncSubjobConfig{
			Add: BiasedBoolConfig{Probability: 0.01},
		},
		ReturnError: BiasedBoolConfig{Probability: 0.05},
	},
	ScatterCount: BiasedIntConfig{Min: 1, Med: 2, Max: 10},
	Flush:        BiasedBoolConfig{Probability: 0.05},
}

type CombineConfig struct {
	Count        BiasedIntConfig
	Func         FuncConfig
	ScatterCount BiasedIntConfig
	Flush        BiasedBoolConfig
}

// Combine represents a simulated combine
type Combine struct {
	ID           int
	Index        int
	Func         *Func
	FlushHandler ResultHandler
	pathDuration time.Duration
}

var _ ResultHandler = &Combine{}

func (c *Combine) PathDuration() time.Duration {
	return c.pathDuration
}

// Format implements fmt.Formatter for pretty-printing a combine hierarchy.
func (c *Combine) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if fs.Flag('#') {
		c.Dump(fs, "")
	} else {
		_, _ = fmt.Fprintf(fs, "Combine#%d", c.ID)
	}
}

func (c *Combine) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(c)
	_, _ = fmt.Fprintf(fs, "%s: index=%d flush=%v\n%s", name, c.Index, c.FlushHandler, indent)
	c.Func.Dump(fs, indent, name)
	_, _ = fmt.Fprintf(fs, "\n%s%s ends at %v", indent, name, c.PathDuration())
}
