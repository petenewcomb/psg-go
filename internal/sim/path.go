// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
)

// Path tracks generator state during backward path construction.
// RemainingLength counts how many more upstream ops must still be
// added before this path's origin is committed; RootRunner is the
// most-recently-added upstream op (initially the leaf TaskRunner;
// finally the top-level origin TaskRunner whose StartTask dispatch
// goes into Plan.Steps).
type Path struct {
	RemainingLength int
	RootRunner      *TaskRunner
}

func (p *Path) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	_, _ = fmt.Fprintf(fs, "Path{%d, %v}", p.RemainingLength, p.RootRunner)
}
