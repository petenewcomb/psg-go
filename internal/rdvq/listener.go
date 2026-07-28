// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"
)

// Listener is a reusable wake relay owned by a single entity (typically a work
// queue, whose relay wakes one of its parked workers). It can be planted in
// multiple Listeners sets — each planting is one-shot, popped by the set's next
// walk — and it can be fired directly via [Listener.Notify] by a notifier that
// holds it (e.g. as a demand's attendant).
//
// The mutex protects the planted-set bookkeeping against concurrent walks
// firing from different goroutines.
type Listener struct {
	fn func()

	mu      sync.Mutex
	addedTo map[*Listeners]struct{}
}

// NewListener returns a Listener that runs fn when fired.
func NewListener(fn func()) *Listener {
	return &Listener{fn: fn}
}

// Notify fires the listener's relay directly.
func (m *Listener) Notify() { m.fn() }

// listenerNotifyWrapper wraps the parameters needed to call Listener.notify,
// avoiding the allocation of a closure in Listener.AddTo. The wrapper is pooled
// and reused across calls.
type listenerNotifyWrapper struct {
	listener  *Listener
	listeners *Listeners

	notifyFn func() // avoid reallocating closure
}

func (w *listenerNotifyWrapper) Init() {
	w.notifyFn = w.notify
}

func (w *listenerNotifyWrapper) Reset() {
	*w = listenerNotifyWrapper{
		notifyFn: w.notifyFn,
	}
}

func (w *listenerNotifyWrapper) notify() {
	w.listener.notify(w.listeners)
	listenerNotifyWrapperPool.Release(w)
}

var listenerNotifyWrapperPool = omnipool.For[listenerNotifyWrapper]()

// AddTo plants this listener in the given Listeners set. If already planted
// there, this is a no-op. The planting is one-shot: the set's next walk pops
// and fires it, after which the owner re-plants on its next retry.
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
func (m *Listener) notify(listeners *Listeners) {
	traceRegion := "rdvq.Listener.notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listener=%p, listeners=%p", m, listeners)

	m.mu.Lock()
	delete(m.addedTo, listeners)
	fn := m.fn
	m.mu.Unlock()

	fn()
}
