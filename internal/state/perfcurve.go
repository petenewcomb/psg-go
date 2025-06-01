// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"fmt"
	"math"
	"slices"
	"time"
)

type perfCurves struct {
	samples          []perfSample
	oldestSampleTime time.Time

	// Configuration parameters
	minConcurrency           int
	maxConcurrency           int // -1 means unlimited
	retentionPeriod          time.Duration
	highUtilThreshold        float64
	minimumReturn            float64
	aggressiveGrowthFactor   float64
	conservativeGrowthFactor float64
}

type perfSample struct {
	Time           time.Time
	GoroutineCount int
	Throughput     float64
	SecondaryUtil  float64
}

// Format implements fmt.Formatter
func (pc *perfCurves) Format(fs fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	_, _ = fmt.Fprint(fs, "[")
	sep := ""
	valley := pc.findUtilizationValley()
	knee := pc.findThroughputKnee()
	for i, s := range pc.samples {
		label := ""
		switch {
		case i == valley && i == knee:
			label = "(v,k)"
		case i == valley:
			label = "(v)"
		case i == knee:
			label = "(k)"
		}
		_, _ = fmt.Fprintf(fs, "%s%d%s: %.2f@%.0f%%", sep, s.GoroutineCount, label, s.Throughput*float64(time.Second), s.SecondaryUtil*100)
		sep = ", "
	}
	_, _ = fmt.Fprint(fs, "]")
}

// SetLimits configures the concurrency limits for the performance curve
func (pc *perfCurves) SetLimits(minConcurrency, maxConcurrency int) {
	if minConcurrency < 0 {
		panic(fmt.Sprintf("invalid minimum concurrency %d: must be >= 0", minConcurrency))
	}
	if maxConcurrency < -1 {
		panic(fmt.Sprintf("invalid maximum concurrency %d: must be >= -1", maxConcurrency))
	}
	if maxConcurrency >= 0 && minConcurrency > maxConcurrency {
		panic(fmt.Sprintf("minimum concurrency %d is greater than maximum concurrency %d", minConcurrency, maxConcurrency))
	}
	pc.minConcurrency = minConcurrency
	pc.maxConcurrency = maxConcurrency
}

// SetThresholds configures the utilization thresholds for the performance curve
func (pc *perfCurves) SetHighUtilizationThreshold(high float64) {
	if high < 0 || high > 1 {
		panic(fmt.Sprintf("invalid high utilization threshold %v: must be between 0 and 1, inclusive", high))
	}
	pc.highUtilThreshold = high
}

// SetRetentionPeriod configures how long performance samples are retained
func (pc *perfCurves) SetRetentionPeriod(d time.Duration) {
	if d <= 0 {
		panic(fmt.Sprintf("invalid retention period %v: must be > 0", d))
	}
	pc.retentionPeriod = d
}

// SetMinimumReturn configures the ratio for throughput knee detection
func (pc *perfCurves) SetMinimumReturn(ratio float64) {
	if ratio <= 0 || ratio > 1 {
		panic(fmt.Sprintf("invalid minimum return ratio %v: must be > 0 and <= 1", ratio))
	}
	pc.minimumReturn = ratio
}

// SetGrowthFactors configures the growth factors for scaling decisions
func (pc *perfCurves) SetGrowthFactors(aggressive, conservative float64) {
	if aggressive <= 1 {
		panic(fmt.Sprintf("invalid aggressive growth factor %v: must be > 1", aggressive))
	}
	if conservative <= 1 {
		panic(fmt.Sprintf("invalid conservative growth factor %v: must be > 1", conservative))
	}
	if conservative > aggressive {
		panic(fmt.Sprintf("conservative growth factor %v cannot be greater than aggressive growth factor %v", conservative, aggressive))
	}
	pc.aggressiveGrowthFactor = aggressive
	pc.conservativeGrowthFactor = conservative
}

func (pc *perfCurves) AddSample(s perfSample) {
	oldestValidTime := time.Now().Add(-pc.retentionPeriod)
	if pc.oldestSampleTime.IsZero() || pc.oldestSampleTime.Before(oldestValidTime) {
		// Scan to expire old samples
		pc.oldestSampleTime = s.Time
		j := 0
		append := func(i int, s perfSample) {
			if j != i {
				if j < len(pc.samples) {
					pc.samples[j] = s
				} else {
					pc.samples = append(pc.samples, s)
				}
			}
			j++
			if s.Time.Before(pc.oldestSampleTime) {
				pc.oldestSampleTime = s.Time
			}
		}
		newSampleIndex := -1
		for i, es := range pc.samples {
			if newSampleIndex == -1 && s.GoroutineCount <= es.GoroutineCount {
				append(-1, s)
				newSampleIndex = j - 1
			}
			if s.GoroutineCount != es.GoroutineCount &&
				!es.Time.Before(oldestValidTime) {
				append(i, es)
			}
		}
		if newSampleIndex == -1 {
			append(-1, s)
			newSampleIndex = j - 1
		}
		pc.samples = pc.samples[:j]

		// Refresh timestamps near the new sample if we can infer that they are
		// still valid. This helps improve stability by retaining upper and
		// lower bounds around a good target.
		for i := newSampleIndex - 1; i >= 0; i-- {
			if pc.samples[i].SecondaryUtil < pc.highUtilThreshold ||
				!pc.rangeExhibitsAcceptableReturn(i, i+1) {
				break
			}
			//fmt.Printf("refreshing time for lower sample %d\n", pc.samples[i].GoroutineCount)
			pc.samples[i].Time = s.Time
		}
		for i := newSampleIndex + 1; i < len(pc.samples); i++ {
			if pc.rangeExhibitsAcceptableReturn(i-1, i) {
				break
			}
			//fmt.Printf("refreshing time for higher sample %d\n", pc.samples[i].GoroutineCount)
			pc.samples[i].Time = s.Time
		}
	} else {
		// Existing samples still valid, just need to insert or update
		i := pc.findByGoroutineCount(s.GoroutineCount)
		if i < len(pc.samples) && pc.samples[i].GoroutineCount == s.GoroutineCount {
			pc.samples[i] = s
		} else {
			pc.samples = slices.Insert(pc.samples, i, s)
		}
		if s.Time.Before(pc.oldestSampleTime) {
			pc.oldestSampleTime = s.Time
		}
	}
}

func (pc *perfCurves) findByGoroutineCount(goroutineCount int) int {
	i, _ := slices.BinarySearchFunc(
		pc.samples,
		goroutineCount,
		func(s perfSample, goroutineCount int) int {
			return s.GoroutineCount - goroutineCount
		},
	)
	return i
}

func (pc *perfCurves) RecommendTarget() int {
	target := pc.calculateBestTarget()
	switch {
	case target < pc.minConcurrency:
		return pc.minConcurrency
	case pc.maxConcurrency >= 0 && target > pc.maxConcurrency:
		return pc.maxConcurrency
	default:
		return target
	}
}

func (pc *perfCurves) calculateBestTarget() int {

	valleyIndex := pc.findUtilizationValley()
	kneeIndex := pc.findThroughputKnee()
	switch {

	case valleyIndex < len(pc.samples):
		// Non-high utilization valley found: fine-tune from best known point
		baseIndex := min(valleyIndex, kneeIndex)
		if baseIndex < len(pc.samples)-1 {
			return pc.scaleUpWithinGapAt(baseIndex)
		} else {
			return pc.scaleUpConservatively()
		}

	case kneeIndex == len(pc.samples):
		if len(pc.samples) < 2 {
			// Can't determine throughput growth with less than two samples:
			// explore a bit higher
			return pc.scaleUpConservatively()
		} else {
			// All samples are high utilization and exhibit good throughput
			// growth: explore much higher
			return pc.scaleUpAggressively()
		}

	case kneeIndex == 0: // above already ensures that len(pc.samples) > 0
		// All samples are high utilization but show at best subpar throughput
		// growth: explore lower
		return pc.scaleDown()

	default: // above already ensures that 0 < kneeIndex < len(pc.samples)
		// Throughput knee found but all samples have high utilization:
		// fine-tune upward from the knee
		return pc.scaleUpWithinGapAt(kneeIndex)
	}
}

// Finds the index of the first non-high-utilization sample or len(pc.samples)
// if all samples are high
func (pc *perfCurves) findUtilizationValley() int {
	for i, s := range pc.samples {
		if s.SecondaryUtil < pc.highUtilThreshold {
			return i
		}
	}
	return len(pc.samples)
}

// scaleUpAggressively returns a goroutine count significantly higher relative
// to the goroutine count of the highest-goroutine (last) existing sample, or 1
// if there are no existing samples.
func (pc *perfCurves) scaleUpAggressively() int {
	if len(pc.samples) == 0 {
		// No samples available, start with 1 goroutine
		return 1
	}
	return pc.scaleUpByFactor(len(pc.samples)-1, pc.aggressiveGrowthFactor)
}

// scaleUpConservatively returns a goroutine count moderately higher relative to
// the goroutine count of the sample at the given base index, but only if the
// throughput of the sample is greater than that of the preceding sample. In the
// latter case, it returns the goroutine count of original sample.
func (pc *perfCurves) scaleUpConservatively() int {
	baseIndex := len(pc.samples) - 1
	if baseIndex > 0 && !pc.throughputStillImprovingAt(baseIndex) {
		return pc.samples[baseIndex].GoroutineCount // Stay put
	}
	return pc.scaleUpByFactor(baseIndex, pc.conservativeGrowthFactor)
}

func (pc *perfCurves) scaleUpByFactor(baseIndex int, factor float64) int {
	baseGC := 0
	if baseIndex >= 0 {
		baseGC = pc.samples[baseIndex].GoroutineCount
	}
	return max(baseGC+1, int(math.Round(float64(baseGC)*factor)))
}

// scaleUpWithinGapAt returns a goroutine count between that of the sample at
// the given base index and that of the following sample, but only if the
// throughput of the first sample is greater than that of the preceding one.
// Returns the goroutine count at the base index in the latter case or if there
// is no gap between it and that of the following sample.
func (pc *perfCurves) scaleUpWithinGapAt(baseIndex int) int {
	baseGC := pc.samples[baseIndex].GoroutineCount
	if !pc.throughputStillImprovingAt(baseIndex) {
		return baseGC
	}
	gapSize := pc.samples[baseIndex+1].GoroutineCount - baseGC
	if gapSize == 1 {
		return baseGC
	}
	return baseGC + int(math.Round(float64(gapSize)*0.5))
}

func (pc *perfCurves) throughputStillImprovingAt(baseIndex int) bool {
	return baseIndex > 0 && pc.samples[baseIndex-1].Throughput < pc.samples[baseIndex].Throughput
}

// scaleDown returns a lower goroutine count roughly half that of the
// lowest-goroutine (first) existing sample, if possible. It will never return a
// goroutine count less than 1.
func (pc *perfCurves) scaleDown() int {
	baseGC := pc.samples[0].GoroutineCount
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
func (pc *perfCurves) findLinearEnd() int {
	// Must ensure that at least two samples are not skipped, for the same
	// reason as above.
	maxSkips := min(pc.maxLinearitySkips, len(pc.samples)-2)

	var sumSlopes float64
	var count int
	minSlope := math.Inf(1)
	maxSlope := math.Inf(-1)
	skipsUsed := 0
	linearEnd := 0

	for i, sample := range pc.samples {
		slope := sample.Throughput / float64(sample.GoroutineCount)

		// Test adding this slope
		testSum := sumSlopes + slope
		testCount := count + 1
		testMin := math.Min(minSlope, slope)
		testMax := math.Max(maxSlope, slope)

		if testCount >= 1 {
			meanSlope := testSum / float64(testCount)
			normalizedRange := (testMax - testMin) / meanSlope

			if normalizedRange > pc.linearityTolerance {
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

func (pc *perfCurves) findThroughputKnee() int {
	for i := 0; i < len(pc.samples)-1; i++ {
		if !pc.rangeExhibitsAcceptableReturn(i, i+1) {
			return i
		}
	}
	return len(pc.samples)
}

func (pc *perfCurves) rangeExhibitsAcceptableReturn(lowerIndex, higherIndex int) bool {
	lower := pc.samples[lowerIndex]
	higher := pc.samples[higherIndex]
	slope := (higher.Throughput - lower.Throughput) / float64(higher.GoroutineCount-lower.GoroutineCount)
	returnRate := slope / lower.Throughput
	return returnRate >= pc.minimumReturn
}
