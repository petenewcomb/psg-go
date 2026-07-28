// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ema

import (
	"math"
	"time"
)

type Tau time.Duration

func (tau Tau) Alpha(deltaTime time.Duration) Alpha {
	return Alpha(math.Exp(-float64(deltaTime) / float64(tau)))
}

type Alpha float64

type EMA float64

func (ema *EMA) Get() float64 {
	return float64(*ema)
}

func (ema *EMA) Set(value float64) {
	*ema = EMA(value)
}

func (ema *EMA) Project(alpha Alpha) float64 {
	return float64(alpha) * float64(*ema)
}

func (ema *EMA) Update(alpha Alpha, sample float64) {
	*ema = EMA(ema.Project(alpha) + float64(1-alpha)*sample)
}
