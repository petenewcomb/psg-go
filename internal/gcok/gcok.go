// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package gcok

import (
	"runtime/metrics"
	"sync"
	"time"

	"github.com/petenewcomb/psg-go/internal/dynval"
)

type Monitor struct {
	busy dynval.Value[bool]
	wg   sync.WaitGroup

	// Below all protected by mu
	mu             sync.Mutex
	updateInterval time.Duration
	busyThreshold  float64
	done           chan struct{}
}

func (m *Monitor) SetBusyThreshold(threshold float64) {
	if threshold <= 0 || threshold > 1 {
		panic("invalid busy threshold: must be in the range (0, 1]")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.busyThreshold = threshold
}

// Zero disables updates and forces the busy signal to false.
func (m *Monitor) SetUpdateInterval(interval time.Duration) {
	if interval < 0 {
		panic("invalid update interval: must be zero or greater")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wasRunning := m.updateInterval > 0
	m.updateInterval = interval

	if interval == 0 {
		m.busy.Store(false)
		if wasRunning {
			close(m.done)
		}
	} else if !wasRunning {
		// Start monitoring
		m.done = make(chan struct{})
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.run(interval)
		}()
	}
}

func (m *Monitor) BusySignal() (bool, <-chan struct{}) {
	return m.busy.Load()
}

func (m *Monitor) Cancel() {
	m.SetUpdateInterval(0)
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
