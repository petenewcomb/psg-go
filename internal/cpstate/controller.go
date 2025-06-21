// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cpstate

import (
	"fmt"
	"math"
	"slices"
	"time"
)

type controllerConfig struct {
	MinConcurrency           int
	MaxConcurrency           int // -1 means unlimited
	HighUtilThreshold        float64
	MinThroughputROI         float64
	AggressiveGrowthFactor   float64
	ConservativeGrowthFactor float64
}

type controller struct {
	samples          []perfSample
	oldestSampleTime time.Time

	// Configuration parameters
	config controllerConfig
}

type perfSample struct {
	Time           time.Time
	GoroutineCount int
	Throughput     float64
	SpareUtil      float64
}

// Format implements fmt.Formatter
func (c *controller) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	_, _ = fmt.Fprint(fs, "[")
	sep := ""
	valley := c.findUtilizationValley()
	knee := c.findThroughputKnee()
	for i, s := range c.samples {
		label := ""
		switch {
		case i == valley && i == knee:
			label = "(v,k)"
		case i == valley:
			label = "(v)"
		case i == knee:
			label = "(k)"
		}
		_, _ = fmt.Fprintf(fs, "%s%d%s: %.2f@%.0f%%", sep, s.GoroutineCount, label, s.Throughput*float64(time.Second), s.SpareUtil*100)
		sep = ", "
	}
	_, _ = fmt.Fprint(fs, "]")
}

// SetConfig atomically applies a complete controller configuration.
func (c *controller) SetConfig(config controllerConfig) {
	c.config = config
}

func (c *controller) Reset() {
	c.samples = c.samples[:0]
	c.oldestSampleTime = time.Time{}
}

func (c *controller) AddSample(retentionPeriod time.Duration, s perfSample) {
	// Find where this sample should be inserted/updated in the sorted slice
	newSampleIndex := c.findByGoroutineCount(s.GoroutineCount)
	if newSampleIndex < len(c.samples) && s.GoroutineCount == c.samples[newSampleIndex].GoroutineCount {
		c.samples[newSampleIndex] = s
	}

	valleyIndex := c.findUtilizationValley()
	kneeIndex := c.findThroughputKnee()
	if valleyIndex == len(c.samples) && kneeIndex == len(c.samples) {
		for i := range c.samples {
			c.samples[i].Time = s.Time
		}
	} else {
		refreshAroundInflectionPoint := func(inflectionPointIndex int) {
			for i := max(0, inflectionPointIndex-2); i <= min(inflectionPointIndex+2, len(c.samples)-1); i++ {
				c.samples[i].Time = s.Time
			}
		}
		if valleyIndex < len(c.samples) {
			refreshAroundInflectionPoint(valleyIndex)
		}
		if kneeIndex < len(c.samples) {
			refreshAroundInflectionPoint(kneeIndex)
		}
	}

	oldestValidTime := s.Time.Add(-retentionPeriod)
	if !c.oldestSampleTime.IsZero() && !c.oldestSampleTime.Before(oldestValidTime) {
		// Existing samples still valid, just need to insert if new.
		if newSampleIndex == len(c.samples) || s.GoroutineCount != c.samples[newSampleIndex].GoroutineCount {
			c.samples = slices.Insert(c.samples, newSampleIndex, s)
		}
		return
	}

	// Scan to expire old samples while also inserting new sample
	c.oldestSampleTime = s.Time
	j := 0
	append := func(i int, s perfSample) {
		if j != i {
			if j < len(c.samples) {
				c.samples[j] = s
			} else {
				c.samples = append(c.samples, s)
			}
		}
		j++
		if s.Time.Before(c.oldestSampleTime) {
			c.oldestSampleTime = s.Time
		}
	}
	for i, es := range c.samples {
		if i == newSampleIndex {
			// Insert new sample or replace existing one with same goroutine count
			if es.GoroutineCount == s.GoroutineCount {
				// Replace existing sample with same goroutine count
				append(i, s)
			} else {
				// Insert new sample before existing one
				append(-1, s)
			}
		} else if !es.Time.Before(oldestValidTime) {
			// Keep existing sample that hasn't expired
			append(i, es)
		}
	}
	if newSampleIndex == len(c.samples) {
		append(-1, s)
	}
	c.samples = c.samples[:j]
}

func (c *controller) findByGoroutineCount(goroutineCount int) int {
	i, _ := slices.BinarySearchFunc(
		c.samples,
		goroutineCount,
		func(s perfSample, goroutineCount int) int {
			return s.GoroutineCount - goroutineCount
		},
	)
	return i
}

func (c *controller) RecommendTarget() int {
	target := c.calculateBestTarget()
	switch {
	case target < c.config.MinConcurrency:
		return c.config.MinConcurrency
	case c.config.MaxConcurrency >= 0 && target > c.config.MaxConcurrency:
		return c.config.MaxConcurrency
	default:
		return target
	}
}

func (c *controller) calculateBestTarget() int {

	valleyIndex := c.findUtilizationValley()
	kneeIndex := c.findThroughputKnee()
	switch {

	case valleyIndex < len(c.samples):
		// Non-high utilization valley found: fine-tune from best known point
		baseIndex := min(valleyIndex, kneeIndex)
		if baseIndex < len(c.samples)-1 {
			return c.scaleUpWithinGapAt(baseIndex)
		} else {
			return c.scaleUpConservatively()
		}

	case kneeIndex == len(c.samples):
		if len(c.samples) < 2 {
			// Can't determine throughput growth with less than two samples:
			// explore a bit higher
			return c.scaleUpConservatively()
		} else {
			// All samples are high utilization and exhibit good throughput
			// growth: explore much higher
			return c.scaleUpAggressively()
		}

	case kneeIndex == 0: // above already ensures that len(c.samples) > 0
		// All samples are high utilization but show at best subpar throughput
		// growth: explore lower
		return c.scaleDown()

	default: // above already ensures that 0 < kneeIndex < len(c.samples)
		// Throughput knee found but all samples have high utilization:
		// fine-tune upward from the knee
		return c.scaleUpWithinGapAt(kneeIndex)
	}
}

// Finds the index of the first non-high-utilization sample or len(c.samples)
// if all samples are high
func (c *controller) findUtilizationValley() int {
	for i, s := range c.samples {
		if s.SpareUtil < c.config.HighUtilThreshold {
			return i
		}
	}
	return len(c.samples)
}

// scaleUpAggressively returns a goroutine count significantly higher relative
// to the goroutine count of the highest-goroutine (last) existing sample, or 1
// if there are no existing samples.
func (c *controller) scaleUpAggressively() int {
	if len(c.samples) == 0 {
		// No samples available, start with 1 goroutine
		return 1
	}
	return c.scaleUpByFactor(len(c.samples)-1, c.config.AggressiveGrowthFactor)
}

// scaleUpConservatively returns a goroutine count moderately higher relative to
// the goroutine count of the sample at the given base index, but only if the
// throughput of the sample is greater than that of the preceding sample. In the
// latter case, it returns the goroutine count of original sample.
func (c *controller) scaleUpConservatively() int {
	baseIndex := len(c.samples) - 1
	if baseIndex > 0 && !c.throughputStillImprovingAt(baseIndex) {
		return c.samples[baseIndex].GoroutineCount // Stay put
	}
	return c.scaleUpByFactor(baseIndex, c.config.ConservativeGrowthFactor)
}

func (c *controller) scaleUpByFactor(baseIndex int, factor float64) int {
	baseGC := 0
	if baseIndex >= 0 {
		baseGC = c.samples[baseIndex].GoroutineCount
	}
	return max(baseGC+1, int(math.Round(float64(baseGC)*factor)))
}

// scaleUpWithinGapAt returns a goroutine count between that of the sample at
// the given base index and that of the following sample, but only if the
// throughput of the first sample is greater than that of the preceding one.
// Returns the goroutine count at the base index in the latter case or if there
// is no gap between it and that of the following sample.
func (c *controller) scaleUpWithinGapAt(baseIndex int) int {
	baseGC := c.samples[baseIndex].GoroutineCount
	if !c.throughputStillImprovingAt(baseIndex) {
		return baseGC
	}
	gapSize := c.samples[baseIndex+1].GoroutineCount - baseGC
	if gapSize == 1 {
		return baseGC
	}
	return baseGC + min(1, int(math.Round(float64(gapSize)*0.5)))
}

func (c *controller) throughputStillImprovingAt(baseIndex int) bool {
	return baseIndex > 0 && c.samples[baseIndex-1].Throughput < c.samples[baseIndex].Throughput
}

// scaleDown returns a lower goroutine count roughly half that of the
// lowest-goroutine (first) existing sample, if possible. It will never return a
// goroutine count less than 1.
func (c *controller) scaleDown() int {
	baseGC := c.samples[0].GoroutineCount
	return max(1, min(baseGC-1, int(math.Round(float64(baseGC)*0.5))))
}

/*
// findLinearEnd determines how far into the sample sequence the throughput
// remains linear from origin (throughput = slope * capacity). May be called
// only if there are at least two samples available, since a linearity test is
// not meaningful without at least three points. (The third is the origin point
// - zero throughput at zero goroutines.)
//
// Uses an incremental algorithm that maintains running statistics:
// - sumRatios/count tracks the current mean ratio (throughput/capacity)
// - minRatio/maxRatio track the historical range of ratios seen
// - Linearity test: normalized range (max-min)/mean < linearityTolerance
//
// ALGORITHM TRADEOFF: minRatio and maxRatio reflect historical extremes
// across all accepted samples, while meanRatio is recalculated each iteration.
// This means we're asking "how much spread have we seen historically, relative
// to our current best estimate of the true ratio?" Early outliers contribute
// to the range forever but get diluted in the mean as more samples are added.
//
// This asymmetry is acceptable because:
// 1. We're detecting when linear scaling breaks down, not computing precise statistics
// 2. The approach is sensitive to deviations while remaining O(n) efficient
//
// OUTLIER HANDLING: Can skip up to maxLinearitySkips samples that would break
// linearity, allowing robustness against measurement noise while remaining
// sensitive to real system behavior changes.
//
// Returns the index just past the last sample that maintains linearity from
// origin or 0 if a valid linear sequence from origin cannot be found.
func (c *controller) findLinearEnd() int {
	// Must ensure that at least two samples are not skipped, for the same
	// reason as above.
	maxSkips := min(c.maxLinearitySkips, len(c.samples)-2)

	var sumSlopes float64
	var count int
	minSlope := math.Inf(1)
	maxSlope := math.Inf(-1)
	skipsUsed := 0
	linearEnd := 0

	for i, sample := range c.samples {
		slope := sample.Throughput / float64(sample.GoroutineCount)

		// Test adding this slope
		testSum := sumSlopes + slope
		testCount := count + 1
		testMin := math.Min(minSlope, slope)
		testMax := math.Max(maxSlope, slope)

		if testCount >= 1 {
			meanSlope := testSum / float64(testCount)
			normalizedRange := (testMax - testMin) / meanSlope

			if normalizedRange > c.linearityTolerance {
				if skipsUsed < maxSkips {
					skipsUsed++
					continue
				}
				break
			}

			// Point is good
			linearEnd = i + 1
		}

		sumSlopes = testSum
		count = testCount
		minSlope = testMin
		maxSlope = testMax
	}

	return linearEnd
}
*/

func (c *controller) findThroughputKnee() int {
	for i := 0; i < len(c.samples)-1; i++ {
		if !c.rangeExhibitsAcceptableReturn(&c.samples[i], &c.samples[i+1]) {
			return i
		}
	}
	return len(c.samples)
}

func (c *controller) rangeExhibitsAcceptableReturn(lower, higher *perfSample) bool {
	slope := (higher.Throughput - lower.Throughput) / float64(higher.GoroutineCount-lower.GoroutineCount)
	returnRate := slope / lower.Throughput
	return returnRate >= c.config.MinThroughputROI
}
