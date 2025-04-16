// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"slices"
	"time"

	"github.com/stretchr/testify/require"
)

type Result struct {
	MaxConcurrencyByPool []int64
	OverallDuration      time.Duration
}

type ResultRange struct {
	MinMaxConcurrencyByPool []int64
	MaxMaxConcurrencyByPool []int64
	MinOverallDuration      time.Duration
	MaxOverallDuration      time.Duration
}

func (rr *ResultRange) MergeResult(t require.TestingT, r *Result) {
	chk := require.New(t)
	chk.NotNil(rr)
	chk.NotNil(r)
	if len(rr.MinMaxConcurrencyByPool) == 0 && len(rr.MaxMaxConcurrencyByPool) == 0 {
		rr.MinMaxConcurrencyByPool = slices.Clone(r.MaxConcurrencyByPool)
		rr.MaxMaxConcurrencyByPool = slices.Clone(r.MaxConcurrencyByPool)
		rr.MinOverallDuration = r.OverallDuration
		rr.MaxOverallDuration = r.OverallDuration
	} else {
		chk.Equal(len(rr.MinMaxConcurrencyByPool), len(r.MaxConcurrencyByPool))
		chk.Equal(len(rr.MaxMaxConcurrencyByPool), len(r.MaxConcurrencyByPool))
		for i := range len(r.MaxConcurrencyByPool) {
			rr.MinMaxConcurrencyByPool[i] = min(rr.MinMaxConcurrencyByPool[i], r.MaxConcurrencyByPool[i])
			rr.MaxMaxConcurrencyByPool[i] = max(rr.MaxMaxConcurrencyByPool[i], r.MaxConcurrencyByPool[i])
		}
		rr.MinOverallDuration = min(rr.MinOverallDuration, r.OverallDuration)
		rr.MaxOverallDuration = max(rr.MaxOverallDuration, r.OverallDuration)
	}
}

func (rr *ResultRange) MergeRange(t require.TestingT, r *ResultRange) {
	chk := require.New(t)
	chk.Equal(len(r.MinMaxConcurrencyByPool), len(r.MaxMaxConcurrencyByPool))
	if len(rr.MinMaxConcurrencyByPool) == 0 && len(rr.MaxMaxConcurrencyByPool) == 0 {
		rr.MinMaxConcurrencyByPool = slices.Clone(r.MinMaxConcurrencyByPool)
		rr.MaxMaxConcurrencyByPool = slices.Clone(r.MaxMaxConcurrencyByPool)
		rr.MinOverallDuration = r.MinOverallDuration
		rr.MaxOverallDuration = r.MaxOverallDuration
	} else {
		chk.Equal(len(rr.MinMaxConcurrencyByPool), len(r.MinMaxConcurrencyByPool))
		chk.Equal(len(rr.MaxMaxConcurrencyByPool), len(r.MaxMaxConcurrencyByPool))
		for i := range len(r.MinMaxConcurrencyByPool) {
			rr.MinMaxConcurrencyByPool[i] = min(rr.MinMaxConcurrencyByPool[i], r.MinMaxConcurrencyByPool[i])
			rr.MaxMaxConcurrencyByPool[i] = max(rr.MaxMaxConcurrencyByPool[i], r.MaxMaxConcurrencyByPool[i])
		}
		rr.MinOverallDuration = min(rr.MinOverallDuration, r.MinOverallDuration)
		rr.MaxOverallDuration = max(rr.MaxOverallDuration, r.MaxOverallDuration)
	}
}

func MergeResultMap(t require.TestingT, dst map[*Plan]*ResultRange, src map[*Plan]*Result) {
	//chk := require.New(t)
	if len(dst) == 0 {
		for p, sr := range src {
			drr := &ResultRange{}
			dst[p] = drr
			drr.MergeResult(t, sr)
		}
	} else {
		//chk.Equal(len(dst), len(src))
		for p, sr := range src {
			drr := dst[p]
			//chk.NotNil(drr)
			if drr == nil {
				drr = &ResultRange{}
				dst[p] = drr
			}
			drr.MergeResult(t, sr)
		}
	}
}

func MergeResultRangeMap(t require.TestingT, dst, src map[*Plan]*ResultRange) {
	//chk := require.New(t)
	if len(dst) == 0 {
		for p, srr := range src {
			drr := &ResultRange{}
			dst[p] = drr
			drr.MergeRange(t, srr)
		}
	} else {
		//chk.Equal(len(dst), len(src))
		for p, srr := range src {
			drr := dst[p]
			//chk.NotNil(drr)
			if drr == nil {
				drr = &ResultRange{}
				dst[p] = drr
			}
			drr.MergeRange(t, srr)
		}
	}
}
