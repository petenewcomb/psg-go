// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import (
	"context"
)

type Required[T any] struct {
	Optional[T]
	sharedChan chan T
}

// Must be called before first use.
func (q *Required[T]) Init(p *Pool[T]) {
	q.Optional.Init(p)
	q.sharedChan = make(chan T)
}

// PushSelectFunc is called when PushBackFunc needs to send to the shared
// channel.
type PushSelectFunc[T any] func(sharedCh chan<- T, value T)

func (q *Required[T]) PushBackFunc(p *Pool[T], value T, selectFn PushSelectFunc[T]) {
	if !q.Optional.TryPushBack(p, value) {
		selectFn(q.sharedChan, value)
	}
}

// PushBack sends a value to a waiting receiver, blocking until the value  or queues the value for a
// receiver to pick up later. This blocks until the value is sent or the context
// is cancelled.
func (q *Required[T]) PushBack(ctx context.Context, p *Pool[T], value T) error {
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
func (q *Required[T]) TryPushBack(p *Pool[T], value T) bool {
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

// Must return dedicatedCh if a value was received from it, otherwise nil or any
// other value. The return value could be a boolean flag, but returning the
// channel makes the code more robust: imagine an implementation that calls the
// parameters foo and bar but then has to make sure that it returns true only
// for foo and not for bar. Returning the channel that matches the select clause
// is slightly harder to get wrong.
type RequiredPopSelectFunc[T any] func(dedicatedCh, sharedCh <-chan T) <-chan T

func (q *Required[T]) PopFrontFunc(p *Pool[T], processOrphanFn ProcessValueFunc[T], selectFn RequiredPopSelectFunc[T]) {
	q.Optional.PopFrontFunc(p, processOrphanFn,
		func(dedicatedCh <-chan T) bool {
			sourceCh := selectFn(dedicatedCh, q.sharedChan)
			return sourceCh == dedicatedCh
		},
	)
}

func (q *Required[T]) PopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(p, processFn, func(dedicatedCh, sharedChan <-chan T) <-chan T {
		select {
		case value := <-dedicatedCh:
			processFn(value)
			return dedicatedCh
		case value := <-q.sharedChan:
			processFn(value)
		case <-ctx.Done():
			err = ctx.Err()
		}
		return nil
	})
	return err
}

func (q *Required[T]) TryPopFront(p *Pool[T], processFn ProcessValueFunc[T]) {
	q.PopFrontFunc(p, processFn, func(dedicatedCh, sharedChan <-chan T) <-chan T {
		select {
		case value := <-dedicatedCh:
			processFn(value)
			return dedicatedCh
		case value := <-q.sharedChan:
			processFn(value)
		default:
		}
		return nil
	})
}
