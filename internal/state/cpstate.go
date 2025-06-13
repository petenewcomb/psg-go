// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/ema"
	"github.com/petenewcomb/psg-go/internal/ttrk"
)

var epoch = time.Now()

type CombinerPoolState struct {
	cumulativeCompletedCount atomic.Int64 // monotonic
	cumulativeLatency        atomic.Int64 // monotonic time.Duration
	timeOrigin               atomic.Int64 // time.Duration since epoch

	mu sync.Mutex

	tau                ema.Tau
	stabilityThreshold float64

	completedCountOrigin int64
	latencyOrigin        int64
	secondaryWait        ttrk.TimeTracker
	secondaryWaitOrigin  time.Duration

	throughput  ema.Trended // count/time.Duration
	latency     ema.Trended
	goroutines  ema.Trended
	utilization ema.Trended

	// Performance curve for intelligent scaling decisions
	perfCurves perfCurves

	targetGoroutineCount           int
	spawnedGoroutineCount          int
	liveGoroutineCount             int
	latestGoroutineCountChangeTime time.Time

	waitChan chan struct{}
}

func (cps *CombinerPoolState) SetLimits(minConcurrency, maxConcurrency int) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.perfCurves.SetLimits(minConcurrency, maxConcurrency)

	// Ask perfCurves if the target should change given new limits
	newTarget := cps.perfCurves.RecommendTarget()
	if newTarget != cps.targetGoroutineCount {
		cps.targetGoroutineCount = newTarget
		cps.notifyWaiter()
	}
}

func (cps *CombinerPoolState) SetHighUtilizationThreshold(high float64) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.perfCurves.SetHighUtilizationThreshold(high)
}

func (cps *CombinerPoolState) SetMeasurementTimeConstant(d time.Duration) {
	if d <= 0 {
		panic(fmt.Sprintf("invalid tau %v: must be > 0", d))
	}
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.tau = ema.Tau(d)
}

func (cps *CombinerPoolState) SetMeasurementStabilityThreshold(ratio float64) {
	if ratio <= 0 || ratio > 1 {
		panic(fmt.Sprintf("invalid stability threshold %v: must be > 0 and <= 1", ratio))
	}
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.stabilityThreshold = ratio
}

func (cps *CombinerPoolState) SetHistoryRetentionPeriod(d time.Duration) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.perfCurves.SetRetentionPeriod(d)
}

func (cps *CombinerPoolState) SetMinimumReturn(ratio float64) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.perfCurves.SetMinimumReturn(ratio)
}

func (cps *CombinerPoolState) SetGrowthFactors(aggressive, conservative float64) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.perfCurves.SetGrowthFactors(aggressive, conservative)
}

func (cps *CombinerPoolState) IncrementCompleted() {
	cps.cumulativeCompletedCount.Add(1)
}

func (cps *CombinerPoolState) SecondaryWaitStarted(startTime time.Time) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.secondaryWait.Started(startTime)
	if cps.secondaryWait.StartedCount != 1 {
		panic("SecondaryWaitStarted called when already started")
	}
}

func (cps *CombinerPoolState) SecondaryWaitEnded(startTime time.Time) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.secondaryWait.Ended(startTime, epoch.Add(time.Duration(cps.timeOrigin.Load())))
}

func (cps *CombinerPoolState) MaybeSpawnGoroutine() bool {
	if time.Since(epoch)-time.Duration(cps.timeOrigin.Load()) > min(time.Duration(cps.tau), cps.perfCurves.RetentionPeriod()/2) {
		return cps.ShouldSpawnGoroutine() == nil
	}
	return false
}

// Returns nil if the caller should start a new combiner goroutine, otherwise a
// channel that will be signaled if the caller should wake and retry.
func (cps *CombinerPoolState) ShouldSpawnGoroutine() <-chan struct{} {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	if cps.timeOrigin.Load() == 0 {
		cps.timeOrigin.Store(int64(time.Since(epoch)))
		cps.targetGoroutineCount = cps.perfCurves.RecommendTarget()
		cps.waitChan = make(chan struct{}, 1)
	}

	// Spawn immediately if we haven't yet reached target, and don't make
	// matters worse if we're above target.
	switch {
	case cps.spawnedGoroutineCount < cps.targetGoroutineCount:
		cps.spawnedGoroutineCount++
		return nil
	case cps.spawnedGoroutineCount > cps.targetGoroutineCount:
		return cps.waitChan
	}

	if !cps.updateStats() {
		// Stats for this configuration are not yet stable
		return cps.waitChan
	}

	cps.targetGoroutineCount = cps.perfCurves.RecommendTarget()

	if cps.spawnedGoroutineCount < cps.targetGoroutineCount {
		cps.spawnedGoroutineCount++
		cps.report("spawning")
		return nil
	}

	return cps.waitChan
}

func (cps *CombinerPoolState) ShouldExitGoroutine() bool {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	// Exit immediately if we haven't reduced to target yet, and don't make
	// matters worse if we're below target.
	switch {
	case cps.spawnedGoroutineCount > cps.targetGoroutineCount:
		cps.spawnedGoroutineCount--
		return true
	case cps.spawnedGoroutineCount < cps.targetGoroutineCount:
		return false
	}

	if !cps.updateStats() {
		// Stats for this configuration not yet stable
		return false
	}

	cps.targetGoroutineCount = cps.perfCurves.RecommendTarget()

	if cps.spawnedGoroutineCount > cps.targetGoroutineCount {
		cps.spawnedGoroutineCount--
		cps.report("exiting")
		return true
	}
	return false
}

func (cps *CombinerPoolState) RecordLatency(latency time.Duration) {
	cps.cumulativeLatency.Add(int64(latency))
}

func (cps *CombinerPoolState) report(msg string) {
	/*
		   fmt.Printf("%-8s\tgoroutines: %d->%d->%d(%.1f)\tthroughput: %.1f/s (%.1f/s each)\tutil: %.1f%%\tlatency: %v\t%v\n",
			msg+":",
			cps.spawnedGoroutineCount,
			cps.liveGoroutineCount,
			cps.targetGoroutineCount,
			cps.goroutines.Get(),
			cps.throughput.Get()*float64(time.Second),
			cps.throughput.Get()*float64(time.Second)/float64(cps.liveGoroutineCount),
			(cps.utilization.Get()-float64(cps.liveGoroutineCount-1))*100,
			time.Duration(max(0, cps.latency.Get()-1)),
			&cps.perfCurves,
		)
	*/
}

func (cps *CombinerPoolState) GoroutineStarted() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.liveGoroutineCount > 0 {
		cps.updateStats()
	}
	oldGoroutineCount := cps.liveGoroutineCount
	cps.liveGoroutineCount++

	if cps.liveGoroutineCount > 1 {
		cps.adjustStats(oldGoroutineCount, cps.liveGoroutineCount)
	}

	cps.latestGoroutineCountChangeTime = time.Now()
	if cps.liveGoroutineCount == cps.targetGoroutineCount {
		cps.report("started")
	}
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) GoroutineExited() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.liveGoroutineCount <= 0 {
		panic("underflow")
	}
	cps.updateStats()
	oldGoroutineCount := cps.liveGoroutineCount
	cps.liveGoroutineCount--

	if cps.liveGoroutineCount == 0 {
		cps.perfCurves = perfCurves{} // Reset performance curve
		cps.throughput.Reset(0)
		cps.utilization.Reset(0)
	} else {
		cps.adjustStats(oldGoroutineCount, cps.liveGoroutineCount)
	}

	cps.latestGoroutineCountChangeTime = time.Now()
	if cps.liveGoroutineCount == cps.targetGoroutineCount {
		cps.report("exited")
	}
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) adjustStats(oldGoroutineCount int, newGoroutineCount int) {
	/*
		if newGoroutineCount < oldGoroutineCount {
			// Scaling down: assume utilization will be 100% of the remaining capacity
			cps.utilization.Trend.Set(float64(newGoroutineCount) - cps.utilization.Get())
			cps.utilization.Set(float64(newGoroutineCount))
		} else {
			// Scaling up: assume utilization of the new capacity will be 0%
			cps.utilization.Trend.Set(float64(oldGoroutineCount) - cps.utilization.Get())
			cps.utilization.Set(float64(oldGoroutineCount))
		}
	*/

	cps.utilization.Set(cps.utilization.Get() + float64(newGoroutineCount-oldGoroutineCount))

	/*
		oldSecondaryUtil := cps.secondaryUtil.Get()
		oldTotalUtil := float64(oldGoroutineCount-1) + oldSecondaryUtil
		newTotalUtil := oldTotalUtil * float64(newGoroutineCount) / float64(oldGoroutineCount)

		newSecondaryUtil := max(0, min(newTotalUtil-float64(newGoroutineCount-1), 1))
		cps.secondaryUtil.Set(newSecondaryUtil)
		cps.secondaryUtil.Trend.Set(newSecondaryUtil - oldSecondaryUtil)
			oldThroughput := cps.throughput.Get()
			newThroughput := oldThroughput * newTotalUtil / oldTotalUtil
			cps.throughput.Set(newThroughput)
			cps.throughput.Trend.Set(newThroughput - oldThroughput)
	*/
}

func (cps *CombinerPoolState) updateStats() bool {
	// Capture raw datapoints
	now := time.Now()
	curCompletedCount := cps.cumulativeCompletedCount.Load()
	curLatency := cps.cumulativeLatency.Load()

	// Calculate deltas, reset origins, and update EMAs
	timeOrigin := epoch.Add(time.Duration(cps.timeOrigin.Load()))
	elapsedTime := now.Sub(timeOrigin)
	cps.timeOrigin.Store(int64(now.Sub(epoch)))

	alpha := cps.tau.Alpha(elapsedTime)

	completedCount := curCompletedCount - cps.completedCountOrigin
	cps.completedCountOrigin = curCompletedCount
	cps.throughput.Update(alpha, float64(completedCount)/float64(elapsedTime))

	if completedCount == 0 {
		cps.latency.Update(alpha, cps.latency.Get())
	} else {
		latency := curLatency - cps.latencyOrigin
		cps.latencyOrigin = curLatency
		cps.latency.Update(alpha, float64(latency+1)/float64(completedCount))
	}

	cps.goroutines.Update(alpha, float64(cps.liveGoroutineCount))

	cps.secondaryWait.Update(now, timeOrigin)
	secondaryWait := cps.secondaryWait.CumulativeDuration - cps.secondaryWaitOrigin
	cps.secondaryWaitOrigin = cps.secondaryWait.CumulativeDuration
	if secondaryWait > elapsedTime {
		panic(fmt.Sprintf("%v secondaryWait %d greater than elapsed time %d!", time.Now(), secondaryWait, elapsedTime))
	}
	cps.utilization.Update(alpha, float64(time.Duration(cps.liveGoroutineCount)*elapsedTime-secondaryWait)/float64(elapsedTime))

	if time.Since(cps.latestGoroutineCountChangeTime) < 3*time.Duration(cps.tau) {
		//!cps.throughput.IsStable(cps.stabilityThreshold) ||
		//!cps.latency.IsStable(cps.stabilityThreshold) ||
		//!cps.utilization.IsStable(cps.stabilityThreshold) ||
		//!cps.goroutines.IsStable(cps.stabilityThreshold) {
		return false
	}

	// Update performance curve with latest sample
	throughputValue := cps.throughput.Get()
	// fmt.Printf("DEBUG: About to upsert - throughput.Get()=%.12f (%.1f/s)\n",
	// 	throughputValue, throughputValue*float64(time.Second))
	cps.perfCurves.AddSample(perfSample{
		Time:           timeOrigin,
		GoroutineCount: cps.liveGoroutineCount,
		Throughput:     throughputValue,
		SecondaryUtil:  cps.utilization.Get() - float64(cps.liveGoroutineCount-1),
		Latency:        time.Duration(max(0, cps.latency.Get()-1)),
	})

	if now.Sub(epoch)/time.Second != timeOrigin.Sub(epoch)/time.Second {
		defer cps.report("updated")
	}

	return true
}

// Notify a single waiter that the state has changed so it will wake and retry
// calling ShouldSpawnGoroutine. Notifying a single waiter instead of all
// waiters avoids a thundering herd attempting to lock the mutex and also allows
// us to reuse waitChan indefinitely. Only one goroutine need call
// ShouldSpawnGoroutine, since all goroutines will benefit from any new
// goroutine spawned.
func (cps *CombinerPoolState) notifyWaiter() {
	select {
	case cps.waitChan <- struct{}{}:
	default:
	}
}
