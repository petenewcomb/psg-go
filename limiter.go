// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/petenewcomb/streampool/internal/wavestate"
	"github.com/petenewcomb/streampool/internal/workq"
)

// Limiter is the user-facing concurrency-control primitive. Limiters are
// bound to an op via [WithLimits] at construction time; the op's
// dispatch pipeline acquires a permit before the work runs and releases
// it when the work completes.
//
// Limiters are values, but the underlying state is shared by reference: copying a
// Limiter does not duplicate its permits. Sharing the same Limiter across multiple ops
// makes them compete for the same pool of permits.
//
// In v0.x the internal contract is closed — Limiter is a sealed type constructed only
// via framework functions ([NewSemaphore], later NewRateLimit, etc.). This keeps the
// framework free to evolve the acquire/notify machinery without breaking users. Custom
// concurrency logic that doesn't fit the built-ins should live inside the user's Task or
// Accumulator body, calling whatever blocking primitive is appropriate.
type Limiter struct {
	// pool is the permit-core allocation boundary for this Limiter (one Pool per
	// Limiter, shared by reference across copies). Each Wave that an op bound to this
	// Limiter dispatches into lazily creates a per-wave Cache drawing on this Pool (the
	// C_W^L forest); a body acquires its permit from that cache. nil only on the zero
	// (unlimited) Limiter.
	pool *permits.Pool
}

// SetMaxConcurrency adjusts the maximum number of simultaneously-held
// permits on a [Semaphore]-backed Limiter. Use n < 0 for unlimited.
// Panics if l is not Semaphore-backed.
//
// Raising the limit immediately unblocks waiters up to the new ceiling;
// lowering it lets in-flight work drain naturally — no permits are
// revoked.
func SetMaxConcurrency(l Limiter, n int) {
	if l.pool == nil {
		panic("SetMaxConcurrency: Limiter is not Semaphore-backed")
	}
	sem, ok := l.pool.Resource().(*semaphoreResource)
	if !ok {
		panic("SetMaxConcurrency: Limiter is not Semaphore-backed")
	}
	sem.setMaxConcurrency(n)
}

// NewSemaphore returns a Limiter that grants at most n simultaneous
// permits. Use n < 0 for unlimited; n == 0 blocks all dispatches.
//
// Share one Limiter across ops for a collective cap; pass several to a
// single op's [WithLimits] for AND-composition (admitted jointly in a
// global order, deadlock-free).
//
//nolint:contextcheck // permits.NewPool.Init uses background context for tracing only
func NewSemaphore(n int) Limiter {
	if n < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", n))
	}
	if n > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", n, math.MaxInt32))
	}
	r := &semaphoreResource{}
	r.maxConcurrency.Store(int32(n))
	p := permits.NewPool(r)
	// A capacity raise (SetMaxConcurrency) frees capacity at the Resource without
	// any cache returning a permit: a capacity event. A multi-unit raise needs no
	// herd — the cascade rule admits claimants one per delivery until a miss.
	r.capacityChangedFn = p.NotifyCapacity
	return Limiter{pool: p}
}

// semaphoreResource is the in-flight-counter-backed concurrency [permits.Resource]:
// maxConcurrency==-1 means unlimited; ==0 blocks everything; >0 caps to that many
// concurrent permits. Pure accounting — wakeups belong to the owning permits.Pool.
type semaphoreResource struct {
	maxConcurrency    atomic.Int32
	inFlight          wavestate.InFlightCounter
	capacityChangedFn func() // mints into the pool notifier when the ceiling is raised
}

// TryAcquire and Release implement [permits.Resource]. n is the weight: the whole
// amount is admitted atomically or not at all (a partial admit would strand the
// remainder — the same all-or-nothing contract the gather's shortfall arm assumes).
func (s *semaphoreResource) TryAcquire(n int) bool {
	limit := s.maxConcurrency.Load()
	switch {
	case limit < 0:
		for range n {
			s.inFlight.Increment()
		}
		return true
	case limit == 0:
		return false
	default:
		return s.inFlight.AddIfUnder(n, int(limit))
	}
}

func (s *semaphoreResource) Release(n int) {
	for range n {
		s.inFlight.Decrement()
	}
}

// Overdraft implements permits.OverdraftResource: the pool has PROVEN the head
// demand infeasible at current capacity with zero permits in use anywhere. For
// every weight, the answer is "not now" (granted=false, err=nil) — the head
// keeps waiting, re-driven by a release or a SetMaxConcurrency raise; there is no
// commitment.
//
// Why not grant yet: an overdraft episode exempts the grantee's causal subtree from
// the head-of-line gate, but that subtree is not representable until
// weighted-acquisition step 4 wires the body-cache meta-redirect. Pre-step-4 an
// episode owner's own downstream dispatches would be gated behind its own episode
// and wedge whenever the owner blocks on them, so granting is unsafe regardless of
// weight. Step 4 decides the real policy — at minimum "not now" while PAUSED
// (limit 0 keeps blocking until a raise); grant-vs-refuse for a demand heavier than
// a nonzero ceiling is an open call (see weighted-acquisition.md).
func (s *semaphoreResource) Overdraft(int) (bool, error) {
	return false, nil
}

func (s *semaphoreResource) setMaxConcurrency(limit int) {
	if limit < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", limit))
	}
	if limit > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", limit, math.MaxInt32))
	}
	s.maxConcurrency.Store(int32(limit))
	// Wake parked managers/executors to re-check against the new ceiling. A raise lets
	// some in; a lowering wakes them to a still-full check (a harmless re-park). No
	// permits are revoked — in-flight work drains naturally.
	if s.capacityChangedFn != nil {
		s.capacityChangedFn()
	}
}

// limiterScatterWork gates an inner workq.Work behind a Limiter permit. It drives the
// handle only through the gate phase (acquire, and release-for-retry when the gated work
// can't start); the handle's lifecycle — release at body completion, recycle — is owned
// by the taskWork the handle travels with across the queue hand-off.
type limiterScatterWork struct {
	workq.Work
	wave *waveImpl
	h    *heldPermit
}

func newLimiterScatterWork(wv *waveImpl, inner workq.Work, h *heldPermit) *limiterScatterWork {
	wk := limiterScatterWorkPool.Get()
	wk.Work = inner
	wk.wave = wv
	wk.h = h
	return wk
}

func (wk *limiterScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	held, err := wk.h.acquireJoint(ctx, ex, wk.wave)
	if err != nil || !held {
		return err
	}
	err = wk.Work.Execute(ctx, ex)
	if !ex.Started() {
		// Acquired, but the inner post couldn't start (downstream queue full under
		// postpone discipline): release the permit while the work waits for queue
		// space; the gate re-acquires on retry (Acquire is state-free). See "The
		// POSTPONED state" in docs/limiter-suspend-resume.md.
		wk.h.release()
	}
	return err
}

func (wk *limiterScatterWork) Free() {
	wk.Work.Free()
	// wk.h is owned by the taskWork (released and recycled in taskWork.Free); just
	// drop the reference via the pool's zeroing Put.
	limiterScatterWorkPool.Release(wk)
}

var limiterScatterWorkPool = omnipool.For[limiterScatterWork]()
