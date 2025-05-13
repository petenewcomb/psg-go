// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
)

var defaultTaskPoolConfig = TaskPoolConfig{
	Count:            BiasedIntConfig{Min: 1, Med: 3, Max: 10},
	ConcurrencyLimit: BiasedIntConfig{Min: 1, Med: 3, Max: 10},
}

type TaskPoolConfig struct {
	Count            BiasedIntConfig
	ConcurrencyLimit BiasedIntConfig
}

type TaskPool struct {
	ID               int
	ConcurrencyLimit int
}

// Format implements fmt.Formatter for pretty-printing a plan.
func (p *TaskPool) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		_, _ = fmt.Fprintf(f, "TaskPool#%d: limit=%d", p.ID, p.ConcurrencyLimit)
	} else {
		_, _ = fmt.Fprintf(f, "TaskPool#%d", p.ID)
	}
}
