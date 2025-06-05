// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import (
	"context"
)

type Patient[T any] struct {
	Tolerant[T]
	sharedChan chan T
}

// Init initializes the queue. Must be called before first use.
func (q *Patient[T]) Init(p *Pool[T]) {
	q.Tolerant.Init(p)
	q.sharedChan = make(chan T)
}

// PushSelectFunc is called when PushBackFunc needs to send to the shared channel.
// It receives the shared channel and should implement custom blocking logic
// (e.g., selecting on context cancellation).
type PushSelectFunc[T any] func(sharedCh chan<- T, value T)

// PushBackFunc sends a value to a waiting receiver using custom shared logic.
// If no waiting receiver is available, it calls selectFn with the shared channel.
// This allows the caller to implement complex blocking patterns (e.g., context
// cancellation) without creating additional goroutines.
// Returns whether the value was sent.
func (q *Patient[T]) PushBackFunc(p *Pool[T], value T, selectFn PushSelectFunc[T]) {
	q.pushBackFunc(&p.basePool, value, func(value T) {
		selectFn(q.sharedChan, value)
	})
}

// PushBack sends a value to a waiting receiver, or queues the value for a
// receiver to pick up later. This blocks until the value is sent or the context is cancelled.
func (q *Patient[T]) PushBack(ctx context.Context, p *Pool[T], value T) error {
	var err error
	q.PushBackFunc(p, value, func(sharedCh chan<- T, value T) {
		select {
		case sharedCh <- value:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

// TryPushBack attempts to send a value without blocking.
// Returns true if the value was sent to a waiting receiver, false otherwise.
// This is analogous to a non-blocking channel send.
func (q *Patient[T]) TryPushBack(p *Pool[T], value T) bool {
	sent := true
	q.PushBackFunc(p, value, func(sharedCh chan<- T, value T) {
		select {
		case sharedCh <- value:
		default:
			// No waiting receivers
			sent = false
		}
	})
	return sent
}

// Must return true if a value was received from the given channel, false
// otherwise.
type PatientPopSelectFunc[T any] func(dedicatedCh, sharedCh <-chan T) PopSelectResult[T]

func (q *Patient[T]) PopFrontFunc(p *Pool[T], selectFn PatientPopSelectFunc[T]) (T, bool) {
	return q.Tolerant.PopFrontFunc(p, func(dedicatedCh <-chan T) PopSelectResult[T] {
		return selectFn(dedicatedCh, q.sharedChan)
	})
}

func (q *Patient[T]) PopFront(ctx context.Context, p *Pool[T]) (T, error) {
	var err error
	value, _ := q.PopFrontFunc(p, func(dedicatedCh, sharedChan <-chan T) PopSelectResult[T] {
		select {
		case value := <-dedicatedCh:
			return NewPopSelectResult(value, true, dedicatedCh)
		case value := <-q.sharedChan:
			return NewPopSelectResult(value, true, q.sharedChan)
		case <-ctx.Done():
			err = ctx.Err()
		}
		return PopSelectResult[T]{}
	})
	// We can ignore the "OK" value from PopFrontFunc because it would be false
	// only if the context was cancelled, in which case error would be non-nil.
	return value, err
}

// PopFront receives a value from the sent value queue or waits for one to
// arrive. It blocks until a value is available or the context is cancelled.
// This is analogous to receiving from a buffered channel.
func (q *Patient[T]) TryPopFront(p *Pool[T]) (T, bool) {
	return q.PopFrontFunc(p, func(dedicatedCh, sharedChan <-chan T) PopSelectResult[T] {
		select {
		case value := <-dedicatedCh:
			return NewPopSelectResult(value, true, dedicatedCh)
		case value := <-q.sharedChan:
			return NewPopSelectResult(value, true, q.sharedChan)
		default:
		}
		return PopSelectResult[T]{}
	})
}
