// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"
)

// Step represents an portion of the body of a Func or Plan
type Step interface {
	Duration() time.Duration
	Dump(f fmt.State, indent string)
}
