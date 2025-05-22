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

const cpPerfWindowDuration = 10 * time.Microsecond

type CombinerPoolState struct {
	pendingCount   atomic.Int64 // monotonic
	completedCount atomic.Int64 // monotonic
	waitTime       atomic.Int64 // monotonic time.Duration

	mu                     sync.Mutex
	launchedGoroutineCount int
	perf                   cpPerfSample
	perfHistory            basicq.Queue[cpPerfSample]
	waitChan               chan struct{}
}

type cpPerfSample struct {
	liveGoroutineCount   int
	timeOrigin           time.Time
	pendingCountOrigin   int64
	completedCountOrigin int64
	waitTimeOrigin       time.Duration
}

func (cps *CombinerPoolState) IncrementPending() {
	//fmt.Println("increment pending")
	cps.pendingCount.Add(1)
}

func (cps *CombinerPoolState) IncrementCompleted() {
	//fmt.Println("increment completed")
	cps.completedCount.Add(1)
}

func (cps *CombinerPoolState) AddWaitTime(d time.Duration) {
	cps.waitTime.Add(int64(d))

}

// Returns nil if the caller should start a new combiner goroutine, otherwise a
// channel that will be signaled if the caller should wake and retry.
func (cps *CombinerPoolState) ShouldSpawnGoroutine(period time.Duration, limit int) <-chan struct{} {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	if cps.waitChan == nil {
		cps.waitChan = make(chan struct{}, 1)
	}

	if limit >= 0 && cps.launchedGoroutineCount >= limit {
		//fmt.Println("no spawn: cps.liveGoroutineCount >= limit")
		return cps.waitChan
	}

	if cps.launchedGoroutineCount == 0 {
		cps.launchedGoroutineCount = 1
		//fmt.Println("spawn: 1")
		return nil
	}

	/*
		if cps.perf.liveGoroutineCount < cps.launchedGoroutineCount {
			return cps.waitChan
		}
	*/

	if time.Since(cps.perf.timeOrigin) < period {
		fmt.Println("no spawn: age < period")
		return cps.waitChan
	}

	now := time.Now()
	cps.trimPerfHistory(now)

	curPendingCount := cps.pendingCount.Load()
	curCompletedCount := cps.completedCount.Load()
	curWaitTime := time.Duration(cps.waitTime.Load())

	remainingWindowDuration := cpPerfWindowDuration
	sample := cps.perf
	remainingSampleDuration := now.Sub(sample.timeOrigin)
	remainingPendingCount := float64(curPendingCount - sample.pendingCountOrigin)
	remainingCompletedCount := float64(curCompletedCount - sample.completedCountOrigin)
	remainingWaitTime := curWaitTime - sample.waitTimeOrigin
	windowIndex := 0
	sampleIndex := cps.perfHistory.Len()
	var liveGoroutineTime [2]time.Duration
	var pendingCount float64
	var completedCount [2]float64
	var waitTime [2]time.Duration
	var windowDuration [2]time.Duration
	for {
		if remainingSampleDuration <= remainingWindowDuration {
			// Accumulate
			liveGoroutineTime[windowIndex] += remainingSampleDuration * time.Duration(sample.liveGoroutineCount)
			if windowIndex == 0 {
				pendingCount += remainingPendingCount
			}
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
			remainingPendingCount = float64(laterSample.pendingCountOrigin - sample.pendingCountOrigin)
			remainingCompletedCount = float64(laterSample.completedCountOrigin - sample.completedCountOrigin)
			remainingWaitTime = laterSample.waitTimeOrigin - sample.waitTimeOrigin
		} else {
			// Accumulate
			liveGoroutineTime[windowIndex] += remainingWindowDuration * time.Duration(sample.liveGoroutineCount)
			if windowIndex == 0 {
				proratedPendingCount := remainingPendingCount * float64(remainingWindowDuration) / float64(remainingSampleDuration)
				pendingCount += proratedPendingCount
				remainingPendingCount -= proratedPendingCount
			}

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
			remainingWindowDuration = cpPerfWindowDuration
		}
	}

	if min(liveGoroutineTime[0], liveGoroutineTime[1]) < cpPerfWindowDuration {
		// Not enough data to make a decision yet
		//fmt.Println("no spawn: liveGoroutineTime[0,1] < cpPerfWindowDuration")
		return cps.waitChan
	}

	idleRatio := float64(waitTime[0]) / float64(liveGoroutineTime[0])
	effectiveLiveGoroutineCount := float64(liveGoroutineTime[0]) / float64(windowDuration[0])
	if idleRatio > (1 / effectiveLiveGoroutineCount) {
		//fmt.Println("no spawn: idleRatio > 1/effectiveLiveGoroutineCount", idleRatio, effectiveLiveGoroutineCount)
		return cps.waitChan
	}

	idealCompletedRatePerGoroutine := completedCount[0] / float64(liveGoroutineTime[0]-waitTime[0])
	previousIdealCompletedRatePerGoroutine := completedCount[1] / float64(liveGoroutineTime[1]-waitTime[1])

	if idealCompletedRatePerGoroutine < previousIdealCompletedRatePerGoroutine {
		// Past point of improvement, don't go any further.
		//fmt.Println("no spawn: idealCompletedRatePerGoroutine <= previousIdealCompletedRatePerGoroutine", idealCompletedRatePerGoroutine*float64(time.Second), previousIdealCompletedRatePerGoroutine*float64(time.Second))
		return cps.waitChan
	}

	/*
		pendingRate := pendingCount / float64(windowDuration[0])

		goroutineDemand := int(math.Ceil(pendingRate / idealCompletedRatePerGoroutine))
		if goroutineDemand <= cps.launchedGoroutineCount {
			fmt.Println("no spawn: goroutineDemand <= launchedGoroutineCount", goroutineDemand, cps.launchedGoroutineCount, pendingRate/idealCompletedRatePerGoroutine)
			return cps.waitChan
		}
	*/

	cps.launchedGoroutineCount++
	//fmt.Println("spawn:", cps.launchedGoroutineCount)
	return nil
}

func (cps *CombinerPoolState) GoroutineStarted() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.pushPerfHistory()
	cps.perf.liveGoroutineCount++
	//fmt.Println("started:", cps.perf.liveGoroutineCount)
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
	//fmt.Println("exited:", cps.perf.liveGoroutineCount)
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) trimPerfHistory(now time.Time) {
	for cps.perfHistory.Len() > 1 && now.Sub(cps.perfHistory.Peek(1).timeOrigin) >= 2*cpPerfWindowDuration {
		_, _ = cps.perfHistory.PopFront()
	}
}

func (cps *CombinerPoolState) pushPerfHistory() {
	now := time.Now()
	cps.trimPerfHistory(now)
	cps.perfHistory.PushBack(cps.perf)
	cps.perf.timeOrigin = time.Now()
	cps.perf.pendingCountOrigin = cps.pendingCount.Load()
	cps.perf.completedCountOrigin = cps.completedCount.Load()
}

func (cps *CombinerPoolState) notifyWaiter() {
	select {
	case cps.waitChan <- struct{}{}:
	default:
	}
}
