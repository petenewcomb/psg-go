// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Receiver is a vestigial handle retained on the receive API while the
// mechanical removal pass is pending. Inbox storage now lives on the destination
// [Queue], which pools inboxes (see [inboxOnlyQueue.borrowInbox]); a Receiver
// holds no state, so callers may share one freely or pass nil.
//
// It once cached a per-goroutine inbox per destination plus a [Waiter] for the
// outbox-availability subscription; both now come from the destination's pools.
type Receiver struct{}

// Release is a no-op retained for API compatibility. A Receiver holds no state
// to free.
func (r *Receiver) Release() {}
