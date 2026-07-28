// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ttrk

import (
	"time"
)

var epoch = time.Now()

type TimeTracker struct {
	StartedCount       int
	StartTimeSum       time.Duration // relative to epoch
	CumulativeDuration time.Duration // monotonic
}

func (tt *TimeTracker) Started(startTime time.Time) {
	tt.StartedCount++
	tt.StartTimeSum += startTime.Sub(epoch)
}

func (tt *TimeTracker) Ended(startTime, timeOrigin time.Time) {
	tt.StartedCount--
	tt.StartTimeSum -= startTime.Sub(epoch)
	if startTime.Before(timeOrigin) {
		startTime = timeOrigin
	}
	tt.CumulativeDuration += time.Since(startTime)
}

func (tt *TimeTracker) Update(now, timeOrigin time.Time) {
	if now.Before(timeOrigin) {
		panic("now is before timeOrigin")
	}
	if tt.StartedCount > 0 {
		avgStartTime := epoch.Add(tt.StartTimeSum / time.Duration(tt.StartedCount))
		if avgStartTime.Before(timeOrigin) {
			avgStartTime = timeOrigin
		}
		tt.CumulativeDuration += now.Sub(avgStartTime) * time.Duration(tt.StartedCount)
	}
}
