// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

// Optional implements the base layer of rdvq's two-tier architecture,
// providing direct sender-receiver rendezvous without overflow handling.
// It serves as the foundation for Required[T] and can be used standalone
// for simple rendezvous scenarios.
//
// Optional uses lock-free operations and eliminates channel contention
// by giving each receiver a dedicated inbox channel. Senders attempt
// direct handoff to waiting receivers, failing immediately if none are available.
type Optional[T any] struct {
	emptyInboxes nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *Optional[T]) Init(p *Pool[T]) {
	q.emptyInboxes.Init(&p.nodePool)
}

func (q *Optional[T]) TryPushBack(p *Pool[T], value T) bool {
	for {
		inboxCh, ok := q.emptyInboxes.PopFront(&p.nodePool)
		if !ok {
			// No waiting emptyInboxes
			return false
		}

		// Try to send to this receiver
		select {
		case inboxCh <- value:
			// Successfully delivered
			return true
		default:
			// inboxCh is full which means that the receiver abandoned it.
			// Drain and put back in the pool, then loop and try getting
			// another.
			<-inboxCh
			p.putChan(inboxCh)
		}
	}
}

// OptionalPopSelectFunc handles the select operation for PopFrontFunc.
// It should select on the inbox channel, returning the appropriate result.
type OptionalPopSelectFunc[T any] func(inboxCh <-chan T) SelectResult

func (q *Optional[T]) PopFrontFunc(p *Pool[T], processOrphanFn ProcessValueFunc[T], selectFn OptionalPopSelectFunc[T]) {

	// Register ourselves as a waiting receiver.
	inboxCh := p.getChan()
	q.emptyInboxes.PushBack(&p.nodePool, inboxCh)

	// Call the custom selecting function
	result := selectFn(inboxCh)
	if result == SelectInboxEmptied {
		// PushBack must have pulled it out of the queue and the select just
		// emptied it, so it's safe to return to the pool.
		p.putChan(inboxCh)
		return // Done!
	}

	// Clean up the dedicated channel, which may still be in the queue or
	// contain an orphaned value.
	select {
	case inboxCh <- *new(T):
		// Successfully marked channel as abandoned, but it's still in
		// the queue so we can't put it back in the pool.
	default:
		// Channel is full, drain the value and process it.
		processOrphanFn(<-inboxCh)
		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(inboxCh)
	}
}

func (q *Optional[T]) TryPopFront(p *Pool[T], processFn ProcessValueFunc[T]) {
	q.PopFrontFunc(p, processFn, func(ch <-chan T) SelectResult {
		select {
		case value := <-ch:
			processFn(value)
			return SelectInboxEmptied
		default:
		}
		return SelectAborted
	})
}

func (q *Optional[T]) PopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(p, processFn, func(ch <-chan T) SelectResult {
		select {
		case value := <-ch:
			processFn(value)
			return SelectInboxEmptied
		case <-ctx.Done():
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}
