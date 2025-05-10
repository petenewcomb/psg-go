// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
)

var defaultPoolConfig = PoolConfig{
	Count:            BiasedIntConfig{Min: 1, Med: 3, Max: 10},
	ConcurrencyLimit: BiasedIntConfig{Min: 1, Med: 3, Max: 10},
}

type PoolConfig struct {
	Count            BiasedIntConfig
	ConcurrencyLimit BiasedIntConfig
}

type Pool struct {
	ID               int
	ConcurrencyLimit int
}

// Format implements fmt.Formatter for pretty-printing a plan.
func (p *Pool) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		_, _ = fmt.Fprintf(f, "Pool#%d: limit=%d", p.ID, p.ConcurrencyLimit)
	} else {
		_, _ = fmt.Fprintf(f, "Pool#%d", p.ID)
	}
}
