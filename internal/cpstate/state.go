// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cpstate

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/opts"
)

type FunnelPoolState struct {
	// High-frequency atomic counters updated lock-free from multiple goroutines
	cumulativeCompletedCount atomic.Int64 // monotonic

	// Mutex protects complex state analysis and scaling decisions
	mu sync.Mutex

	spawnNotifier rdvq.Notifier

	// Configuration
	maxConcurrency atomic.Int32 // -1 means unlimited
	idleTimeout    atomic.Int64 // time.Duration
	idleJitter     atomic.Int64 // time.Duration

	spawnedGoroutineCount atomic.Int32
	liveGoroutineCount    int
	latestIdleExit        time.Time // protected by mu
}

func (cps *FunnelPoolState) Init() {
	cps.spawnNotifier.Init()
}

// SetOptions atomically applies the given set of configuration options (later options override earlier ones).
// If validation fails, the method panics and no changes are applied.
func (cps *FunnelPoolState) SetOptions(options ...opts.FunnelPoolOption) {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	oldMaxConcurrency := int(cps.maxConcurrency.Load())

	// Create a copy of the current configuration
	newConfig := Config{
		MaxConcurrency: oldMaxConcurrency,
		IdleTimeout:    time.Duration(cps.idleTimeout.Load()),
		IdleJitter:     time.Duration(cps.idleJitter.Load()),
	}

	// Apply changes to the copy
	opts.ApplyToFunnelPool(&newConfig, options...)

	// Validate the new configuration (panics if invalid)
	newConfig.validate()

	newConfig.MaxConcurrency = min(newConfig.MaxConcurrency, int(math.MaxInt32))

	// Apply the validated configuration to the actual state
	cps.maxConcurrency.Store(int32(newConfig.MaxConcurrency)) //nolint:gosec // bounded by MaxInt32 above
	cps.idleTimeout.Store(int64(newConfig.IdleTimeout))
	cps.idleJitter.Store(int64(newConfig.IdleJitter))

	if newConfig.MaxConcurrency != oldMaxConcurrency &&
		(newConfig.MaxConcurrency == -1 || newConfig.MaxConcurrency > oldMaxConcurrency) {
		cps.spawnNotifier.Notify(nil)
	}
}

func (cps *FunnelPoolState) IdleTimeout() time.Duration {
	return time.Duration(cps.idleTimeout.Load())
}

func (cps *FunnelPoolState) IdleJitter() time.Duration {
	return time.Duration(cps.idleJitter.Load())
}

func (cps *FunnelPoolState) IncrementCompleted() {
	cps.cumulativeCompletedCount.Add(1)
}

//nolint:contextcheck // background context used only for tracing
func (cps *FunnelPoolState) ShouldSpawnFirstGoroutine() bool {
	traceRegion := "FunnelPoolState.ShouldSpawnFirstGoroutine"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	for {
		newCount := cps.spawnedGoroutineCount.Add(1)
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion, "FunnelPoolState=%p, newCount=%d", cps, newCount)
		}
		if newCount == 1 {
			trace.Logf(context.Background(), traceRegion, "returning true")
			return true
		}
		restoredCount := cps.spawnedGoroutineCount.Add(-1)
		if restoredCount < 0 {
			panic("restoredCount < 0")
		}
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion, "restoredCount=%d", restoredCount)
		}
		if restoredCount > 0 {
			trace.Logf(context.Background(), traceRegion, "returning false")
			return false
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (cps *FunnelPoolState) ShouldSpawnGoroutine() bool {
	traceRegion := "FunnelPoolState.ShouldSpawnGoroutine"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	for {
		newCount := cps.spawnedGoroutineCount.Add(1)
		maxConcurrency := cps.maxConcurrency.Load()
		trace.Logf(context.Background(), traceRegion,
			"FunnelPoolState=%p, newCount=%d, maxConcurrency=%d", cps, newCount, maxConcurrency)
		if maxConcurrency == -1 || newCount <= maxConcurrency {
			trace.Logf(context.Background(), traceRegion, "returning true")
			return true
		}
		restoredCount := cps.spawnedGoroutineCount.Add(-1)
		trace.Logf(context.Background(), traceRegion, "restoredCount=%d", restoredCount)
		if restoredCount < 0 {
			panic("restoredCount < 0")
		}
		if restoredCount >= maxConcurrency {
			trace.Logf(context.Background(), traceRegion, "returning false")
			return false
		}
	}
}

func (cps *FunnelPoolState) LiveGoroutineCount() int {
	cps.mu.Lock()
	defer cps.mu.Unlock()
	return cps.liveGoroutineCount
}

func (cps *FunnelPoolState) GoroutineStarted() {
	traceRegion := "FunnelPoolState.GoroutineStarted"
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.liveGoroutineCount++

	trace.Logf(context.Background(), traceRegion,
		"FunnelPoolState=%p, spawnedCount=%d, liveCount=%d",
		cps, cps.spawnedGoroutineCount.Load(), cps.liveGoroutineCount)
}

func (cps *FunnelPoolState) GoroutineRestarted() {
	traceRegion := "FunnelPoolState.GoroutineStarted"
	cps.mu.Lock()
	defer cps.mu.Unlock()
	cps.liveGoroutineCount++
	spawnedCount := cps.spawnedGoroutineCount.Add(1)
	trace.Logf(context.Background(), traceRegion,
		"FunnelPoolState=%p, spawnedCount=%d, liveCount=%d", cps, spawnedCount, cps.liveGoroutineCount)
}

func (cps *FunnelPoolState) GoroutineExiting() bool {
	traceRegion := "FunnelPoolState.GoroutineExiting"
	cps.mu.Lock()
	defer cps.mu.Unlock()
	if cps.liveGoroutineCount <= 0 {
		panic("underflow")
	}
	cps.liveGoroutineCount--

	// Always decrement spawned count when a goroutine actually exits
	// This ensures the atomic counter stays in sync with reality
	spawnedCount := cps.spawnedGoroutineCount.Add(-1)
	trace.Logf(context.Background(), traceRegion,
		"FunnelPoolState=%p, spawnedCount=%d, liveCount=%d", cps, spawnedCount, cps.liveGoroutineCount)
	if spawnedCount < 0 {
		panic("spawnedCount < 0")
	}

	return cps.liveGoroutineCount == 0
}

func (cps *FunnelPoolState) SpawnNotifier() *rdvq.Notifier {
	return &cps.spawnNotifier
}

// TryIdleExit attempts to record an idle exit. Returns true if this worker
// is allowed to exit (enough time has passed since latest exit), false if
// another worker exited too recently and this worker should retry later.
func (cps *FunnelPoolState) TryIdleExit() bool {
	cps.mu.Lock()
	defer cps.mu.Unlock()

	now := time.Now()
	idleTimeout := time.Duration(cps.idleTimeout.Load())
	if now.Sub(cps.latestIdleExit) >= idleTimeout {
		cps.latestIdleExit = now
		return true
	}
	return false
}
