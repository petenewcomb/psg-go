// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"reflect"
	"sync"
)

// Trait defines the interface for types that can create and reset pooled objects.
// This enables custom object lifecycle management while keeping the pooled type's API clean.
type Trait[P any] interface {
	Make() P
	Reset(P)
}

// CustomPool is a type-safe wrapper around sync.Pool that handles object P
// creation and resetting automatically using the provided trait type T.
// The trait encapsulates both creation logic (Make) and cleanup logic (Reset),
// keeping the pooled type's public API free of pool-related methods.
type CustomPool[T Trait[P], P any] struct {
	pool  sync.Pool
	trait T
}

// ForCustom returns a shared pool instance for type P as managed by trait type T.
// Multiple calls with the same types T, P will return the same pool instance,
// enabling efficient sharing across different parts of the application.
// The trait parameter is used only for type inference; its value is ignored.
func ForCustom[T Trait[P], P any](trait T) *CustomPool[T, P] {
	typ := reflect.TypeFor[T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*CustomPool[T, P])
	}

	pool := &CustomPool[T, P]{}
	actual, _ := pools.LoadOrStore(typ, pool)
	return actual.(*CustomPool[T, P])
}

// Get retrieves a P from the pool. If the pool is empty, creates a new
// object using T{}.Make().
func (p *CustomPool[T, P]) Get() P {
	pooled := p.pool.Get()
	if pooled != nil {
		var zero P
		if reflect.TypeOf(pooled) == reflect.TypeOf(zero) {
			return pooled.(P)
		}
	}
	return p.trait.Make()
}

// Put returns a P to the pool after resetting it using T{}.Reset(p).
func (p *CustomPool[T, P]) Put(obj P) {
	p.trait.Reset(obj)
	p.pool.Put(obj)
}

// GetCustom is a package-level convenience function that gets a custom pool and retrieves an object.
// For better performance, store and reuse a pool returned by ForCustom.
// The trait parameter is used only for type inference; its value is ignored.
func GetCustom[T Trait[P], P any](trait T) P {
	pool := ForCustom(trait)
	return pool.Get()
}

// PutCustom is a package-level convenience function that gets a pool and puts an object.
// For better performance, store and reuse a pool returned by ForCustom.
// The trait parameter is used only for type inference; its value is ignored.
func PutCustom[T Trait[P], P any](trait T, obj P) {
	pool := ForCustom(trait)
	pool.Put(obj)
}
