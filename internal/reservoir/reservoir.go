// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package reservoir

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
)

// AddFunc adds a sample to a reservoir using the reservoir sampling algorithm
// and the provided store function
func AddFunc[S ~[]E, E any](sample S, newCount int64, storeFn func(S, int)) {
	capacity := capacityOf(sample)
	if newCount < 1 {
		panic("invalid new count: must be greater than zero")
	}
	if newCount <= capacity {
		storeFn(sample, int(newCount-1))
	} else {
		j := rand.Int64N(newCount)
		if j < capacity {
			storeFn(sample, int(j))
		}
	}
}

func capacityOf[S ~[]E, E any](sample S) int64 {
	validateSample(sample)
	return int64(len(sample))
}

func validateSample[S ~[]E, E any](sample S) {
	if len(sample) == 0 {
		panic("sample slice must have greater than zero length")
	}
}

// Add adds a sample to a reservoir using the reservoir sampling algorithm
func Add[S ~[]E, E any](sample S, newCount int64, value E) {
	AddFunc(sample, newCount, func(sample S, index int) {
		sample[index] = value
	})
}

// FinalizeFunc sorts the sample using the provided comparison function
func FinalizeFunc[S ~[]E, E any](sample S, count int64, cmp func(a, b E) int) {
	slices.SortFunc(sample[:Len(sample, count)], cmp)
}

// Finalize sorts the sample.
func Finalize[S ~[]E, E cmp.Ordered](sample S, count int64) {
	slices.SortFunc(sample[:Len(sample, count)], cmp.Compare[E])
}

func Len[S ~[]E, E any](sample S, count int64) int {
	capacity := capacityOf(sample)
	validateCount(count)
	return int(min(capacity, count))
}

func validateCount(count int64) {
	if count < 0 {
		panic("invalid count: must not be negative")
	}
}

// Quantile returns the requested quantile from the sorted sample. Use Finalize
// or FinalizeFunc to sort the sample first.
func Quantile[S ~[]E, E any](sample S, count int64, q float64) int {
	n := Len(sample, count)
	validateQuantile(q)
	switch n {
	case 0:
		return -1
	case 1:
		return 0
	}

	index := int(q * float64(n-1))
	return max(0, min(index, n-1))
}

// InterpolatedQuantileFunc returns the requested quantile with linear
// interpolation between values supplied by the provided function.
func InterpolatedQuantileFunc[S ~[]E, E any](sample S, count int64, q float64, valueFn func(S, int) float64) float64 {
	n := Len(sample, count)
	validateQuantile(q)
	switch n {
	case 0:
		return math.NaN()
	case 1:
		return valueFn(sample, 0)
	}

	index := q * float64(n-1)
	lower := int(index)
	upper := lower + 1
	if upper >= n {
		return valueFn(sample, n-1)
	}

	// Linear interpolation
	weight := index - float64(lower)
	lowerVal := valueFn(sample, lower)
	upperVal := valueFn(sample, upper)
	return lowerVal*(1-weight) + upperVal*weight
}

type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// InterpolatedQuantile returns the requested quantile with linear interpolation
// for numeric samples
func InterpolatedQuantile[S ~[]E, E Numeric](sample S, count int64, q float64) float64 {
	return InterpolatedQuantileFunc(sample, count, q, func(sample S, index int) float64 {
		return float64(sample[index])
	})
}

func validateQuantile(q float64) {
	if q < 0 || q > 1 {
		panic("invalid quantile: must be in the range [0, 1]")
	}
}
