// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

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
	q        *Waiters
	verifyFn func() bool
}

func (w Waiter) Wait(selectFn WaitSelectFunc) SelectResult {
	if w.q == nil {
		return selectFn(nil)
	}
	result := SelectAborted
	w.q.inner.PopFrontFunc(wp,
		func(struct{}) {
			// There was an orphaned value in the channel, meaning that this
			// waiter was notified but didn't receive it. Call Notify to pass
			// the notification to another.
			w.q.Notify()
		},
		func(ch <-chan struct{}) SelectResult {
			if w.verifyFn == nil || w.verifyFn() {
				innerResult := selectFn(ch)
				if innerResult == SelectWaitSignaled {
					result = SelectWaitSignaled
					return SelectInboxEmptied
				}
			}
			return SelectAborted
		},
	)
	return result
}

// WaitSelectFunc handles select operations on wait channels, returning
// the appropriate SelectResult to indicate what happened.
type WaitSelectFunc func(waitCh <-chan struct{}) SelectResult
