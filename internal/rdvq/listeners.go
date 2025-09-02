// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

// A NotifyFunc is used to deliver a notification to a subscriber that is
// waiting for it. If unable to deliver the notification to such a subscriber,
// it must arrange for the given RenotifyFunc to be called. This may happen
// synchronously or asynchronously, though synchronous is preferred for
// efficiency. For maximal efficiency, especially with respect to stack depth, a
// NotifyFunc that synchronously determines that it is unable to deliver the
// notification to a suitable subscriber should return false instead of calling
// the RenotifyFunc. This signals the caller that it should find another
// subscriber or call the RenotifyFunc itself. In all other cases, the
// NotifyFunc must return true and synchronously or asynchronously find another
// subscriber or else call the RenotifyFunc.
type NotifyFunc func(RenotifyFunc) bool

type RenotifyFunc = func()

func NoopRenotify() {}

type Listeners struct {
	q nbcq.Queue[NotifyFunc]
}

func (c *Listeners) Init() {
	traceRegion := "rdvq.Listeners.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p, nbcq.Queue=%p", c, &c.q)

	c.q.Init()
}

//nolint:contextcheck // background context used only for tracing
func (c *Listeners) add(notifyFn NotifyFunc) {
	traceRegion := "rdvq.Listeners.add"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	if notifyFn == nil {
		panic("notifyFn is nil")
	}
	c.q.PushBack(notifyFn)
}

//nolint:contextcheck // background context used only for tracing
func (c *Listeners) Notify(renotifyFn RenotifyFunc) bool {
	traceRegion := "rdvq.Listeners.notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.PopFront()
		if !ok {
			return false
		}

		if notifyFn(renotifyFn) {
			return true
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *Listeners) NotifyAll() {
	traceRegion := "rdvq.Listeners.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.PopFront()
		if !ok {
			break
		}
		notifyFn(NoopRenotify)
	}
}

func (c *Listeners) Reset() {
	if _, ok := c.q.PopFront(); ok {
		panic("resetting non-empty Listeners")
	}
}
