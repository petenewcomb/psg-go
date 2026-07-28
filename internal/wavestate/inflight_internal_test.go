// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package wavestate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClaimZeroInterlock covers the counter-level serialization between a
// zero-crossing transition (ClaimZero/ReleaseClaim) and the conditional pin
// (IncrementUnlessClaimed): a claim excludes pins, a pin excludes the claim, and a
// stray increment during a claim survives the release arithmetically.
func TestClaimZeroInterlock(t *testing.T) {
	chk := require.New(t)
	var c InFlightCounter

	// A pin on an unclaimed zero count succeeds (idle-but-open wave) and then
	// excludes any claim until released.
	chk.True(c.IncrementUnlessClaimed())
	chk.False(c.ClaimZero(), "claim must fail while a pin is outstanding")
	chk.True(c.Decrement())

	// A claim on a zero count succeeds and then refuses pins until released.
	chk.True(c.ClaimZero())
	chk.False(c.IncrementUnlessClaimed(), "pin must fail while the count is claimed")
	chk.False(c.ClaimZero(), "claims are mutually exclusive")
	c.ReleaseClaim()
	chk.True(c.IsZero())

	// A (misuse-class) unconditional increment during a claim is preserved across
	// the release rather than lost or unclaiming the counter.
	chk.True(c.ClaimZero())
	c.Increment()
	chk.False(c.IncrementUnlessClaimed(), "stray increment must not unclaim the counter")
	c.ReleaseClaim()
	chk.False(c.IsZero())
	chk.True(c.Decrement())

	// After release, pins work again.
	chk.True(c.IncrementUnlessClaimed())
	chk.True(c.Decrement())
}
