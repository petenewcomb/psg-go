// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import (
	"context"
	"sync"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type Queue[T any] struct {
	nextValues       nbcq.Queue[T]
	sharedChan       chan T
	waitingReceivers nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *Queue[T]) Init(p *Pool[T]) {
	q.nextValues.Init(&p.valuePool)
	q.sharedChan = make(chan T)
	q.waitingReceivers.Init(&p.receiverPool)
}

// PushFunc is called when PushBackFunc needs to send to the shared channel.
// It receives the shared channel and should implement custom blocking logic
// (e.g., selecting on context cancellation).
type PushFunc[T any] func(ch chan<- T, value T)

// PushBackFunc sends a value to a waiting receiver using custom shared logic.
// If no waiting receiver is available, it calls pushFn with the shared channel.
// This allows the caller to implement complex blocking patterns (e.g., context
// cancellation) without creating additional goroutines.
// Returns whether the value was sent.
func (q *Queue[T]) PushBackFunc(p *Pool[T], value T, pushFn PushFunc[T]) {
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

	// No waiting receivers, use custom push function
	pushFn(q.sharedChan, value)
}

// PushBack sends a value to a waiting receiver, or queues the value for a
// receiver to pick up later. This blocks until the value is sent or the context is cancelled.
func (q *Queue[T]) PushBack(ctx context.Context, p *Pool[T], value T) error {
	var err error
	q.PushBackFunc(p, value, func(ch chan<- T, value T) {
		select {
		case ch <- value:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
func (q *Queue[T]) TryPushBack(p *Pool[T], value T) bool {
	sent := true
	q.PushBackFunc(p, value, func(ch chan<- T, v T) {
		select {
		case ch <- v:
		default:
			// No waiting receivers
			sent = false
		}
	})
	return sent
}

// BlockFunc is called when PopFrontFunc needs to block waiting for a value. It
// receives two channel on which a value might arrive and should implement
// custom blocking logic (e.g., selecting on those channels and potentially
// more). It returns the received value and which channel it came from, else the
// zero value and nil.
type BlockFunc[T any] func(dedicatedCh, sharedCh <-chan T) BlockResult[T]

type BlockResult[T any] struct {
	Value         T
	OK            bool
	SourceChannel <-chan T
}

func NewBlockResult[T any](value T, ok bool, sourceCh <-chan T) BlockResult[T] {
	return BlockResult[T]{Value: value, OK: ok, SourceChannel: sourceCh}
}

// PopFrontFunc receives values from the queue using custom blocking logic.
// It calls processFn with each value dequeued (including any drained values
// from cleanup). If blockFn returns false, PopFrontFunc returns without calling processFn.
func (q *Queue[T]) PopFrontFunc(p *Pool[T], blockFn BlockFunc[T]) (value T, ok bool) {

	if value, ok = q.nextValues.PopFront(&p.valuePool); ok {
		return
	}

	// Prepare to register ourselves as a waiting receiver
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
				drainedValue := <-receiverCh
				if ok {
					q.nextValues.PushBack(&p.valuePool, drainedValue)
				} else {
					value = drainedValue
					ok = true
				}
				// PushBack must have pulled it out of the queue and we just
				// emptied it, so it's safe to return to the pool.
				p.putChan(receiverCh)
			}
		}
	}()

	// Register ourselves as a waiting receiver.
	q.waitingReceivers.PushBack(&p.receiverPool, receiverCh)

	// Call the custom blocking function
	result := blockFn(receiverCh, q.sharedChan)
	if result.SourceChannel == receiverCh {
		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(receiverCh)
		// Make sure that the deferred cleanup function knows there's nothing
		// left to do.
		receiverCh = nil
	}
	return result.Value, result.OK
}

// PopFront receives a value from the sent value queue or waits for one to
// arrive. It blocks until a value is available or the context is cancelled.
// This is analogous to receiving from a buffered channel.
func (q *Queue[T]) PopFront(ctx context.Context, p *Pool[T]) (T, error) {
	var err error
	value, _ := q.PopFrontFunc(p, func(dedicatedCh, sharedCh <-chan T) BlockResult[T] {
		select {
		case value := <-dedicatedCh:
			return NewBlockResult(value, true, dedicatedCh)
		case value := <-sharedCh:
			return NewBlockResult(value, true, sharedCh)
		case <-ctx.Done():
			err = ctx.Err()
		}
		return NewBlockResult(*new(T), false, nil)
	})
	return value, err
}

// TryPopFront attempts to receive a value without blocking. Calls processFn
// with the value if one was immediately available, otherwise returns false.
// Note: processFn may be called up to twice if cleanup drains an abandoned value.
// This is analogous to a non-blocking channel receive.
func (q *Queue[T]) TryPopFront(p *Pool[T]) (T, bool) {
	if value, ok := q.nextValues.PopFront(&p.valuePool); ok {
		return value, true
	}
	select {
	case value := <-q.sharedChan:
		return value, true
	default:
		return *new(T), false
	}
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
