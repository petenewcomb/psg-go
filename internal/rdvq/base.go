// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import (
	"sync"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type baseQ[T any] struct {
	receivers nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *baseQ[T]) init(p *basePool[T]) {
	q.receivers.Init(&p.receiverPool)
}

type baseFallbackPushFunc[T any] func(value T)

func (q *baseQ[T]) pushBackFunc(p *basePool[T], value T, fallbackPushFn baseFallbackPushFunc[T]) {
	for {
		receiverCh, ok := q.receivers.PopFront(&p.receiverPool)
		if !ok {
			// No waiting receivers
			break
		}

		// Try to send to this receiver
		select {
		case receiverCh <- value:
			// Successfully delivered
			return
		default:
			// receiverCh is full which means that the receiver abandoned it.
			// Drain and put back in the pool, then loop and try getting
			// another.
			<-receiverCh
			p.putChan(receiverCh)
		}
	}

	// No waiting receivers, use custom push function
	fallbackPushFn(value)
}

type OrphanedValueFunc[T any] func(value T)

func (q *baseQ[T]) popFrontFunc(p *basePool[T], selectFn StrictPopSelectFunc[T], adoptValueFn OrphanedValueFunc[T]) {

	// Register ourselves as a waiting receiver.
	receiverCh := p.getChan()
	q.receivers.PushBack(&p.receiverPool, receiverCh)

	// Call the custom selecting function
	if selectFn(receiverCh) {
		// PushBack must have pulled it out of the queue and we just
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
		// Channel is full, drain the orphaned value and get it adopted.
		adoptValueFn(<-receiverCh)

		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(receiverCh)
	}
}

type basePool[T any] struct {
	receiverPool nbcq.Pool[chan T]
	chanPool     sync.Pool
}

func (p *basePool[T]) getChan() chan T {
	ch, _ := p.chanPool.Get().(chan T)
	if ch == nil {
		ch = make(chan T, 1)
	}
	return ch
}

func (p *basePool[T]) putChan(ch chan T) {
	p.chanPool.Put(ch)
}
