// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
)

type Path struct {
	RemainingLength int
	RootTask        *Task
}

func (p *Path) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	_, _ = fmt.Fprintf(fs, "Path{%d, %v}", p.RemainingLength, p.RootTask)
}
