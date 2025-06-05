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

type Tolerant[T any] struct {
	Strict[T]
	nextValues nbcq.Queue[T]
}

// Init initializes the queue. Must be called before first use.
func (q *Tolerant[T]) Init(p *Pool[T]) {
	q.Strict.Init(&p.StrictPool)
	q.nextValues.Init(&p.valuePool)
}

// Must return true if a value was received from the given channel, false
// otherwise.
type TolerantPopSelectFunc[T any] func(ch <-chan T) PopSelectResult[T]

type PopSelectResult[T any] struct {
	Value         T
	OK            bool
	SourceChannel <-chan T
}

func NewPopSelectResult[T any](value T, ok bool, sourceCh <-chan T) PopSelectResult[T] {
	return PopSelectResult[T]{Value: value, OK: ok, SourceChannel: sourceCh}
}

func (q *Tolerant[T]) PopFrontFunc(p *Pool[T], selectFn TolerantPopSelectFunc[T]) (T, bool) {
	value, ok := q.nextValues.PopFront(&p.valuePool)
	if ok {
		return value, ok
	}
	q.Strict.PopFrontFunc(&p.StrictPool,
		func(ch <-chan T) bool {
			result := selectFn(ch)
			if result.OK {
				value = result.Value
				ok = true
			}
			return result.SourceChannel == ch
		},
		func(orphanedValue T) {
			if ok {
				q.nextValues.PushBack(&p.valuePool, orphanedValue)
			} else {
				value = orphanedValue
				ok = true
			}
		},
	)
	return value, ok
}

func (q *Tolerant[T]) PopFront(ctx context.Context, p *Pool[T]) (T, error) {
	var err error
	value, _ := q.PopFrontFunc(p, func(ch <-chan T) PopSelectResult[T] {
		select {
		case value := <-ch:
			return NewPopSelectResult(value, true, ch)
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
func (q *Tolerant[T]) TryPopFront(p *Pool[T]) (T, bool) {
	return q.PopFrontFunc(p, func(ch <-chan T) PopSelectResult[T] {
		select {
		case value := <-ch:
			return NewPopSelectResult(value, true, ch)
		default:
		}
		return PopSelectResult[T]{}
	})
}

type Pool[T any] struct {
	StrictPool[T]
	valuePool nbcq.Pool[T]
}
