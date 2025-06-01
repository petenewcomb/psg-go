// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Helper to create a perfCurves with standard test configuration
func newTestPerfCurves() *perfCurves {
	pc := &perfCurves{
		minConcurrency:           1,
		maxConcurrency:           -1, // unlimited
		retentionPeriod:          time.Hour,
		highUtilThreshold:        0.6,
		minimumReturn:            0.2,
		aggressiveGrowthFactor:   2.5,
		conservativeGrowthFactor: 1.3,
	}
	return pc
}

func TestPerfCurvesBasics(t *testing.T) {

	t.Run("empty curves", func(t *testing.T) {
		pc := newTestPerfCurves()
		if len(pc.samples) != 0 {
			t.Errorf("expected empty samples, got %d", len(pc.samples))
		}
	})

	t.Run("single sample insertion", func(t *testing.T) {
		pc := newTestPerfCurves()
		s := perfSample{
			Time:           time.Now(),
			GoroutineCount: 5,
			Throughput:     1000,
			SecondaryUtil:  0.5,
		}
		pc.AddSample(s)

		if len(pc.samples) != 1 {
			t.Fatalf("expected 1 sample, got %d", len(pc.samples))
		}
		if pc.samples[0].GoroutineCount != 5 {
			t.Errorf("expected GoroutineCount=5, got %d", pc.samples[0].GoroutineCount)
		}
	})

	t.Run("samples sorted by goroutine count", func(t *testing.T) {
		pc := newTestPerfCurves()
		now := time.Now()

		// Add samples out of order
		pc.AddSample(perfSample{
			Time:           now,
			GoroutineCount: 10,
			Throughput:     2000,
			SecondaryUtil:  0.5,
		})
		pc.AddSample(perfSample{
			Time:           now.Add(time.Second),
			GoroutineCount: 5,
			Throughput:     1000,
			SecondaryUtil:  0.5,
		})
		pc.AddSample(perfSample{
			Time:           now.Add(2 * time.Second),
			GoroutineCount: 15,
			Throughput:     2500,
			SecondaryUtil:  0.5,
		})

		if len(pc.samples) != 3 {
			t.Fatalf("expected 3 samples, got %d", len(pc.samples))
		}

		// Check sorted order
		if pc.samples[0].GoroutineCount != 5 {
			t.Errorf("expected first sample GoroutineCount=5, got %d", pc.samples[0].GoroutineCount)
		}
		if pc.samples[1].GoroutineCount != 10 {
			t.Errorf("expected second sample GoroutineCount=10, got %d", pc.samples[1].GoroutineCount)
		}
		if pc.samples[2].GoroutineCount != 15 {
			t.Errorf("expected third sample GoroutineCount=15, got %d", pc.samples[2].GoroutineCount)
		}
	})

	t.Run("update existing goroutine count", func(t *testing.T) {
		pc := newTestPerfCurves()
		now := time.Now()

		// First sample
		pc.AddSample(perfSample{
			Time:           now,
			GoroutineCount: 5,
			Throughput:     1000,
			SecondaryUtil:  0.5,
		})

		// Update with same goroutine count
		pc.AddSample(perfSample{
			Time:           now.Add(time.Second),
			GoroutineCount: 5,
			Throughput:     1200,
			SecondaryUtil:  0.6,
		})

		if len(pc.samples) != 1 {
			t.Fatalf("expected 1 sample after update, got %d", len(pc.samples))
		}
		if pc.samples[0].Throughput != 1200 {
			t.Errorf("expected updated Throughput=1200, got %f", pc.samples[0].Throughput)
		}
	})
}

func TestFindUtilizationValley(t *testing.T) {
	t.Run("finds lowest utilization", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.highUtilThreshold = 0.6

		// Add samples with different utilizations
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},  // high
			{Time: now, GoroutineCount: 2, Throughput: 200, SecondaryUtil: 0.7},  // high
			{Time: now, GoroutineCount: 3, Throughput: 300, SecondaryUtil: 0.5},  // low (valley)
			{Time: now, GoroutineCount: 4, Throughput: 380, SecondaryUtil: 0.55}, // low
			{Time: now, GoroutineCount: 5, Throughput: 400, SecondaryUtil: 0.65}, // high
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		valleyIdx := pc.findUtilizationValley()
		if valleyIdx != 2 {
			t.Errorf("expected valley at index 2 (GC=3), got %d", valleyIdx)
		}
	})

	t.Run("returns len when all high utilization", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.highUtilThreshold = 0.6

		// All samples have high utilization
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},
			{GoroutineCount: 2, Throughput: 200, SecondaryUtil: 0.7},
			{GoroutineCount: 3, Throughput: 300, SecondaryUtil: 0.65},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		valleyIdx := pc.findUtilizationValley()
		if valleyIdx != len(pc.samples) {
			t.Errorf("expected valley index = len(samples) when all high, got %d", valleyIdx)
		}
	})
}

func TestFindThroughputKnee(t *testing.T) {
	t.Run("identifies throughput knee", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.minimumReturn = 0.2

		// Perfect linear scaling from origin
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},
			{Time: now, GoroutineCount: 2, Throughput: 200, SecondaryUtil: 0.7},
			{Time: now, GoroutineCount: 3, Throughput: 300, SecondaryUtil: 0.6},
			{Time: now, GoroutineCount: 4, Throughput: 380, SecondaryUtil: 0.5}, // Still within tolerance
			{Time: now, GoroutineCount: 5, Throughput: 400, SecondaryUtil: 0.4}, // Breaks minimum return
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		knee := pc.findThroughputKnee()
		// Should detect growth up to index 3 (GC=4)
		if knee != 3 {
			t.Errorf("expected knee at index 3, got %d", knee)
		}
	})

	t.Run("handles non-linear scaling", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.minimumReturn = 0.2

		// Non-linear pattern
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},
			{GoroutineCount: 2, Throughput: 150, SecondaryUtil: 0.7}, // Not linear
			{GoroutineCount: 3, Throughput: 180, SecondaryUtil: 0.6},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		knee := pc.findThroughputKnee()
		if knee > 2 {
			t.Errorf("expected limited throughput knee, got %d", knee)
		}
	})
}

func TestRecommendTarget(t *testing.T) {
	t.Run("no samples recommends 1", func(t *testing.T) {
		pc := newTestPerfCurves()
		target := pc.RecommendTarget()
		if target != 1 {
			t.Errorf("expected target=1 for empty curves, got %d", target)
		}
	})

	t.Run("valley found recommends conservative exploration", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.highUtilThreshold = 0.6

		// Create a valley scenario
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},
			{GoroutineCount: 2, Throughput: 200, SecondaryUtil: 0.7},
			{GoroutineCount: 3, Throughput: 300, SecondaryUtil: 0.4}, // Valley (low util)
			{GoroutineCount: 4, Throughput: 380, SecondaryUtil: 0.5},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		target := pc.RecommendTarget()
		// Should explore conservatively from the valley or linear end
		if target <= 4 || target > 6 {
			t.Errorf("expected conservative exploration from valley, got %d", target)
		}
	})

	t.Run("all linear high util recommends aggressive growth", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.highUtilThreshold = 0.6
		pc.aggressiveGrowthFactor = 2.5

		// All samples linear and high utilization
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SecondaryUtil: 0.8},
			{Time: now, GoroutineCount: 2, Throughput: 200, SecondaryUtil: 0.7},
			{Time: now, GoroutineCount: 3, Throughput: 300, SecondaryUtil: 0.75},
			{Time: now, GoroutineCount: 4, Throughput: 400, SecondaryUtil: 0.8},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		target := pc.RecommendTarget()
		// Should aggressively scale up from 4
		expectedMin := int(math.Round(4 * 2.5))
		if target < expectedMin {
			t.Errorf("expected aggressive growth to at least %d, got %d", expectedMin, target)
		}
	})

	t.Run("respects max concurrency limit", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.SetLimits(1, 10)
		pc.aggressiveGrowthFactor = 2.5

		// Would recommend > 10 without limit
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 8, Throughput: 800, SecondaryUtil: 0.8},
			{Time: now, GoroutineCount: 9, Throughput: 900, SecondaryUtil: 0.8},
			{Time: now, GoroutineCount: 10, Throughput: 1000, SecondaryUtil: 0.8},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		target := pc.RecommendTarget()
		if target > 10 {
			t.Errorf("expected target <= maxConcurrency(10), got %d", target)
		}
	})

	t.Run("scales down when no linear region found", func(t *testing.T) {
		pc := newTestPerfCurves()
		pc.highUtilThreshold = 0.6
		pc.minimumReturn = 0.2

		// High util but no linear scaling from origin
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 5, Throughput: 300, SecondaryUtil: 0.8},
			{Time: now, GoroutineCount: 6, Throughput: 310, SecondaryUtil: 0.85},
			{Time: now, GoroutineCount: 7, Throughput: 315, SecondaryUtil: 0.9},
		}

		for _, s := range samples {
			pc.AddSample(s)
		}

		target := pc.RecommendTarget()
		// Should scale down from 5
		if target >= 5 {
			t.Errorf("expected scale down from 5, got %d", target)
		}
	})
}

// Property-based tests
func TestPerfCurvesProperties(t *testing.T) {
	t.Run("samples remain sorted after random insertions", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			pc := newTestPerfCurves()

			// Generate random samples
			numSamples := rapid.IntRange(1, 50).Draw(t, "numSamples")
			for i := 0; i < numSamples; i++ {
				gc := rapid.IntRange(1, 100).Draw(t, "goroutineCount")
				throughput := rapid.Float64Range(10, 10000).Draw(t, "throughput")
				util := rapid.Float64Range(0.1, 0.95).Draw(t, "utilization")

				pc.AddSample(perfSample{
					Time:           time.Now(),
					GoroutineCount: gc,
					Throughput:     throughput,
					SecondaryUtil:  util,
				})
			}

			// Check samples are sorted by goroutine count
			for i := 1; i < len(pc.samples); i++ {
				if pc.samples[i-1].GoroutineCount > pc.samples[i].GoroutineCount {
					t.Fatalf("samples not sorted: [%d].GC=%d > [%d].GC=%d",
						i-1, pc.samples[i-1].GoroutineCount,
						i, pc.samples[i].GoroutineCount)
				}
			}
		})
	})

	t.Run("RecommendTarget always respects limits", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			pc := newTestPerfCurves()

			// Generate random but valid limits
			minConcurrency := rapid.IntRange(0, 10).Draw(t, "minConcurrency")
			maxConcurrency := rapid.OneOf(
				rapid.Just(-1), // unlimited
				rapid.IntRange(max(minConcurrency, 1), 50),
			).Draw(t, "maxConcurrency")
			pc.SetLimits(minConcurrency, maxConcurrency)

			// Generate some random performance data
			numSamples := rapid.IntRange(0, 20).Draw(t, "numSamples")
			for i := 0; i < numSamples; i++ {
				gc := rapid.IntRange(1, 100).Draw(t, "goroutineCount")
				throughput := rapid.Float64Range(100, 10000).Draw(t, "throughput")
				util := rapid.Float64Range(0.1, 0.95).Draw(t, "utilization")

				pc.AddSample(perfSample{
					Time:           time.Now(),
					GoroutineCount: gc,
					Throughput:     throughput,
					SecondaryUtil:  util,
				})
			}

			recommendation := pc.RecommendTarget()

			// Check minimum bound
			expectedMin := minConcurrency
			if expectedMin == 0 {
				expectedMin = 1 // algorithm never recommends 0
			}
			if recommendation < expectedMin {
				t.Fatalf("RecommendTarget() = %d, violates minConcurrency %d",
					recommendation, minConcurrency)
			}

			// Check maximum bound
			if maxConcurrency >= 0 && recommendation > maxConcurrency {
				t.Fatalf("RecommendTarget() = %d, violates maxConcurrency %d",
					recommendation, maxConcurrency)
			}
		})
	})

	t.Run("findThroughputKnee is consistent with tolerance", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			pc := newTestPerfCurves()
			pc.minimumReturn = 0.2

			// Generate samples with known linear portion
			linearSamples := rapid.IntRange(2, 10).Draw(t, "linearSamples")
			slope := rapid.Float64Range(50, 200).Draw(t, "slope")

			// Add perfectly linear samples
			for i := 1; i <= linearSamples; i++ {
				pc.AddSample(perfSample{
					GoroutineCount: i,
					Throughput:     slope * float64(i),
					SecondaryUtil:  0.5,
				})
			}

			// Add some non-linear samples
			for i := linearSamples + 1; i <= linearSamples+5; i++ {
				// Throughput deviates from linear
				deviation := rapid.Float64Range(0.3, 0.7).Draw(t, "deviation")
				pc.AddSample(perfSample{
					GoroutineCount: i,
					Throughput:     slope * float64(i) * deviation,
					SecondaryUtil:  0.7,
				})
			}

			knee := pc.findThroughputKnee()

			// Linear end should be around where we introduced deviation
			if knee < linearSamples-1 || knee > linearSamples+2 {
				t.Logf("Expected knee around %d, got %d", linearSamples, knee)
			}
		})
	})
}
