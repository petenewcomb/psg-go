// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"
)

// Listener provides a reusable notification subscription that can be added to
// multiple Listeners instances. It tracks which Listeners it has been added to
// and ensures it's only added once per Listeners instance.
//
// The mutex protects against concurrent calls to the internal notify method,
// which can occur when multiple Listeners instances fire notifications from
// different goroutines. The Listener itself is typically owned by a single entity.
type Listener struct {
	// Notify is the function called when this listener is signaled.
	Notify NotifyFunc

	mu      sync.Mutex
	addedTo map[*Listeners]struct{}
}

// listenerNotifyWrapper wraps the parameters needed to call Listener.notify,
// avoiding the allocation of a closure in Listener.AddTo. The wrapper is pooled
// and reused across calls.
type listenerNotifyWrapper struct {
	listener  *Listener
	listeners *Listeners

	notifyFn NotifyFunc // avoid reallocating closure
}

func (w *listenerNotifyWrapper) Init() {
	w.notifyFn = w.notify
}

func (w *listenerNotifyWrapper) Reset() {
	*w = listenerNotifyWrapper{
		notifyFn: w.notifyFn,
	}
}

func (w *listenerNotifyWrapper) notify(renotifyFn RenotifyFunc) bool {
	result := w.listener.notify(w.listeners, renotifyFn)
	listenerNotifyWrapperPool.Put(w)
	return result
}

var listenerNotifyWrapperPool = omnipool.For[listenerNotifyWrapper]()

// AddTo subscribes this listener to the given Listeners instance.
// If already subscribed, this is a no-op. The listener will be called
// when the Listeners instance receives a notification.
//
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
		w := listenerNotifyWrapperPool.Get()
		w.listener = m
		w.listeners = listeners
		listeners.add(w.notifyFn)
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
