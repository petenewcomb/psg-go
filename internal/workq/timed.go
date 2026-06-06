// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import "github.com/petenewcomb/psg-go/internal/delayq"

// ScheduledWork is a [Work] that can be handed to an [Accepted] queue
// with a future deadline via [Accepted.Schedule]. It becomes ordinary
// fresh work once its deadline arrives, at which point it is executed
// like any other work item.
//
// The Position/SetPosition pair is opaque bookkeeping for the queue's
// internal deadline structure: embed [Scheduled] to satisfy it rather
// than implementing it by hand. Hand-rolling it would couple the work
// type to that structure's current representation (a binary heap
// position today), whereas a future timing-wheel implementation would
// store different per-item state — keeping the bookkeeping behind the
// embeddable helper confines such a change to [Scheduled] and delayq.
type ScheduledWork interface {
	Work
	delayq.Item
}

// Scheduled is the embeddable position-tracking helper a
// [ScheduledWork] implementation embeds to satisfy the opaque
// Position/SetPosition bookkeeping, mirroring how [WorkItem] supplies
// ID/Group/Free. Only the queue ever reads or mutates the position.
type Scheduled struct {
	pos int
}

// Position reports the work's slot in the queue's internal deadline
// structure. It is queue bookkeeping; callers should treat it as opaque.
func (s *Scheduled) Position() int { return s.pos }

// SetPosition records the work's slot in the queue's internal deadline
// structure. It is queue bookkeeping; callers should treat it as opaque.
func (s *Scheduled) SetPosition(p int) { s.pos = p }
