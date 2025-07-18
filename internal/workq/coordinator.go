// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type NotifyFunc = rdvq.NotifyFunc

type Coordinator struct {
	q nbcq.Queue[NotifyFunc]
}

func (c *Coordinator) Init() {
	traceRegion := "workq.Coordinator.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Coordinator=%p, nbcq.Queue=%p", c, &c.q)

	c.q.Init(notifyPool)
}

//nolint:contextcheck // background context used only for tracing
func (c *Coordinator) add(notifyFn NotifyFunc) {
	traceRegion := "workq.Coordinator.add"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Coordinator=%p", c)

	if notifyFn != nil {
		c.q.PushBack(notifyPool, notifyFn)
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *Coordinator) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "workq.Coordinator.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Coordinator=%p", c)

	for {
		notifyFn, ok := c.q.PopFront(notifyPool)
		if !ok {
			if renotifyFn != nil {
				renotifyFn()
			}
			return
		}

		// This shim function is used to avoid deep recursion if notifyFn
		// synchronously calls the RenotifyFunc passed to it.
		var state atomic.Int32
		renotifyShimFn := func() {
			switch state.Add(1) {
			case 0:
				// This is the first call to this function and the outer
				// function must have already decremented and exited the loop,
				// so we must recursively (though asynchronously) call Notify
				// again to continue propagation.
				c.Notify(renotifyFn)
			default:
				// Either the state has not yet been decremented by the outer
				// function and we can therefore let the outer loop continue
				// propagation or this is the second or later call and we should
				// ignore anyway.
			}
		}

		notifyFn(renotifyShimFn)

		switch state.Add(-1) {
		case -1:
			// renotifyShimFn had not yet decremented the state, so we will
			// leave further notification propagation to it to ensure if needed
			// and otherwise consider the notification productively delivered.
			return
		default:
			// renotifyShimFn had been called at least once before we
			// decremented, meaning that notification propagation is necessary
			// and up to us to ensure, so continue the loop.
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *Coordinator) NotifyAll() {
	traceRegion := "workq.Coordinator.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Coordinator=%p", c)

	for {
		notifyFn, ok := c.q.PopFront(notifyPool)
		if !ok {
			break
		}
		notifyFn(nil)
	}
}

var notifyPool = &nbcq.Pool[NotifyFunc]{}
