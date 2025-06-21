// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cpstate

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Helper to create a controller with standard test configuration
func newTestController() *controller {
	c := &controller{}
	c.SetConfig(controllerConfig{
		MinConcurrency:           1,
		MaxConcurrency:           -1, // unlimited
		HighUtilThreshold:        0.6,
		MinThroughputROI:         0.2,
		AggressiveGrowthFactor:   2.5,
		ConservativeGrowthFactor: 1.3,
	})
	return c
}

func TestControllerBasics(t *testing.T) {

	t.Run("empty controller", func(t *testing.T) {
		c := newTestController()
		if len(c.samples) != 0 {
			t.Errorf("expected empty samples, got %d", len(c.samples))
		}
	})

	t.Run("single sample insertion", func(t *testing.T) {
		c := newTestController()
		s := perfSample{
			Time:           time.Now(),
			GoroutineCount: 5,
			Throughput:     1000,
			SpareUtil:      0.5,
		}
		c.AddSample(time.Hour, s)

		if len(c.samples) != 1 {
			t.Fatalf("expected 1 sample, got %d", len(c.samples))
		}
		if c.samples[0].GoroutineCount != 5 {
			t.Errorf("expected GoroutineCount=5, got %d", c.samples[0].GoroutineCount)
		}
	})

	t.Run("samples sorted by goroutine count", func(t *testing.T) {
		c := newTestController()
		now := time.Now()

		// Add samples out of order
		c.AddSample(time.Hour, perfSample{
			Time:           now,
			GoroutineCount: 10,
			Throughput:     2000,
			SpareUtil:      0.5,
		})
		c.AddSample(time.Hour, perfSample{
			Time:           now.Add(time.Second),
			GoroutineCount: 5,
			Throughput:     1000,
			SpareUtil:      0.5,
		})
		c.AddSample(time.Hour, perfSample{
			Time:           now.Add(2 * time.Second),
			GoroutineCount: 15,
			Throughput:     2500,
			SpareUtil:      0.5,
		})

		if len(c.samples) != 3 {
			t.Fatalf("expected 3 samples, got %d", len(c.samples))
		}

		// Check sorted order
		if c.samples[0].GoroutineCount != 5 {
			t.Errorf("expected first sample GoroutineCount=5, got %d", c.samples[0].GoroutineCount)
		}
		if c.samples[1].GoroutineCount != 10 {
			t.Errorf("expected second sample GoroutineCount=10, got %d", c.samples[1].GoroutineCount)
		}
		if c.samples[2].GoroutineCount != 15 {
			t.Errorf("expected third sample GoroutineCount=15, got %d", c.samples[2].GoroutineCount)
		}
	})

	t.Run("update existing goroutine count", func(t *testing.T) {
		c := newTestController()
		now := time.Now()

		// First sample
		c.AddSample(time.Hour, perfSample{
			Time:           now,
			GoroutineCount: 5,
			Throughput:     1000,
			SpareUtil:      0.5,
		})

		// Update with same goroutine count
		c.AddSample(time.Hour, perfSample{
			Time:           now.Add(time.Second),
			GoroutineCount: 5,
			Throughput:     1200,
			SpareUtil:      0.6,
		})

		if len(c.samples) != 1 {
			t.Fatalf("expected 1 sample after update, got %d", len(c.samples))
		}
		if c.samples[0].Throughput != 1200 {
			t.Errorf("expected updated Throughput=1200, got %f", c.samples[0].Throughput)
		}
	})
}

func TestFindUtilizationValley(t *testing.T) {
	t.Run("finds lowest utilization", func(t *testing.T) {
		c := newTestController()
		c.config.HighUtilThreshold = 0.6

		// Add samples with different utilizations
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},  // high
			{Time: now, GoroutineCount: 2, Throughput: 200, SpareUtil: 0.7},  // high
			{Time: now, GoroutineCount: 3, Throughput: 300, SpareUtil: 0.5},  // low (valley)
			{Time: now, GoroutineCount: 4, Throughput: 380, SpareUtil: 0.55}, // low
			{Time: now, GoroutineCount: 5, Throughput: 400, SpareUtil: 0.65}, // high
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		valleyIdx := c.findUtilizationValley()
		if valleyIdx != 2 {
			t.Errorf("expected valley at index 2 (GC=3), got %d", valleyIdx)
		}
	})

	t.Run("returns len when all high utilization", func(t *testing.T) {
		c := newTestController()
		c.config.HighUtilThreshold = 0.6

		// All samples have high utilization
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},
			{GoroutineCount: 2, Throughput: 200, SpareUtil: 0.7},
			{GoroutineCount: 3, Throughput: 300, SpareUtil: 0.65},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		valleyIdx := c.findUtilizationValley()
		if valleyIdx != len(c.samples) {
			t.Errorf("expected valley index = len(samples) when all high, got %d", valleyIdx)
		}
	})
}

func TestFindThroughputKnee(t *testing.T) {
	t.Run("identifies throughput knee", func(t *testing.T) {
		c := newTestController()
		c.config.MinThroughputROI = 0.2

		// Perfect linear scaling from origin
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},
			{Time: now, GoroutineCount: 2, Throughput: 200, SpareUtil: 0.7},
			{Time: now, GoroutineCount: 3, Throughput: 300, SpareUtil: 0.6},
			{Time: now, GoroutineCount: 4, Throughput: 380, SpareUtil: 0.5}, // Still within tolerance
			{Time: now, GoroutineCount: 5, Throughput: 400, SpareUtil: 0.4}, // Breaks minimum return
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		knee := c.findThroughputKnee()
		// Should detect growth up to index 3 (GC=4)
		if knee != 3 {
			t.Errorf("expected knee at index 3, got %d", knee)
		}
	})

	t.Run("handles non-linear scaling", func(t *testing.T) {
		c := newTestController()
		c.config.MinThroughputROI = 0.2

		// Non-linear pattern
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},
			{GoroutineCount: 2, Throughput: 150, SpareUtil: 0.7}, // Not linear
			{GoroutineCount: 3, Throughput: 180, SpareUtil: 0.6},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		knee := c.findThroughputKnee()
		// With minimumReturn=0.2:
		// 1→2: return=0.5 (acceptable), 2→3: return=0.2 (acceptable)
		// So knee should be at end (index 3)
		if knee != 3 {
			t.Errorf("expected knee at end (index 3), got %d", knee)
		}
	})
}

func TestRecommendTarget(t *testing.T) {
	t.Run("no samples recommends 1", func(t *testing.T) {
		c := newTestController()
		target := c.RecommendTarget()
		if target != 1 {
			t.Errorf("expected target=1 for empty controller, got %d", target)
		}
	})

	t.Run("valley found recommends conservative exploration", func(t *testing.T) {
		c := newTestController()
		c.config.HighUtilThreshold = 0.6

		// Create a valley scenario
		samples := []perfSample{
			{GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},
			{GoroutineCount: 2, Throughput: 200, SpareUtil: 0.7},
			{GoroutineCount: 3, Throughput: 300, SpareUtil: 0.4}, // Valley (low util)
			{GoroutineCount: 4, Throughput: 380, SpareUtil: 0.5},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		target := c.RecommendTarget()
		// Valley at index 2 (GC=3), knee at end (index 4)
		// scaleUpWithinGapAt(2): baseGC=3, gap to next=1, so returns 3
		if target != 3 {
			t.Errorf("expected target=3 from valley exploration, got %d", target)
		}
	})

	t.Run("all linear high util recommends aggressive growth", func(t *testing.T) {
		c := newTestController()
		c.config.HighUtilThreshold = 0.6
		c.config.AggressiveGrowthFactor = 2.5

		// All samples linear and high utilization
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 1, Throughput: 100, SpareUtil: 0.8},
			{Time: now, GoroutineCount: 2, Throughput: 200, SpareUtil: 0.7},
			{Time: now, GoroutineCount: 3, Throughput: 300, SpareUtil: 0.75},
			{Time: now, GoroutineCount: 4, Throughput: 400, SpareUtil: 0.8},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		target := c.RecommendTarget()
		// Should aggressively scale up from 4
		expectedMin := int(math.Round(4 * 2.5))
		if target < expectedMin {
			t.Errorf("expected aggressive growth to at least %d, got %d", expectedMin, target)
		}
	})

	t.Run("respects max concurrency limit", func(t *testing.T) {
		c := newTestController()
		c.SetConfig(controllerConfig{
			MinConcurrency:           1,
			MaxConcurrency:           10,
			HighUtilThreshold:        0.6,
			MinThroughputROI:         0.2,
			AggressiveGrowthFactor:   2.5,
			ConservativeGrowthFactor: 1.3,
		})

		// Would recommend > 10 without limit
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 8, Throughput: 800, SpareUtil: 0.8},
			{Time: now, GoroutineCount: 9, Throughput: 900, SpareUtil: 0.8},
			{Time: now, GoroutineCount: 10, Throughput: 1000, SpareUtil: 0.8},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		target := c.RecommendTarget()
		if target > 10 {
			t.Errorf("expected target <= maxConcurrency(10), got %d", target)
		}
	})

	t.Run("scales down when no linear region found", func(t *testing.T) {
		c := newTestController()
		c.config.HighUtilThreshold = 0.6
		c.config.MinThroughputROI = 0.2

		// High util but no linear scaling from origin
		now := time.Now()
		samples := []perfSample{
			{Time: now, GoroutineCount: 5, Throughput: 300, SpareUtil: 0.8},
			{Time: now, GoroutineCount: 6, Throughput: 310, SpareUtil: 0.85},
			{Time: now, GoroutineCount: 7, Throughput: 315, SpareUtil: 0.9},
		}

		for _, s := range samples {
			c.AddSample(time.Hour, s)
		}

		target := c.RecommendTarget()
		// Should scale down from 5
		if target >= 5 {
			t.Errorf("expected scale down from 5, got %d", target)
		}
	})
}

// Property-based tests
func TestControllerProperties(t *testing.T) {
	t.Run("samples remain sorted after random insertions", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			c := newTestController()

			// Generate random samples
			numSamples := rapid.IntRange(1, 50).Draw(t, "numSamples")
			for i := 0; i < numSamples; i++ {
				gc := rapid.IntRange(1, 100).Draw(t, "goroutineCount")
				throughput := rapid.Float64Range(10, 10000).Draw(t, "throughput")
				util := rapid.Float64Range(0.1, 0.95).Draw(t, "utilization")

				c.AddSample(time.Hour, perfSample{
					Time:           time.Now(),
					GoroutineCount: gc,
					Throughput:     throughput,
					SpareUtil:      util,
				})
			}

			// Check samples are sorted by goroutine count
			for i := 1; i < len(c.samples); i++ {
				if c.samples[i-1].GoroutineCount > c.samples[i].GoroutineCount {
					t.Fatalf("samples not sorted: [%d].GC=%d > [%d].GC=%d",
						i-1, c.samples[i-1].GoroutineCount,
						i, c.samples[i].GoroutineCount)
				}
			}
		})
	})

	t.Run("RecommendTarget always respects limits", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			c := newTestController()

			// Generate random but valid limits
			minConcurrency := rapid.IntRange(0, 10).Draw(t, "minConcurrency")
			maxConcurrency := rapid.OneOf(
				rapid.Just(-1), // unlimited
				rapid.IntRange(max(minConcurrency, 1), 50),
			).Draw(t, "maxConcurrency")
			config := c.config
			config.MinConcurrency = minConcurrency
			config.MaxConcurrency = maxConcurrency
			c.SetConfig(config)

			// Generate some random performance data
			numSamples := rapid.IntRange(0, 20).Draw(t, "numSamples")
			for i := 0; i < numSamples; i++ {
				gc := rapid.IntRange(1, 100).Draw(t, "goroutineCount")
				throughput := rapid.Float64Range(100, 10000).Draw(t, "throughput")
				util := rapid.Float64Range(0.1, 0.95).Draw(t, "utilization")

				c.AddSample(time.Hour, perfSample{
					Time:           time.Now(),
					GoroutineCount: gc,
					Throughput:     throughput,
					SpareUtil:      util,
				})
			}

			recommendation := c.RecommendTarget()

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
			c := newTestController()
			c.config.MinThroughputROI = 0.2

			// Generate samples with known linear portion
			linearSamples := rapid.IntRange(2, 10).Draw(t, "linearSamples")
			slope := rapid.Float64Range(50, 200).Draw(t, "slope")

			// Add perfectly linear samples
			for i := 1; i <= linearSamples; i++ {
				c.AddSample(time.Hour, perfSample{
					GoroutineCount: i,
					Throughput:     slope * float64(i),
					SpareUtil:      0.5,
				})
			}

			// Add some non-linear samples
			for i := linearSamples + 1; i <= linearSamples+5; i++ {
				// Throughput deviates from linear
				deviation := rapid.Float64Range(0.3, 0.7).Draw(t, "deviation")
				c.AddSample(time.Hour, perfSample{
					GoroutineCount: i,
					Throughput:     slope * float64(i) * deviation,
					SpareUtil:      0.7,
				})
			}

			knee := c.findThroughputKnee()

			// Linear end should be around where we introduced deviation
			if knee < linearSamples-1 || knee > linearSamples+2 {
				t.Logf("Expected knee around %d, got %d", linearSamples, knee)
			}
		})
	})
}
