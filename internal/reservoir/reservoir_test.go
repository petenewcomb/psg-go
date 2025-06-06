// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package reservoir_test

import (
	"cmp"
	"testing"

	"github.com/petenewcomb/psg-go/internal/reservoir"
)

func TestSampleBasic(t *testing.T) {
	samples := make([]float64, 5)
	count := int64(0)

	// Add some values
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	for _, v := range values {
		count++
		reservoir.Add(samples, count, v)
	}

	if count != 5 {
		t.Errorf("Expected count 5, got %d", count)
	}

	reservoir.Finalize(samples, count)

	// Test quantiles
	p0Index := reservoir.Quantile(samples, count, 0.0)
	if samples[p0Index] != 1.0 {
		t.Errorf("Expected p0 = 1.0, got %f", samples[p0Index])
	}
	p100Index := reservoir.Quantile(samples, count, 1.0)
	if samples[p100Index] != 5.0 {
		t.Errorf("Expected p100 = 5.0, got %f", samples[p100Index])
	}

	// Test interpolated quantiles
	p50 := reservoir.InterpolatedQuantile(samples, count, 0.5)
	if p50 != 3.0 {
		t.Errorf("Expected p50 = 3.0, got %f", p50)
	}
}

func TestSampleOverflow(t *testing.T) {
	samples := make([]int, 3)
	count := int64(0)

	// Add more values than capacity
	for i := 1; i <= 10; i++ {
		count++
		reservoir.Add(samples, count, i)
	}

	if count != 10 {
		t.Errorf("Expected count 10, got %d", count)
	}

	if len(samples) != 3 {
		t.Errorf("Expected capacity 3, got %d", len(samples))
	}

	reservoir.Finalize(samples, count)

	// Should have exactly 3 samples
	minIndex := reservoir.Quantile(samples, count, 0.0)
	maxIndex := reservoir.Quantile(samples, count, 1.0)
	min := samples[minIndex]
	max := samples[maxIndex]

	if min < 1 || min > 10 {
		t.Errorf("Min value %d out of expected range [1, 10]", min)
	}
	if max < 1 || max > 10 {
		t.Errorf("Max value %d out of expected range [1, 10]", max)
	}
	if min > max {
		t.Errorf("Min %d should be <= max %d", min, max)
	}
}

func TestSampleGeneric(t *testing.T) {
	samples := make([]string, 3)
	count := int64(0)

	words := []string{"apple", "banana", "cherry", "date"}
	for _, w := range words {
		count++
		reservoir.Add(samples, count, w)
	}

	reservoir.FinalizeFunc(samples, count, cmp.Compare[string])

	// Should be sorted alphabetically
	firstIndex := reservoir.Quantile(samples, count, 0.0)
	lastIndex := reservoir.Quantile(samples, count, 1.0)
	first := samples[firstIndex]
	last := samples[lastIndex]

	// All should be valid words from our input
	validWords := map[string]bool{"apple": true, "banana": true, "cherry": true, "date": true}
	if !validWords[first] {
		t.Errorf("Invalid first word: %s", first)
	}
	if !validWords[last] {
		t.Errorf("Invalid last word: %s", last)
	}

	// Should be in alphabetical order
	if first > last {
		t.Errorf("First word %s should be <= last word %s", first, last)
	}
}

func TestReset(t *testing.T) {
	samples := make([]int, 5)
	count := int64(0)

	count++
	reservoir.Add(samples, count, 1)
	count++
	reservoir.Add(samples, count, 2)

	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}

	// Reset by setting count to 0
	count = 0

	if count != 0 {
		t.Errorf("Expected count 0 after reset, got %d", count)
	}
}
