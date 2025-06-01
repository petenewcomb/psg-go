// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ema

import "math"

type Trended struct {
	Value EMA
	Trend EMA
}

// TrendRatio returns the trend divided by the value, which will be NaN or
// +/-Inf if the value is zero.
func (t *Trended) TrendRatio() float64 {
	return t.Trend.Get() / t.Value.Get()
}

// IsStable returns true if the trend is within the given ratio of the value.
// IsStable returns false if the value is zero. The trend is used instead of
// variance or standard error because variance and error are inflated by even
// baseline levels of noise. After all, most of the reason to use an EMA is that
// such noise is expected and needs to be smoothed out.
func (t *Trended) IsStable(threshold float64) bool {
	// NaN and +Inf are always greater than any other number
	return math.Abs(t.TrendRatio()) <= threshold
}

func (t *Trended) Get() float64 {
	return t.Value.Get()
}

func (t *Trended) Set(value float64) {
	t.Value.Set(value)
}

func (t *Trended) Reset(value float64) {
	t.Value.Set(value)
	t.Trend.Set(0)
}

func (t *Trended) Project(alpha Alpha) float64 {
	return t.Value.Project(alpha)
}

func (t *Trended) GetTrend() float64 {
	return t.Trend.Get()
}

func (t *Trended) SetTrend(trend float64) {
	t.Trend.Set(trend)
}

func (t *Trended) ProjectTrend(alpha Alpha) float64 {
	return t.Trend.Project(alpha)
}

func (t *Trended) Update(alpha Alpha, sample float64) {
	oldValue := t.Value.Get()
	t.Value.Update(alpha, sample)
	t.Trend.Update(alpha, t.Value.Get()-oldValue)
}
