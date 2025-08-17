// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"

	"github.com/petenewcomb/psg-go/internal/trace"
)

type Listener struct {
	Notify NotifyFunc

	mu      sync.Mutex
	addedTo map[*Listeners]struct{}
}

//nolint:contextcheck // background context used only for tracing
func (m *Listener) AddTo(listeners *Listeners) {
	traceRegion := "rdvq.Listener.AddTo"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listener=%p, listeners=%p", m, listeners)

	m.mu.Lock()
	_, alreadyAdded := m.addedTo[listeners]
	if !alreadyAdded {
		if m.addedTo == nil {
			m.addedTo = make(map[*Listeners]struct{})
		}
		m.addedTo[listeners] = struct{}{}
	}
	m.mu.Unlock()

	if !alreadyAdded {
		listeners.add(func(renotifyFn RenotifyFunc) bool {
			return m.notify(listeners, renotifyFn)
		})
	}
}

//nolint:contextcheck // background context used only for tracing
func (m *Listener) notify(listeners *Listeners, renotifyFn RenotifyFunc) bool {
	traceRegion := "rdvq.Listener.notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listener=%p, listeners=%p", m, listeners)

	m.mu.Lock()
	delete(m.addedTo, listeners)
	notifyFn := m.Notify
	m.mu.Unlock()

	return notifyFn(renotifyFn)
}
