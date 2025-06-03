// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package ucbq provides a high-performance unbounded blocking concurrent queue.
// It provides functionality similar to a buffered channel, but unbounded and
// without mutex contention.
package ubcq

import (
	"context"
	"sync"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type Queue[T any] struct {
	nextValues       nbcq.Queue[T]
	pendingValues    nbcq.Queue[T]
	waitingReceivers nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *Queue[T]) Init(p *Pool[T]) {
	q.nextValues.Init(&p.valuePool)
	q.pendingValues.Init(&p.valuePool)
	q.waitingReceivers.Init(&p.receiverPool)
}

// PushBack sends a value to a waiting receiver, or queues the value for a
// receiver to pick up later. This is analogous to sending on a channel with
// available buffer space.
func (q *Queue[T]) PushBack(p *Pool[T], value T) {
	// First, try to find a waiting receiver for direct handoff
	for {
		receiverCh, ok := q.waitingReceivers.PopFront(&p.receiverPool)
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
			// receiverCh is full, which means that the receiver abandoned it.
			// Drain and put back in the pool, then loop and try getting
			// another.
			<-receiverCh
			p.putChan(receiverCh)
		}
	}

	// No waiting receivers, so queue the value for one to pick up later.
	q.pendingValues.PushBack(&p.valuePool, value)
}

// BlockFunc is called when PopFrontFunc needs to block waiting for a value.
// It receives the channel on which a value will arrive and should implement
// custom blocking logic (e.g., selecting on multiple channels).
// It returns the received value, whether a value was received, and any error.
type BlockFunc[T any] func(ch <-chan T) (value T, ok bool, err error)

// PopFrontFunc receives a value from the queue using custom blocking logic.
// If no value is immediately available, it calls blockFn with a channel that
// will receive the next value. This allows the caller to implement complex
// blocking patterns (e.g., waiting on multiple conditions) without creating
// additional goroutines.
// Returns the value, whether a value was received, and any error.
func (q *Queue[T]) PopFrontFunc(p *Pool[T], blockFn BlockFunc[T]) (T, bool, error) {
	value, ok := q.TryPopFront(p)
	if ok {
		return value, true, nil
	}

	// No waiting senders, prepare to register ourselves as a waiting receiver
	receiverCh := p.getChan()

	// Essential cleanup to mark channel as dead and return to pool
	defer func() {
		if receiverCh != nil {
			select {
			case receiverCh <- *new(T):
				// Successfully marked channel as abandoned, but it's still in
				// the queue so we can't put it back in the pool.
			default:
				// Channel is full, drain the pending work and requeue to be
				// picked up by the next pop operation.
				q.nextValues.PushBack(&p.valuePool, <-receiverCh)
				// PushBack must have pulled it out of the queue and we just
				// emptied it, so it's safe to return to the pool.
				p.putChan(receiverCh)
			}
		}
	}()

	// Register ourselves as a waiting receiver.
	q.waitingReceivers.PushBack(&p.receiverPool, receiverCh)

	// Check again in case a sender added a value while we were putting our
	// channel in the waiting queue.
	value, ok = q.TryPopFront(p)
	if ok {
		return value, true, nil
	}

	// Call the custom blocking function
	value, ok, err := blockFn(receiverCh)
	if ok {
		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(receiverCh)
		// Make sure that the deferred cleanup function knows there's nothing
		// left to do.
		receiverCh = nil
	}

	// Return the value whether ok is true or false
	return value, ok, err
}

// PopFront receives a value from the sent value queue or waits for one to
// arrive. It blocks until a value is available or the context is cancelled.
// This is analogous to receiving from a buffered channel.
func (q *Queue[T]) PopFront(ctx context.Context, p *Pool[T]) (T, error) {
	value, _, err := q.PopFrontFunc(p, func(ch <-chan T) (T, bool, error) {
		select {
		case value := <-ch:
			return value, true, nil
		case <-ctx.Done():
			return *new(T), false, ctx.Err()
		}
	})
	return value, err
}

// TryPopFront attempts to receive a value without blocking. Returns the value
// and true if a value was immediately available, else the zero value and false.
// This is analogous to a non-blocking channel receive.
func (q *Queue[T]) TryPopFront(p *Pool[T]) (T, bool) {
	if value, ok := q.nextValues.PopFront(&p.valuePool); ok {
		return value, true
	}
	return q.pendingValues.PopFront(&p.valuePool)
}

type Pool[T any] struct {
	valuePool    nbcq.Pool[T]
	receiverPool nbcq.Pool[chan T]
	chanPool     sync.Pool
}

func (p *Pool[T]) getChan() chan T {
	ch, _ := p.chanPool.Get().(chan T)
	if ch == nil {
		ch = make(chan T, 1)
	}
	return ch
}

func (p *Pool[T]) putChan(ch chan T) {
	p.chanPool.Put(ch)
}
