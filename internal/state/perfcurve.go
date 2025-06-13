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
	Latency        time.Duration
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
		_, _ = fmt.Fprintf(fs, "%s%d%s: %.2f@%.0f%%(%v)", sep, s.GoroutineCount, label, s.Throughput*float64(time.Second), s.SecondaryUtil*100, s.Latency)
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
func (pc *perfCurves) RetentionPeriod() time.Duration {
	return pc.retentionPeriod
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
	//fmt.Printf("adding sample %d:%.0f to %v\n", s.GoroutineCount, s.Throughput*float64(time.Second), pc)
	//defer fmt.Printf("added sample %d:%.0f to %v\n", s.GoroutineCount, s.Throughput*float64(time.Second), pc)

	// Find where this sample should be inserted/updated in the sorted slice
	newSampleIndex := pc.findByGoroutineCount(s.GoroutineCount)
	if newSampleIndex < len(pc.samples) && s.GoroutineCount == pc.samples[newSampleIndex].GoroutineCount {
		pc.samples[newSampleIndex] = s
	}

	valleyIndex := pc.findUtilizationValley()
	kneeIndex := pc.findThroughputKnee()
	if valleyIndex == len(pc.samples) && kneeIndex == len(pc.samples) {
		for i := range pc.samples {
			pc.samples[i].Time = s.Time
		}
	} else {

		if valleyIndex < len(pc.samples) {
			pc.samples[valleyIndex].Time = s.Time
		}
		if valleyIndex+1 < len(pc.samples) {
			pc.samples[valleyIndex+1].Time = s.Time
		}
		if valleyIndex+2 < len(pc.samples) {
			pc.samples[valleyIndex+2].Time = s.Time
		}
		if valleyIndex-1 >= 0 {
			pc.samples[valleyIndex-1].Time = s.Time
		}
		if valleyIndex-2 >= 0 {
			pc.samples[valleyIndex-2].Time = s.Time
		}

		if kneeIndex < len(pc.samples) {
			pc.samples[kneeIndex].Time = s.Time
		}
		if kneeIndex+1 < len(pc.samples) {
			pc.samples[kneeIndex+1].Time = s.Time
		}
		if kneeIndex+2 < len(pc.samples) {
			pc.samples[kneeIndex+2].Time = s.Time
		}
		if kneeIndex-1 >= 0 {
			pc.samples[kneeIndex-1].Time = s.Time
		}
		if kneeIndex-2 >= 0 {
			pc.samples[kneeIndex-2].Time = s.Time
		}
	}

	/*
		if newSampleIndex < len(pc.samples) && s.GoroutineCount == pc.samples[newSampleIndex].GoroutineCount {
			pc.samples[newSampleIndex] = s
		}
		if len(pc.samples) > 2 {
			pc.samples[0].Time = s.Time
			pc.samples[1].Time = s.Time
			for i := 2; i < len(pc.samples); i++ {
				if pc.rangeExhibitsAcceptableReturn(&pc.samples[i-1], &pc.samples[i]) {
					//pc.samples[0].Time = s.Time
					pc.samples[i].Time = s.Time
				}
			}
		}
	*/

	/*
		// Proactively refresh timestamps of nearby samples to prevent premature expiration
		// and maintain performance curve stability bounds around the new sample
		if newSampleIndex > 0 {
			referenceSample := &s
			// Refresh timestamps for samples with lower goroutine counts
			// Walk backward from insertion point, refreshing timestamps of samples
			// that still appear to have acceptable performance characteristics
			for i := newSampleIndex - 1; i >= 0; i-- {
				//fmt.Printf("refreshing time for lower sample %d\n", pc.samples[i].GoroutineCount)
				if pc.samples[i].SecondaryUtil > pc.highUtilThreshold &&
					!pc.rangeExhibitsAcceptableReturn(&pc.samples[i], referenceSample) {
					break
				}
				pc.samples[i].Time = s.Time
				referenceSample = &pc.samples[i]
			}
		}

		// Handle the sample at the insertion point and refresh forward samples
		if newSampleIndex < len(pc.samples) {
			referenceSample := &s
			i := newSampleIndex
			if pc.samples[newSampleIndex].GoroutineCount == s.GoroutineCount {
				// Start forward refresh from the next sample (the one after our update)
				i++
			}
			// Refresh timestamps for samples with higher goroutine counts
			// Continue until we find a sample that shows acceptable return on investment
			for ; i < len(pc.samples); i++ {
				//fmt.Printf("refreshing time for higher sample %d\n", pc.samples[i].GoroutineCount)
				if !pc.rangeExhibitsAcceptableReturn(referenceSample, &pc.samples[i]) {
					break
				}
				pc.samples[i].Time = s.Time
				referenceSample = &pc.samples[i]
			}
		}
	*/

	// Use the new sample's timestamp for consistent time reference throughout method
	oldestValidTime := s.Time.Add(-pc.retentionPeriod)
	if pc.oldestSampleTime.IsZero() || pc.oldestSampleTime.Before(oldestValidTime) {
		//fmt.Printf("expiring samples (before): %v\n", pc)
		//defer fmt.Printf("expiring samples (after): %v\n", pc)
		// Scan to expire old samples while also inserting new sample
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
		for i, es := range pc.samples {
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
		if newSampleIndex == len(pc.samples) {
			append(-1, s)
		}
		pc.samples = pc.samples[:j]

	} else if newSampleIndex == len(pc.samples) || s.GoroutineCount != pc.samples[newSampleIndex].GoroutineCount {
		// Existing samples still valid, just need to insert/append
		pc.samples = slices.Insert(pc.samples, newSampleIndex, s)
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
	return baseGC + min(1, int(math.Round(float64(gapSize)*0.5)))
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
		if !pc.rangeExhibitsAcceptableReturn(&pc.samples[i], &pc.samples[i+1]) {
			return i
		}
	}
	return len(pc.samples)
}

func (pc *perfCurves) rangeExhibitsAcceptableReturn(lower, higher *perfSample) bool {
	slope := (higher.Throughput - lower.Throughput) / float64(higher.GoroutineCount-lower.GoroutineCount)
	returnRate := slope / lower.Throughput
	return returnRate >= pc.minimumReturn
}
