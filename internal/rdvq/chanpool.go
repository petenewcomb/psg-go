// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue.
// It provides functionality similar to a buffered channel, but unbounded and
// with optimized direct handoff between producers and consumers.
package rdvq

import "github.com/petenewcomb/psg-go/internal/omnipool"

type chanPool[T any] = omnipool.CustomPool[chanTrait[T], chan T]

type chanTrait[T any] struct{}

func (c chanTrait[T]) Make() chan T {
	return make(chan T, 1)
}

func (c chanTrait[T]) Reset(ch chan T) {
	select {
	case ch <- *new(T):
		<-ch
	default:
		panic("attempt to pool non-empty channel")
	}
}

func chanPoolFor[T any]() *chanPool[T] {
	return omnipool.ForCustom(chanTrait[T]{})
}
