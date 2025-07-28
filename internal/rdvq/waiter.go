// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

type Waiter struct {
	inbox      Inbox[RenotifyFunc]
	renotifyFn RenotifyFunc
}

func (w *Waiter) waiter() *Waiter {
	return w
}

// Ch returns the waiter's inbox channel for use in select statements.
// Returns nil if the waiter itself is nil.
// Panics if called when no channel has been allocated, which should only
// happen if Ch() is called outside of a selectFn callback.
func (w *Waiter) Ch() <-chan RenotifyFunc {
	if w == nil {
		return nil
	}
	return w.inbox.Ch()
}

// Notified records that a renotify function was received and marks the inbox as emptied.
// This should be called by callbacks when they receive a RenotifyFunc from a wait channel.
// Panics if renotifyFn is nil.
func (w *Waiter) Notified(renotifyFn RenotifyFunc) {
	w.inbox.Emptied()
	if renotifyFn == nil {
		panic("renotifyFn cannot be nil")
	}
	w.renotifyFn = renotifyFn
}

// WasNotified returns true if a RenotifyFunc was received via Notified.
// Returns false if Renotify() was called since the last Notified() call.
// Returns false if the waiter itself is nil.
func (w *Waiter) WasNotified() bool {
	return w != nil && w.renotifyFn != nil
}

// RenotifyFn returns the RenotifyFunc from the most recent notification.
// Returns nil if the waiter itself is nil.
func (w *Waiter) RenotifyFn() RenotifyFunc {
	if w == nil {
		return nil
	}
	return w.renotifyFn
}
