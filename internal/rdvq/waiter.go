// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// A Waiter has the following lifecycle states:
//
// 1. The zero value of waiter is a waiter that will never be signaled.
// [Waiter.Done] will return a nil channel, and [Waiter.Close] will panic.
//
// 2. [WaiterQueue.Add] returns a waiter with an empty notification channel of
// buffer length one that has been added to the queue.
//
// 3a. [WaiterQueue.Notify] has retrieved the waiter from the queue and sent a
// message, filling the buffer.
//
// 4aa. The message is received by a select on [Waiter.Done], emptying the buffer.
//
// 5aa. [Waiter.Close] sends a message on its own notification channel,
// re-filling the buffer. This is an end state, since the waiter has been closed
// and is no longer in the queue.
//
// 4ab. [Waiter.Close] attempts to send a message on its own notification
// channel but cannot because the buffer is full. It therefore calls
// [WaiterQueue.Notify] to pass the notification on to another waiter in the
// queue. This is an end state, since the waiter has been closed and is no
// longer in the queue.
//
// 3b. [Waiter.Close] has sent a message on its own notification channel,
// filling the buffer.
//
// 4b. [WaiterQueue.Notify] has retrieved the waiter from the queue but was
// unable to send a message because the buffer was full. It therefore moves on
// to the next waiter in the queue. This is an end state, since the waiter has
// been closed and is no longer in the queue.
//
// Waiter variables may be safely copied and are designed to be passed by value.
type Waiter struct {
	w         *Waiters
	confirmFn func() bool
}

// WaitSelectFunc handles select operations on wait channels, returning the the
// received RenotifyFunc or nil if the select exited without receiving one.
type WaitSelectFunc func(waitCh <-chan RenotifyFunc) RenotifyFunc

type NotifyFunc func(RenotifyFunc)

//nolint:contextcheck // background context used only for tracing
func (w Waiter) WaitFuncWithOrphanHandler(orphanFn NotifyFunc, selectFn WaitSelectFunc) RenotifyFunc {
	traceRegion := "rdvq.Waiter.WaitFuncWithOrphanHandler"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w.w)

	if w.w == nil {
		return selectFn(nil)
	}
	var renotifyFn RenotifyFunc
	w.w.q.PopFrontFunc(wp,
		orphanFn,
		func(ch <-chan RenotifyFunc) SelectResult {
			if w.confirmFn == nil || w.confirmFn() {
				renotifyFn = selectFn(ch)
				if renotifyFn != nil {
					return SelectInboxEmptied
				}
			}
			return SelectAborted
		},
	)
	return renotifyFn
}

func (w Waiter) WaitFunc(selectFn WaitSelectFunc) RenotifyFunc {
	return w.WaitFuncWithOrphanHandler(w.w.Notify, selectFn)
}

func (w Waiter) WaitWithOrphanHandler(ctx context.Context, orphanFn NotifyFunc) (RenotifyFunc, error) {
	traceRegion := "rdvq.Waiter.WaitWithOrphanHandler"

	var err error
	renotifyFn := w.WaitFuncWithOrphanHandler(orphanFn, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
		select {
		case renotifyFn := <-waitCh:
			trace.Logf(ctx, traceRegion, "received renotifyFn from waitCh=%p", waitCh)
			return renotifyFn
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
		}
		return nil
	})
	return renotifyFn, err
}

func (w Waiter) Wait(ctx context.Context) (RenotifyFunc, error) {
	return w.WaitWithOrphanHandler(ctx, w.w.Notify)
}
