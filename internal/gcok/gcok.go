// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package gcok

import (
	"runtime/metrics"
	"sync"
	"time"

	"github.com/petenewcomb/psg-go/internal/dynval"
)

// GCConfigChanges holds configuration changes for a GC Monitor.
// Fields use pointers to distinguish between "not set" (nil) and "set to zero value" (non-nil).
type GCConfigChanges struct {
	BusyThreshold  *float64
	UpdateInterval *time.Duration
}

type Monitor struct {
	busy dynval.Value[bool]
	wg   sync.WaitGroup

	// Below all protected by mu
	mu             sync.Mutex
	updateInterval time.Duration
	busyThreshold  float64
	done           chan struct{}
}

func (m *Monitor) BusySignal() (bool, <-chan struct{}) {
	return m.busy.Load()
}

func (m *Monitor) Cancel() {
	m.Update(GCConfigChanges{UpdateInterval: new(time.Duration)}) // *new(time.Duration) is a pointer to zero value
}

func (m *Monitor) Wait() {
	m.wg.Wait()
}

func readMetrics() (gcTime, totalTime float64) {
	samples := []metrics.Sample{
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/cpu/classes/total:cpu-seconds"},
	}
	metrics.Read(samples)
	return samples[0].Value.Float64(), samples[1].Value.Float64()
}

func (m *Monitor) run(interval time.Duration) {
	// Initialize prev values
	prevGCTime, prevTotalTime := readMetrics()
	busy := false

	for {
		// Wait first
		select {
		case <-time.After(interval):
		case <-m.done:
			return
		}

		// Then update
		gcTime, totalTime := readMetrics()

		m.mu.Lock()
		threshold := m.busyThreshold
		interval = m.updateInterval
		m.mu.Unlock()

		if interval == 0 {
			return
		}

		newBusy := false
		if threshold > 0 {
			deltaGC := gcTime - prevGCTime
			deltaTotal := totalTime - prevTotalTime

			ratio := 0.0
			if deltaTotal > 0 {
				ratio = deltaGC / deltaTotal
			}

			newBusy = ratio > threshold
		}

		if newBusy != busy {
			m.busy.Store(newBusy)
			busy = newBusy
		}

		prevGCTime = gcTime
		prevTotalTime = totalTime
	}
}

// Update atomically applies configuration changes to the monitor.
func (m *Monitor) Update(changes GCConfigChanges) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate all changes first
	if changes.BusyThreshold != nil {
		if *changes.BusyThreshold <= 0 || *changes.BusyThreshold > 1 {
			panic("invalid busy threshold: must be in the range (0, 1]")
		}
	}
	if changes.UpdateInterval != nil {
		if *changes.UpdateInterval < 0 {
			panic("invalid update interval: must be zero or greater")
		}
	}

	// Apply threshold change
	if changes.BusyThreshold != nil {
		m.busyThreshold = *changes.BusyThreshold
	}

	// Apply interval change (this is more complex due to goroutine management)
	if changes.UpdateInterval != nil {
		wasRunning := m.updateInterval > 0
		m.updateInterval = *changes.UpdateInterval

		if *changes.UpdateInterval == 0 {
			m.busy.Store(false)
			if wasRunning {
				close(m.done)
			}
		} else if !wasRunning {
			m.done = make(chan struct{})
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				m.run(*changes.UpdateInterval)
			}()
		}
	}
}
