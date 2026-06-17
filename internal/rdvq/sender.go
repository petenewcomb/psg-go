// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Sender is a vestigial handle retained on the push API while the mechanical
// removal pass is pending. Outbox ownership now lives on the destination
// [Queue], which keeps a pool of outboxes that self-sizes to the concurrency it
// actually experiences (see the "rdvq Sender redesign" notes). A Sender holds
// no state; callers may share one freely or pass nil.
//
// It once cached per-goroutine outboxes keyed by destination, which accumulated
// stale entries for long-lived senders feeding many short-lived destinations —
// the staleness the destination-owned pool eliminates.
type Sender struct{}

// Release is a no-op retained for API compatibility. A Sender holds no state to
// free.
func (s *Sender) Release() {}
