// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"pgregory.net/rapid"
)

// BiasedBool returns a rapid generator for boolean values biased towards true with probability p.
func BiasedBool(p float64) *rapid.Generator[bool] {
	notOne := func(v float64) bool { return v != 1 }
	return rapid.Custom(func(t *rapid.T) bool {
		return rapid.Float64Range(0, 1).Filter(notOne).Draw(t, "p") < p || p == 1.0
	})
}
