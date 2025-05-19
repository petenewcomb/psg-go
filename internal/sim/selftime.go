// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// SelfTime represents time is spent processing or waiting within a simulated
// task, combine, or gather
type SelfTime time.Duration

var _ Step = SelfTime(0)

func (st SelfTime) Duration() time.Duration {
	return time.Duration(st)
}

func (st SelfTime) Dump(fs fmt.State, indent string) {
	_, _ = fmt.Fprintf(fs, "%v self time", time.Duration(st))
}
