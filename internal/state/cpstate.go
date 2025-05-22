// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/basicq"
)

type CombinerPoolState struct {
	completedCount atomic.Int64 // monotonic
	waitTime       atomic.Int64 // monotonic time.Duration

	mu                          sync.Mutex
	throughputMeasurementWindow time.Duration
	launchedGoroutineCount      int
	perf                        cpPerfSample
	perfHistory                 basicq.Queue[cpPerfSample]
	waitChan                    chan struct{}
}

type cpPerfSample struct {
	liveGoroutineCount   int
	timeOrigin           time.Time
	completedCountOrigin int64
	waitTimeOrigin       time.Duration
}

func (cps *CombinerPoolState) SetThroughputMeasurementWindow(d time.Duration) {
	if d <= 0 {
		panic(fmt.Sprintf("invalid throughput measurement window %v: must be > 0", d))
	}
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.throughputMeasurementWindow = d
}

func (cps *CombinerPoolState) IncrementCompleted() {
	cps.completedCount.Add(1)
}

func (cps *CombinerPoolState) AddWaitTime(d time.Duration) {
	cps.waitTime.Add(int64(d))

}

// Returns nil if the caller should start a new combiner goroutine, otherwise a
// channel that will be signaled if the caller should wake and retry.
func (cps *CombinerPoolState) ShouldSpawnGoroutine(limit int) <-chan struct{} {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	if cps.waitChan == nil {
		cps.perf.timeOrigin = time.Now()
		cps.waitChan = make(chan struct{}, 1)
	}

	if limit >= 0 && cps.launchedGoroutineCount >= limit {
		return cps.waitChan
	}

	if cps.launchedGoroutineCount == 0 {
		cps.launchedGoroutineCount = 1
		return nil
	}

	throughputMeasurementWindow := cps.throughputMeasurementWindow
	if throughputMeasurementWindow <= 0 {
		panic("uninitialized or invalid throughput measurement window")
	}

	now := time.Now()
	cps.trimPerfHistory(now, throughputMeasurementWindow)

	curCompletedCount := cps.completedCount.Load()
	curWaitTime := time.Duration(cps.waitTime.Load())

	remainingWindowDuration := throughputMeasurementWindow
	sample := cps.perf
	remainingSampleDuration := now.Sub(sample.timeOrigin)
	remainingCompletedCount := float64(curCompletedCount - sample.completedCountOrigin)
	remainingWaitTime := curWaitTime - sample.waitTimeOrigin
	windowIndex := 0
	sampleIndex := cps.perfHistory.Len()
	var liveGoroutineTime [2]time.Duration
	var completedCount [2]float64
	var waitTime [2]time.Duration
	var windowDuration [2]time.Duration
	for {
		if remainingSampleDuration <= remainingWindowDuration {
			// Accumulate
			liveGoroutineTime[windowIndex] += remainingSampleDuration * time.Duration(sample.liveGoroutineCount)
			completedCount[windowIndex] += remainingCompletedCount
			waitTime[windowIndex] += remainingWaitTime
			windowDuration[windowIndex] += remainingSampleDuration

			// Move to next sample
			sampleIndex--
			if sampleIndex < 0 {
				break
			}
			laterSample := sample
			sample = cps.perfHistory.Peek(sampleIndex)
			remainingSampleDuration = laterSample.timeOrigin.Sub(sample.timeOrigin)
			remainingCompletedCount = float64(laterSample.completedCountOrigin - sample.completedCountOrigin)
			remainingWaitTime = laterSample.waitTimeOrigin - sample.waitTimeOrigin
		} else {
			// Accumulate
			liveGoroutineTime[windowIndex] += remainingWindowDuration * time.Duration(sample.liveGoroutineCount)

			proratedCompletedCount := remainingCompletedCount * float64(remainingWindowDuration) / float64(remainingSampleDuration)
			completedCount[windowIndex] += proratedCompletedCount
			remainingCompletedCount -= proratedCompletedCount

			proratedWaitTime := remainingWaitTime * remainingWindowDuration / remainingSampleDuration
			waitTime[windowIndex] += proratedWaitTime
			remainingWaitTime -= proratedWaitTime

			windowDuration[windowIndex] += remainingWindowDuration
			remainingSampleDuration -= remainingWindowDuration

			// Move to next window
			windowIndex++
			if windowIndex >= len(windowDuration) {
				break
			}
			remainingWindowDuration = throughputMeasurementWindow
		}
	}

	if min(liveGoroutineTime[0], liveGoroutineTime[1]) < throughputMeasurementWindow {
		// Not enough data to make a decision yet
		return cps.waitChan
	}

	/*
		effectiveLiveGoroutineCount := float64(liveGoroutineTime[0]) / float64(windowDuration[0])
		if completedCount[0] < 0 {
			// Make sure we've seen at least one completion
			return cps.waitChan
		}
	*/

	if waitTime[0] >= throughputMeasurementWindow {
		// At least one goroutine has been idle for the measurement window: hold
		// off launching new goroutines for now
		return cps.waitChan
	}

	idealCompletedRatePerGoroutine := completedCount[0] / float64(liveGoroutineTime[0]-waitTime[0])
	previousIdealCompletedRatePerGoroutine := completedCount[1] / float64(liveGoroutineTime[1]-waitTime[1])

	if idealCompletedRatePerGoroutine < previousIdealCompletedRatePerGoroutine {
		// Past point of improvement, don't go any further.
		return cps.waitChan
	}

	cps.launchedGoroutineCount++
	return nil
}

func (cps *CombinerPoolState) GoroutineStarted() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.pushPerfHistory()
	cps.perf.liveGoroutineCount++
	//fmt.Println("goroutine started:", cps.perf.liveGoroutineCount)
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) GoroutineExited() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.perf.liveGoroutineCount <= 0 {
		panic("underflow")
	}
	cps.pushPerfHistory()
	cps.perf.liveGoroutineCount--
	cps.launchedGoroutineCount--
	//fmt.Println("goroutine exited:", cps.perf.liveGoroutineCount)
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) trimPerfHistory(now time.Time, throughputMeasurementWindow time.Duration) {
	for cps.perfHistory.Len() > 1 && now.Sub(cps.perfHistory.Peek(1).timeOrigin) >= 2*throughputMeasurementWindow {
		_, _ = cps.perfHistory.PopFront()
	}
}

func (cps *CombinerPoolState) pushPerfHistory() {
	now := time.Now()
	cps.trimPerfHistory(now, cps.throughputMeasurementWindow)
	cps.perfHistory.PushBack(cps.perf)
	cps.perf.timeOrigin = time.Now()
	cps.perf.completedCountOrigin = cps.completedCount.Load()
}

func (cps *CombinerPoolState) notifyWaiter() {
	select {
	case cps.waitChan <- struct{}{}:
	default:
	}
}
