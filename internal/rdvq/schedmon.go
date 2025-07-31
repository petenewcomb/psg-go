// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"
)

var schedulerLatency atomic.Int64           // time.Duration
var schedulerLatencyUpdateTime atomic.Int64 // time.Duration since epoch

var epoch = time.Now()

//nolint:contextcheck // background context used only for tracing
func SchedulerLatency() (time.Duration, time.Time) {
	updateTime := epoch.Add(time.Duration(schedulerLatencyUpdateTime.Load()))
	latency := time.Duration(schedulerLatency.Load())
	trace.Logf(context.Background(), "rdvq.SchedulerLatency",
		"returning latency=%v, updateTime=%v, age=%v",
		latency, updateTime.Sub(epoch), time.Since(updateTime))
	return latency, updateTime
}

type schedulerLatencySensor struct {
	waitStartTime time.Duration // since epoch
	triggerTime   atomic.Int64  // time.Duration since epoch
}

func (s *schedulerLatencySensor) waitStarting() {
	s.waitStartTime = time.Since(epoch)
}

//nolint:contextcheck // background context used only for tracing
func (s *schedulerLatencySensor) waitEnded() {
	traceRegion := "rdvq.schedulerLatencySensor.waitEnded"
	waitEndTime := time.Since(epoch)
	triggerTime := time.Duration(s.triggerTime.Load())
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "rdvq.schedulerLatencySensor.waitEnded",
			"schedulerLatencySensor=%p, waitStartTime=%d, triggerTime=%d, waitEndTime=%d",
			s, s.waitStartTime, triggerTime, waitEndTime)
	}
	if triggerTime > s.waitStartTime && triggerTime < waitEndTime {
		latency := waitEndTime - triggerTime
		schedulerLatency.Store(int64(latency))
		// Store update time after latency value so that if update time is read
		// first, latency value will always be current or newer with respect to
		// the update time. The important thing is that if the update time is
		// recent, then the latency value is also recent.
		schedulerLatencyUpdateTime.Store(int64(waitEndTime))
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion, "stored latency=%v", latency)
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (s *schedulerLatencySensor) triggered() {
	triggerTime := time.Since(epoch)
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "rdvq.schedulerLatencySensor.triggered",
			"schedulerLatencySensor=%p triggerTime=%v", s, triggerTime)
	}
	s.triggerTime.Store(int64(triggerTime))
}
