// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"github.com/petenewcomb/psg-go/internal/state"
)

type inFlightCounter interface {
	// Increments the counter.
	Increment()

	// Increments the counter and returns true if the incremented value of the
	// counter is less than or equal to `limit`. Otherwise returns false and
	// reverts any change it may have made to the counter.
	IncrementIfUnder(limit int) bool

	// Decrements the counter and returns true if its value has reached zero.
	// Should panic if the decremented counter is less than zero.
	Decrement() bool

	// Returns true if the counter is greater than zero.
	GreaterThanZero() bool
}

type inFlightCounterFactory func() inFlightCounter

func newSimpleInFlightCounter() inFlightCounter {
	var c state.SimpleInFlightCounter
	return &c
}

func newSyncInFlightCounter() inFlightCounter {
	return &state.SyncInFlightCounter{}
}
