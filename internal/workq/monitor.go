// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"sync"

	"github.com/petenewcomb/psg-go/internal/trace"
)

type Monitor struct {
	Notify NotifyFunc

	mu            sync.Mutex
	subscriptions map[*Coordinator]struct{}
}

//nolint:contextcheck // background context used only for tracing
func (m *Monitor) Subscribe(coordinator *Coordinator) {
	traceRegion := "workq.Monitor.Subscribe"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Monitor=%p, coordinator=%p", m, coordinator)

	m.mu.Lock()
	_, subscribed := m.subscriptions[coordinator]
	if !subscribed {
		if m.subscriptions == nil {
			m.subscriptions = make(map[*Coordinator]struct{})
		}
		m.subscriptions[coordinator] = struct{}{}
	}
	m.mu.Unlock()

	if !subscribed {
		coordinator.add(func(renotifyFn RenotifyFunc) {
			m.notify(coordinator, renotifyFn)
		})
	}
}

//nolint:contextcheck // background context used only for tracing
func (m *Monitor) notify(coordinator *Coordinator, renotifyFn RenotifyFunc) {
	traceRegion := "workq.Monitor.notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Monitor=%p, coordinator=%p", m, coordinator)

	m.mu.Lock()
	delete(m.subscriptions, coordinator)
	notifyFn := m.Notify
	m.mu.Unlock()

	notifyFn(renotifyFn)
}
