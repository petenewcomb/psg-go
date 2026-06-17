// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Waiter is a vestigial handle retained on the [Waiters] wait API while the
// mechanical removal pass is pending. Wait-inbox storage now lives on the
// [Waiters], which pools inboxes (see [inboxOnlyQueue.borrowInbox]); a Waiter
// holds no state, so callers may share one freely or pass nil.
//
// It once cached a per-goroutine wait-inbox per [Waiters]; that now comes from
// the Waiters' own pool.
type Waiter struct{}

// Release is a no-op retained for API compatibility. A Waiter holds no state to
// free.
func (s *Waiter) Release() {}
