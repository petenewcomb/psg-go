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

type Pool[T any] struct {
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
