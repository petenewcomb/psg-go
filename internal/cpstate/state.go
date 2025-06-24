// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cpstate

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/ema"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/ttrk"
)

var epoch = time.Now()

// cpDebug enables detailed combiner pool state reporting.
const cpDebug = false

type CombinerPoolState struct {
	// High-frequency atomic counters updated lock-free from multiple goroutines
	cumulativeCompletedCount atomic.Int64 // monotonic
	timeOrigin               atomic.Int64 // time.Duration since epoch

	// Mutex protects complex state analysis and scaling decisions
	mu sync.Mutex

	// Configuration
	idleTimeout     atomic.Int64 // time.Duration
	tau             ema.Tau
	retentionPeriod atomic.Int64 // time.Duration

	completedCountOrigin int64
	spareWait            ttrk.TimeTracker
	spareWaitOrigin      time.Duration

	throughput ema.EMA // count/time.Duration
	spareUtil  ema.EMA // spare goroutine utilization (0-1)

	// Size controller for intelligent scaling decisions
	controller controller

	targetGoroutineCount           int
	spawnedGoroutineCount          atomic.Int32
	liveGoroutineCount             int
	latestGoroutineCountChangeTime time.Time

	waitChan chan struct{}
}

// SetOptions atomically applies the given set of configuration options (later options override earlier ones).
// If validation fails, the method panics and no changes are applied.
func (cps *CombinerPoolState) SetOptions(options ...opts.CombinerPoolOption) {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	// Create a copy of the current configuration
	newConfig := Config{
		controllerConfig:        cps.controller.config,
		IdleTimeout:             time.Duration(cps.idleTimeout.Load()),
		MeasurementTimeConstant: time.Duration(cps.tau),
		RetentionPeriod:         time.Duration(cps.retentionPeriod.Load()),
	}

	// Apply changes to the copy
	opts.ApplyToCombinerPool(&newConfig, options...)

	// Validate the new configuration (panics if invalid)
	newConfig.validate()

	// Apply the validated configuration to the actual state
	cps.controller.SetConfig(newConfig.controllerConfig)
	cps.idleTimeout.Store(int64(newConfig.IdleTimeout))
	cps.tau = ema.Tau(newConfig.MeasurementTimeConstant)
	cps.retentionPeriod.Store(int64(newConfig.RetentionPeriod))

	// Ask controller if the target should change given new limits
	newTarget := cps.controller.RecommendTarget()
	if newTarget != cps.targetGoroutineCount {
		cps.targetGoroutineCount = newTarget
		cps.notifyWaiter()
	}
}

func (cps *CombinerPoolState) IdleTimeout() time.Duration {
	return time.Duration(cps.idleTimeout.Load())
}

func (cps *CombinerPoolState) IncrementCompleted() {
	cps.cumulativeCompletedCount.Add(1)
}

func (cps *CombinerPoolState) SpareWaitStarted(startTime time.Time) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.spareWait.Started(startTime)
	if cps.spareWait.StartedCount != 1 {
		panic("SpareWaitStarted called when already started")
	}
}

func (cps *CombinerPoolState) SpareWaitEnded(startTime time.Time) {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.spareWait.Ended(startTime, epoch.Add(time.Duration(cps.timeOrigin.Load())))
}

func (cps *CombinerPoolState) MaybeSpawnGoroutine() bool {
	lastUpdate := epoch.Add(time.Duration(cps.timeOrigin.Load()))
	if time.Since(lastUpdate) > min(time.Duration(cps.tau), time.Duration(cps.retentionPeriod.Load())/2) { //nolint:mnd  // nyquist rate
		return cps.ShouldSpawnGoroutine() == nil
	}
	return false
}

func (cps *CombinerPoolState) ShouldStartFirstGoroutine() bool {
	// Always do full check if spawned count is zero
	if cps.spawnedGoroutineCount.Load() == 0 {
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
		cps.targetGoroutineCount = cps.controller.RecommendTarget()
		cps.waitChan = make(chan struct{}, 1)
	}

	// Spawn immediately if we haven't yet reached target, and don't make
	// matters worse if we're above target.
	spawnedCount := int(cps.spawnedGoroutineCount.Load())
	switch {
	case spawnedCount < cps.targetGoroutineCount:
		cps.spawnedGoroutineCount.Add(1)
		return nil
	case spawnedCount > cps.targetGoroutineCount:
		return cps.waitChan
	}

	if !cps.updateStats() {
		// Stats for this configuration are not yet stable
		return cps.waitChan
	}

	cps.targetGoroutineCount = cps.controller.RecommendTarget()

	if int(cps.spawnedGoroutineCount.Load()) < cps.targetGoroutineCount {
		cps.spawnedGoroutineCount.Add(1)
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
	spawnedCount := int(cps.spawnedGoroutineCount.Load())
	switch {
	case spawnedCount > cps.targetGoroutineCount:
		return true
	case spawnedCount < cps.targetGoroutineCount:
		return false
	}

	if !cps.updateStats() {
		// Stats for this configuration not yet stable
		return false
	}

	cps.targetGoroutineCount = cps.controller.RecommendTarget()

	if int(cps.spawnedGoroutineCount.Load()) > cps.targetGoroutineCount {
		cps.report("exiting")
		return true
	}
	return false
}

func (cps *CombinerPoolState) report(msg string) {
	if !cpDebug {
		return
	}
	fmt.Printf("%v %-8s\tgoroutines: %d->%d->%d\tthroughput: %.1f/s (%.1f/s each)\tutil: %.1f%%\t%v\n",
		time.Now(),
		msg+":",
		cps.spawnedGoroutineCount.Load(),
		cps.liveGoroutineCount,
		cps.targetGoroutineCount,
		cps.throughput.Get()*float64(time.Second),
		cps.throughput.Get()*float64(time.Second)/float64(cps.liveGoroutineCount),
		cps.spareUtil.Get()*100, //nolint:mnd // by definition
		&cps.controller,
	)
}

func (cps *CombinerPoolState) GoroutineStarted() {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.liveGoroutineCount > 0 {
		cps.updateStats()
	}
	cps.liveGoroutineCount++

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
	cps.liveGoroutineCount--

	// Always decrement spawned count when a goroutine actually exits
	// This ensures the atomic counter stays in sync with reality
	cps.spawnedGoroutineCount.Add(-1)

	if cps.liveGoroutineCount == 0 {
		cps.controller.Reset()
		cps.throughput.Set(0)
		cps.spareUtil.Set(0)
	}

	cps.latestGoroutineCountChangeTime = time.Now()
	if cps.liveGoroutineCount == cps.targetGoroutineCount {
		cps.report("exited")
	}
	cps.notifyWaiter()
}

func (cps *CombinerPoolState) updateStats() bool {
	// No meaningful stats to update with zero goroutines
	if cps.liveGoroutineCount == 0 {
		return false
	}

	// Capture raw datapoints
	now := time.Now()
	curCompletedCount := cps.cumulativeCompletedCount.Load()

	// Calculate deltas, reset origins, and update EMAs
	timeOrigin := epoch.Add(time.Duration(cps.timeOrigin.Load()))
	elapsedTime := now.Sub(timeOrigin)
	cps.timeOrigin.Store(int64(now.Sub(epoch)))

	alpha := cps.tau.Alpha(elapsedTime)

	completedCount := curCompletedCount - cps.completedCountOrigin
	cps.completedCountOrigin = curCompletedCount
	cps.throughput.Update(alpha, float64(completedCount)/float64(elapsedTime))

	cps.spareWait.Update(now, timeOrigin)
	spareWait := cps.spareWait.CumulativeDuration - cps.spareWaitOrigin
	cps.spareWaitOrigin = cps.spareWait.CumulativeDuration
	if spareWait > elapsedTime {
		panic(fmt.Sprintf("%v spareWait %d greater than elapsed time %d!", time.Now(), spareWait, elapsedTime))
	}
	// Calculate spare utilization: fraction of time spare goroutine was working
	spareUtilization := 1.0 - float64(spareWait)/float64(elapsedTime)
	cps.spareUtil.Update(alpha, spareUtilization)

	if time.Since(cps.latestGoroutineCountChangeTime) < 3*time.Duration(cps.tau) {
		return false
	}

	// Update size controller with latest sample
	cps.controller.AddSample(time.Duration(cps.retentionPeriod.Load()), perfSample{
		Time:           timeOrigin,
		GoroutineCount: cps.liveGoroutineCount,
		Throughput:     cps.throughput.Get(),
		SpareUtil:      cps.spareUtil.Get(),
	})

	if cpDebug && now.Sub(epoch)/time.Second != timeOrigin.Sub(epoch)/time.Second {
		cps.report("updated")
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
