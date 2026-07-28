// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"sync"
	"sync/atomic"
	"time"
)

type Controller struct {
	recordingStartTime atomic.Int64 // time.Duration since epoch
	recordingEndTime   atomic.Int64 // time.Duration since epoch

	mu                  sync.Mutex
	onStartRecordingFns []func()
	onStopRecordingFns  []func()
}

func (c *Controller) OnStartRecording(fn func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recordingEndTime.Load() != 0 {
		return false
	}
	if c.recordingStartTime.Load() == 0 {
		c.onStartRecordingFns = append(c.onStartRecordingFns, fn)
	} else {
		fn()
	}
	return true
}

func (c *Controller) OnStopRecording(fn func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recordingEndTime.Load() != 0 {
		return false
	}
	c.onStopRecordingFns = append(c.onStopRecordingFns, fn)
	return true
}

func (c *Controller) StartRecording() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, fn := range c.onStartRecordingFns {
		fn()
	}
	c.onStartRecordingFns = c.onStartRecordingFns[:0]
	c.recordingStartTime.Store(int64(time.Since(epoch)))
}

func (c *Controller) EndRecording() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordingEndTime.Store(int64(time.Since(epoch)))
	for _, fn := range c.onStopRecordingFns {
		fn()
	}
	c.onStopRecordingFns = c.onStopRecordingFns[:0]
}

func (c *Controller) Recording() bool {
	return c.recordingEndTime.Load() == 0 && c.recordingStartTime.Load() != 0
}

func (c *Controller) RecordingDuration() time.Duration {
	return time.Duration(c.recordingEndTime.Load() - c.recordingStartTime.Load())
}

func (c *Controller) RecordedDurationSince(origin time.Time) time.Duration {
	recordingStartTime := time.Duration(c.recordingStartTime.Load())
	if recordingStartTime == 0 {
		// Recording has not started
		return 0
	}

	recordingEndTime := time.Duration(c.recordingEndTime.Load())
	startTime := origin.Sub(epoch)
	if recordingEndTime != 0 && startTime >= recordingEndTime {
		// Start time is after recording stopped
		return 0
	}

	if startTime < recordingStartTime {
		// Don't count the time before recording started
		startTime = recordingStartTime
	}

	endTime := recordingEndTime
	if endTime == 0 {
		// Still recording, use now
		endTime = time.Since(epoch)
	}

	return endTime - startTime
}

var epoch = time.Now()
