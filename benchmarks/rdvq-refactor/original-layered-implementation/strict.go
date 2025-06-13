//go:build exclude

// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import "context"

type Strict[T any] struct {
	baseQ[T]
}

// Init initializes the queue. Must be called before first use.
func (q *Strict[T]) Init(p *StrictPool[T]) {
	q.init(&p.basePool)
}

func (q *Strict[T]) TryPushBack(p *StrictPool[T], value T) bool {
	sent := true
	q.pushBackFunc(&p.basePool, value, func(T) {
		sent = false
	})
	return sent
}

// Must return true if a value was received from the given channel, false
// otherwise
type StrictPopSelectFunc[T any] func(ch <-chan T) bool

func (q *Strict[T]) PopFrontFunc(p *StrictPool[T], selectFn StrictPopSelectFunc[T], adoptValueFn OrphanedValueFunc[T]) {
	q.popFrontFunc(&p.basePool, selectFn, adoptValueFn)
}

func (q *Strict[T]) PopFront(ctx context.Context, p *StrictPool[T]) (T, error) {
	var value T
	var err error
	q.PopFrontFunc(p,
		func(ch <-chan T) bool {
			select {
			case value = <-ch:
				return true
			case <-ctx.Done():
				err = ctx.Err()
			}
			return false
		},
		func(orphanedValue T) {
			// Will never be called if the select function just above returned
			// true
			value = orphanedValue
			err = nil
		},
	)
	return value, err
}

func (q *Strict[T]) TryPopFront(p *StrictPool[T]) (T, bool) {
	var value T
	ok := false
	q.PopFrontFunc(p,
		func(ch <-chan T) bool {
			select {
			case value = <-ch:
				ok = true
				return true
			default:
			}
			return false
		},
		func(orphanedValue T) {
			// Will never be called if the select function just above returned
			// true
			value = orphanedValue
			ok = true
		},
	)
	return value, ok
}

type StrictPool[T any] struct {
	basePool[T]
}
