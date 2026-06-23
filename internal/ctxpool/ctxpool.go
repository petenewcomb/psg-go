// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package ctxpool provides utilities for associating values with contexts
// with caching and automatic cleanup.
package ctxpool

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/nbcq"
)

type childKeyType struct{}

var childKey childKeyType

var childPools atomic.Pointer[sync.Map] // context.Context -> *childPool
func init() {
	childPools.Store(&sync.Map{})
}

func WithValue[T any](ctx context.Context, value T) context.Context {
	// Check map first to avoid unbounded context parent chain walk unless the
	// ctx is truly new to us
	cp, ok := childPools.Load().Load(ctx)
	if !ok {
		cp = newChildPool(ctx)
	}
	return withValue[T](cp.(*childPool), value)
}

func GetValue[T any](ctx context.Context) (T, bool) {
	c := ctx.Value(childKey)
	if c == nil {
		return *new(T), false
	}
	return getValue[T](c.(*child))
}

func newChildPool(ctx context.Context) any {
	newPool := &childPool{
		parentMap: childPools.Load(),
		parentCtx: ctx,
	}
	newPool.free.Init()
	newPool.stopMu.Lock()
	defer newPool.stopMu.Unlock()
	p, ok := newPool.parentMap.LoadOrStore(ctx, newPool)
	if !ok {
		// newPool already potentially in use by other goroutines, but only
		// this goroutine knows that it is new. If the ctx is already
		// canceled, this will immediately remove it from pools.
		newPool.stop = context.AfterFunc(ctx, newPool.remove)
	}
	return p
}

func Free(ctx context.Context) {
	c := ctx.Value(childKey)
	if c != nil {
		c.(*child).Free()
	}
}

func Clear() {
	cps := childPools.Swap(&sync.Map{})
	cps.Range(func(_ any, v any) bool {
		cp := v.(*childPool)
		cp.stopMu.Lock()
		defer cp.stopMu.Unlock()
		cp.stop()
		return true
	})
	cps.Clear()
}

type childPool struct {
	parentMap *sync.Map
	parentCtx context.Context //nolint:containedctx // the ancestor every child ctx derives from
	stopMu    sync.Mutex
	stop      func() bool // from context.AfterFunc
	// free is this pool's reuse cache of spent children, the same lock-free
	// nbcq pattern as the funnel instanceQueue. Storage is
	// per-pool (not a type-global omnipool.For) because each child's ctx is
	// derived from THIS pool's parentCtx and cannot be reused under another.
	//
	// The childPool struct itself is NOT pooled: it is created once per unique
	// parent ctx and reused across every borrow under that ctx, so it is already a
	// cold-path allocation amortized alongside the unavoidable per-parent AfterFunc
	// registration + first WithValue node. Recycling it on eviction would reintroduce
	// a lookup-vs-evict-vs-reuse race (a concurrent WithValue holding a stale *childPool
	// pointer) that the GC lifecycle makes benign; closing it safely needs a refcount on
	// the hot borrow/Free path — net negative. Left to GC.
	free nbcq.Queue[*child]
}

func (cp *childPool) Get() *child {
	c, ok := cp.free.TryPopFront()
	if !ok {
		c = &child{pool: cp}
		c.ctx = context.WithValue(cp.parentCtx, childKey, c)
	}
	return c
}

func (cp *childPool) remove() {
	cp.stopMu.Lock()
	defer cp.stopMu.Unlock()
	cp.stop()
	cp.parentMap.Delete(cp.parentCtx)
}

func withValue[T any](cp *childPool, v T) context.Context {
	c := cp.Get()
	c.value = v
	return c.ctx
}

// entry stores a cached value along with the context that contains it
// and the cleanup function to cancel the AfterFunc.
type child struct {
	pool  *childPool
	ctx   context.Context //nolint:containedctx // context stamped with the value
	value any
}

func (c *child) Free() {
	// Skip re-pooling a child whose ctx is already cancelled: its pool is being
	// (or has been) removed from the map and the child will simply be GC'd. Clear
	// the transient value first so a re-pooled child never pins it between borrows;
	// the pool back-pointer and the reusable ctx persist.
	if c.ctx.Err() == nil {
		c.value = nil
		c.pool.free.PushBack(c)
	}
}

func getValue[T any](c *child) (T, bool) {
	v, ok := c.value.(T)
	return v, ok
}
