// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/nbcq"
)

// noop is the default fallback for wake operations with no explicit fallback
// action: a never-nil terminal so callers need no nil check.
func noop() {}

// Listeners manages a queue of one-shot wake relays. It provides a planting
// mechanism for queues to register interest in a domain's capacity events.
type Listeners struct {
	q nbcq.Queue[func()]
}

// Init initializes the Listeners for use. Must be called before any other operations.
func (c *Listeners) Init() {
	traceRegion := "rdvq.Listeners.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p, nbcq.Queue=%p", c, &c.q)

	c.q.Init()
}

//nolint:contextcheck // background context used only for tracing
func (c *Listeners) add(notifyFn func()) {
	traceRegion := "rdvq.Listeners.add"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	if notifyFn == nil {
		panic("notifyFn is nil")
	}
	c.q.PushBack(notifyFn)
}

// NotifyAll pops and fires every planted relay. Delivery is unconditional —
// no relay's outcome narrows the walk (docs/notification-conservation.md);
// each popped planting is one-shot and its owner re-plants on its next retry.
//
//nolint:contextcheck // background context used only for tracing
func (c *Listeners) NotifyAll() {
	traceRegion := "rdvq.Listeners.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.TryPopFront()
		if !ok {
			break
		}
		notifyFn()
	}
}
