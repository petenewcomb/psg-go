// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import "github.com/petenewcomb/streampool/internal/delayq"

// ScheduledWork is a [Work] that can be handed to an [Accepted] queue
// with a future deadline via [Accepted.Schedule]. It becomes ordinary
// fresh work once its deadline arrives, at which point it is executed
// like any other work item.
//
// The [delayq.ScheduledState] surfaced via ScheduledState is opaque
// bookkeeping for the queue's internal deadline structure: embed
// [Scheduled] to satisfy it rather than implementing it by hand.
// Hand-rolling it would couple the work type to that structure's current
// representation (a binary heap position today), whereas a future
// timing-wheel implementation would store different per-item state —
// keeping the bookkeeping behind the embeddable helper confines such a
// change to [Scheduled] and delayq.
type ScheduledWork interface {
	Work
	delayq.Item
}

// ScheduledWorkItem is the embeddable base for a [ScheduledWork]
// implementation: it combines [WorkItem] (ID/Group/Free) with
// [Scheduled] (ScheduledState), leaving only Execute for the embedder to
// supply. Init it with the work's group (promoted from WorkItem). An
// embedder that needs custom teardown overrides Free; the rest is
// inherited. The embedder cannot hook position changes — that is by
// design (see [delayq.Item]).
type ScheduledWorkItem struct {
	WorkItem
	Scheduled
}

// Scheduled is the embeddable helper a [ScheduledWork] implementation
// embeds to satisfy the [delayq.Item] bookkeeping, mirroring how
// [WorkItem] supplies ID/Group/Free. It holds the queue's per-item
// [delayq.ScheduledState]; only the queue ever reads or mutates it.
type Scheduled struct {
	state delayq.ScheduledState
}

// ScheduledState returns a pointer to the queue's per-item bookkeeping.
// It is for the queue's use; callers should treat it as opaque.
func (s *Scheduled) ScheduledState() *delayq.ScheduledState { return &s.state }
