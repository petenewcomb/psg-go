// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type CombinerPoolState struct {
	completedCount atomic.Int64 // monotonic
	waitTime       atomic.Int64 // monotonic time.Duration

	mu                     sync.Mutex
	launchedGoroutineCount int
	timeOrigin             time.Time
	completedCountOrigin   int64
	waitTimeOrigin         time.Duration
	alpha                  float64
	current                cpPerfSample
	past                   cpPerfSample
	waitChan               chan struct{}
}

type cpPerfSample struct {
	liveGoroutineCount int
	throughput         float64
	utilization        float64
}

func (cps *CombinerPoolState) SetThroughputMeasurementWindow(d time.Duration) {
	if d <= 0 {
		panic(fmt.Sprintf("invalid throughput measurement window %v: must be > 0", d))
	}

	tau := float64(d)
	alpha := 1 - math.Exp(-1/tau)

	cps.mu.Lock()
	defer cps.mu.Unlock()

	cps.alpha = alpha
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
		cps.timeOrigin = time.Now()
		cps.waitChan = make(chan struct{}, 1)
	}

	if limit >= 0 && cps.launchedGoroutineCount >= limit {
		return cps.waitChan
	}

	if cps.launchedGoroutineCount == 0 {
		cps.launchedGoroutineCount = 1
		return nil
	}

	cps.updateStats()

	if cps.current.utilization < 0.6 {
		// We've got capacity to spare.
		return cps.waitChan
	}

	moreGoroutinesPerEachThroughput := cps.current.throughput / float64(cps.current.liveGoroutineCount)
	fewerGoroutinesPerEachThroughput := cps.past.throughput / float64(cps.past.liveGoroutineCount)
	if cps.current.liveGoroutineCount < cps.past.liveGoroutineCount {
		moreGoroutinesPerEachThroughput, fewerGoroutinesPerEachThroughput = fewerGoroutinesPerEachThroughput, moreGoroutinesPerEachThroughput
	}

	if moreGoroutinesPerEachThroughput < fewerGoroutinesPerEachThroughput {
		// Past point of improvement, don't go any further.
		return cps.waitChan
	}

	cps.launchedGoroutineCount++
	return nil
}

func (cps *CombinerPoolState) GoroutineStarted() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.updateStats()
	cps.past = cps.current
	cps.current.liveGoroutineCount++
	//fmt.Println("goroutine started:", cps.current.liveGoroutineCount)
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) GoroutineExited() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.current.liveGoroutineCount <= 0 {
		panic("underflow")
	}
	cps.updateStats()
	cps.past = cps.current
	cps.current.liveGoroutineCount--
	cps.launchedGoroutineCount--
	//fmt.Println("goroutine exited:", cps.current.liveGoroutineCount)
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) updateStats() {
	now := time.Now()
	elapsedTime := now.Sub(cps.timeOrigin)
	cps.timeOrigin = now
	residualFactor := math.Pow(1-cps.alpha, float64(elapsedTime))

	curCompletedCount := cps.completedCount.Load()
	completedCount := curCompletedCount - cps.completedCountOrigin
	cps.completedCountOrigin = curCompletedCount
	cps.current.throughput = cps.alpha*float64(completedCount) + residualFactor*cps.current.throughput

	curWaitTime := time.Duration(cps.waitTime.Load())
	waitTime := curWaitTime - cps.waitTimeOrigin
	cps.waitTimeOrigin = curWaitTime
	// Utilization should be calculated as (elapsedTime-waitTime)/elapsedTime,
	// but we also want it to be considered a constant for the time period
	// rather than an impulse. Therefore we must multiply it by elapsedTime,
	// which cancels out and leaves us with just elapsedTime-waitTime below.
	cps.current.utilization = cps.alpha*float64(elapsedTime-waitTime) + residualFactor*cps.current.utilization
}

func (cps *CombinerPoolState) notifyWaiter() {
	select {
	case cps.waitChan <- struct{}{}:
	default:
	}
}
