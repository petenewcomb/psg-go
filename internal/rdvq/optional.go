// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type Optional[T any] struct {
	receivers nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *Optional[T]) Init(p *Pool[T]) {
	q.receivers.Init(&p.receiverPool)
}

func (q *Optional[T]) TryPushBack(p *Pool[T], value T) bool {
	for {
		receiverCh, ok := q.receivers.PopFront(&p.receiverPool)
		if !ok {
			// No waiting receivers
			return false
		}

		// Try to send to this receiver
		select {
		case receiverCh <- value:
			// Successfully delivered
			return true
		default:
			// receiverCh is full which means that the receiver abandoned it.
			// Drain and put back in the pool, then loop and try getting
			// another.
			<-receiverCh
			p.putChan(receiverCh)
		}
	}
}

type ProcessValueFunc[T any] func(value T)

// Must return true when a value was read from the given channel, false
// otherwise.
type OptionalPopSelectFunc[T any] func(ch <-chan T) bool

func (q *Optional[T]) PopFrontFunc(p *Pool[T], processOrphanFn ProcessValueFunc[T], selectFn OptionalPopSelectFunc[T]) {

	// Register ourselves as a waiting receiver.
	receiverCh := p.getChan()
	q.receivers.PushBack(&p.receiverPool, receiverCh)

	// Call the custom selecting function
	if selectFn(receiverCh) {
		// PushBack must have pulled it out of the queue and the select just
		// emptied it, so it's safe to return to the pool.
		p.putChan(receiverCh)
		return // Done!
	}

	// Clean up the dedicated channel, which may still be in the queue or
	// contain an orphaned value.
	select {
	case receiverCh <- *new(T):
		// Successfully marked channel as abandoned, but it's still in
		// the queue so we can't put it back in the pool.
	default:
		// Channel is full, drain the value and process it.
		processOrphanFn(<-receiverCh)
		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(receiverCh)
	}
}

func (q *Optional[T]) TryPopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) {
	q.PopFrontFunc(p, processFn, func(ch <-chan T) bool {
		select {
		case value := <-ch:
			processFn(value)
			return true
		default:
		}
		return false
	})
}

func (q *Optional[T]) PopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(p, processFn, func(ch <-chan T) bool {
		select {
		case value := <-ch:
			processFn(value)
			return true
		case <-ctx.Done():
			err = ctx.Err()
		}
		return false
	})
	return err
}
