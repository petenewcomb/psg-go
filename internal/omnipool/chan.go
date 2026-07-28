// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"reflect"
	"sync"
)

// ChanPool is a type-safe wrapper around sync.Pool that handles unbuffered
// channel creation automatically.
type ChanPool[T any] struct {
	pool sync.Pool
}

func ForChan[T any]() *ChanPool[T] {
	typ := reflect.TypeFor[chan T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*ChanPool[T])
	}

	actual, _ := pools.LoadOrStore(typ, &ChanPool[T]{})
	return actual.(*ChanPool[T])
}

// Get retrieves a chan T from the pool. If the pool is empty, returns a new
// channel created by make(chan T)
func (p *ChanPool[T]) Get() chan T {
	pooled := p.pool.Get()
	if pooled != nil {
		return pooled.(chan T)
	}
	return make(chan T)
}

// Put returns a chan T to the pool, which must be unbuffered.
func (p *ChanPool[T]) Put(ch chan T) {
	if ch == nil {
		return
	}
	p.pool.Put(ch)
}

// GetChan is a package-level convenience function that gets a channel pool and retrieves a channel.
// For better performance, store and reuse a pool returned by ForChan.
func GetChan[T any]() chan T {
	pool := ForChan[T]()
	return pool.Get()
}

// Put is a package-level convenience function that gets a pool and puts an object.
// For better performance, store and reuse a pool returned by ForChan.
func PutChan[T any](ch chan T) {
	pool := ForChan[T]()
	pool.Put(ch)
}
