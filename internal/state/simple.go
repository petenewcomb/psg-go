// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

// `Simple` is a single-threaded implementation of the `psg.inFlightCounter`
// interface, suitable when all scatter and gather calls in the associated
// `psg.job` environment will be made from the same goroutine.
type SimpleInFlightCounter int64

func (c *SimpleInFlightCounter) Increment() {
	*c++
}

func (c *SimpleInFlightCounter) IncrementIfUnder(limit int) bool {
	if int64(*c) < int64(limit) {
		*c++
		return true
	}
	return false
}

func (c *SimpleInFlightCounter) Decrement() bool {
	*c--
	if *c < 0 {
		panic("no tasks in flight")
	}
	return *c == 0
}

func (c *SimpleInFlightCounter) GreaterThanZero() bool {
	return *c > 0
}
