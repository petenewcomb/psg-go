// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// ProcessValueFunc is called to process a value retrieved from a queue.
type ProcessValueFunc[T any] = func(value T)

// RenotifyFunc is called when a notification cannot be delivered and needs to be retried.
// It's typically used to re-queue a notification for later delivery.
type RenotifyFunc func()

// NotifyFunc is used to deliver a notification to a subscriber that is
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
