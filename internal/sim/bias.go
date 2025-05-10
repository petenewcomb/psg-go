// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"

	"pgregory.net/rapid"
)

type BiasedIntConfig struct {
	Min int
	Med int
	Max int
}

func (c *BiasedIntConfig) Draw(t *rapid.T, name string) int {
	if c.Med < c.Min || c.Max < c.Med {
		panic(fmt.Sprint("invalid BiasedIntConfig:", *c))
	}
	return rapid.Custom(func(t *rapid.T) int {
		// Generate a value in the range [min-med, max-med] instead of [min,
		// max] to take advantage of rapid's bias toward generating numbers near
		// zero as well as at the provided bounds.
		return c.Med + rapid.IntRange(c.Min-c.Med, c.Max-c.Med).Draw(t, name+"(internal)")
	}).Draw(t, name)
}

type BiasedDurationConfig struct {
	Min time.Duration
	Med time.Duration
	Max time.Duration
}

func (c *BiasedDurationConfig) Draw(t *rapid.T, name string) time.Duration {
	if c.Med < c.Min || c.Max < c.Med {
		panic(fmt.Sprint("invalid BiasedDurationConfig:", *c))
	}
	return rapid.Custom(func(t *rapid.T) time.Duration {
		// Generate a value in the range [min-med, max-med] instead of [min,
		// max] to take advantage of rapid's bias toward generating numbers near
		// zero as well as at the provided bounds.
		return c.Med + time.Duration(rapid.Int64Range(int64(c.Min-c.Med), int64(c.Max-c.Med)).
			Draw(t, name+"(internal)"))
	}).Draw(t, name)
}

type BiasedBoolConfig struct {
	Probability float64
}

func (c BiasedBoolConfig) Draw(t *rapid.T, name string) bool {
	t.Logf("BiasedBoolConfig: %s", name)
	p := c.Probability
	// Always calling rapid ensures that the produced value is always recorded,
	// even if p == 1.
	return rapid.Custom(func(t *rapid.T) bool {
		t.Logf("BiasedBoolConfig1: %s", name)
		// Generate a value in the range [0.5, 0.5) instead of [0, 1) to take
		// advantage of rapid's bias toward generating numbers near zero as well
		// as at the provided bounds.
		val := 0.5 + rapid.Float64Range(-0.5, 0.5).Draw(t, name+"(internal)")
		/*
			Filter(func(val float64) bool {
				t.Logf("BiasedBoolConfig2: %s: %v", name, val)
				// Exclude 0.5 to make the upper bound non-inclusive. This
				// ensures that val is always less than 1.0, and therefore if p
				// == 1.0 the returned boolean value will always be true.
				return val < 0.5
			}).Draw(t, name+"(internal)")
		*/
		t.Logf("BiasedBoolConfig3: %s: %v", name, val)
		return val <= p
	}).Draw(t, name)
}
