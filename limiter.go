// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
)

// Limiter is the user-facing concurrency-control primitive. Limiters are
// bound to an op via [WithLimits] at construction time; the op's
// dispatch pipeline acquires a permit before the work runs and releases
// it when the work completes.
//
// Limiters are values, but the underlying state is shared by reference:
// copying a Limiter does not duplicate its permits. Sharing the same
// Limiter across multiple ops makes them compete for the same pool of
// permits.
//
// In v0.x the internal contract is closed — Limiter is a sealed type
// constructed only via framework functions ([NewSemaphore], later
// NewRateLimit, etc.). This keeps the framework free to evolve the
// acquire/notify machinery without breaking users. Custom concurrency
// logic that doesn't fit the built-ins should live inside the user's
// Task or Accumulator body, calling whatever blocking primitive is
// appropriate.
type Limiter struct {
	impl limiterImpl
}

// limiterImpl is the internal contract every Limiter constructor must
// satisfy. It is unexported, so external packages cannot introduce new
// Limiter types.
type limiterImpl interface {
	// tryAcquire returns true if a permit was reserved. The caller is
	// then responsible for a matching release.
	tryAcquire() bool
	// release returns a permit and wakes any waiter that was blocked
	// because no permit was available.
	release()
	// notifier returns the wake-up notifier waiters subscribe to while
	// blocked. May be nil for Limiters that never block (e.g. an
	// unlimited Semaphore).
	notifier() *workq.Notifier
}

// SetMaxConcurrency adjusts the maximum number of simultaneously-held
// permits on a [Semaphore]-backed Limiter. Use n < 0 for unlimited.
// Panics if l is not Semaphore-backed.
//
// Raising the limit immediately unblocks waiters up to the new ceiling;
// lowering it lets in-flight work drain naturally — no permits are
// revoked.
func SetMaxConcurrency(l Limiter, n int) {
	s, ok := l.impl.(*semaphoreLimiter)
	if !ok {
		panic("SetMaxConcurrency: Limiter is not Semaphore-backed")
	}
	s.setMaxConcurrency(n)
}

// NewSemaphore returns a Limiter that grants at most n simultaneous
// permits. Use n < 0 for unlimited (the same semantics as the
// pre-Wave-4 [NewTaskPool] default). n == 0 blocks all dispatches.
//
// The Semaphore replaces what was a [TaskPool] property in the pre-
// Wave-4 API: WithMaxConcurrency on the pool moves to
// `WithLimits(NewSemaphore(n))` on the op.
func NewSemaphore(n int) Limiter {
	if n < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", n))
	}
	if n > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", n, math.MaxInt32))
	}
	s := &semaphoreLimiter{}
	s.maxConcurrency.Store(int32(n))
	s.notify.Init()
	return Limiter{impl: s}
}

// semaphoreLimiter is the in-flight-counter-backed Limiter
// implementation. Mirrors the pre-Wave-4 TaskPool semantics:
// maxConcurrency==-1 means unlimited; ==0 blocks everything; >0 caps to
// that many concurrent permits.
type semaphoreLimiter struct {
	maxConcurrency atomic.Int32
	inFlight       jobstate.InFlightCounter
	notify         workq.Notifier
}

func (s *semaphoreLimiter) tryAcquire() bool {
	limit := s.maxConcurrency.Load()
	switch {
	case limit < 0:
		s.inFlight.Increment()
		return true
	case limit == 0:
		return false
	default:
		return s.inFlight.IncrementIfUnder(int(limit))
	}
}

func (s *semaphoreLimiter) release() {
	limit := s.maxConcurrency.Load()
	if s.inFlight.DecrementAndCheckIfUnder(int(limit)) {
		s.notify.Notify(nil)
	}
}

func (s *semaphoreLimiter) notifier() *workq.Notifier {
	return &s.notify
}

func (s *semaphoreLimiter) setMaxConcurrency(limit int) {
	if limit < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", limit))
	}
	if limit > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", limit, math.MaxInt32))
	}
	oldLimit := s.maxConcurrency.Swap(int32(limit))
	switch {
	case limit == -1:
		s.notify.NotifyAll()
	case oldLimit != -1:
		for range max(0, limit-int(oldLimit)) {
			s.notify.Notify(nil)
		}
	}
}

// limiterScatterWork wraps an inner workq.Work with a Limiter
// acquire-on-enter / release-on-completion gate. Replaces the
// pre-Wave-4 taskPoolScatterWork, which was hard-coded to TaskPool's
// in-flight semaphore.
type limiterScatterWork struct {
	workq.Work
	job      *Pool
	limiter  Limiter
	deadline time.Time
	acquired bool
}

func newLimiterScatterWork(
	job *Pool, deadline time.Time, inner workq.Work, limiter Limiter,
) *limiterScatterWork {
	w := limiterScatterWorkPool.Get()
	w.Work = inner
	w.job = job
	w.limiter = limiter
	w.deadline = deadline
	w.acquired = false
	return w
}

func (w *limiterScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	wb := workq.WaitBehavior{
		BlockBehavior: w.job.protoBB,
		ShouldWait: func() bool {
			if w.acquired {
				return false
			}
			w.acquired = w.limiter.impl.tryAcquire()
			return !w.acquired
		},
	}

	defer func() {
		// If the work didn't start, release the permit we reserved.
		if !ex.Started() && w.acquired {
			w.limiter.impl.release()
			w.acquired = false
		}
	}()

	return workq.ExecuteOrWait(ctx, ex, w.deadline, w.limiter.impl.notifier(), wb,
		func(ctx context.Context, ex workq.Execution) error {
			return w.Work.Execute(ctx, ex)
		})
}

func (w *limiterScatterWork) Free() {
	w.Work.Free()
	limiterScatterWorkPool.Put(w)
}

var limiterScatterWorkPool = omnipool.For[limiterScatterWork]()

// limiterCompletedFn returns the per-task completion callback that
// releases this Limiter's permit. Wired into the task work's
// completedFn so the permit returns at the moment the worker finishes
// executing the user body. Returns nil if l has no impl.
func limiterCompletedFn(l Limiter) func() {
	if l.impl == nil {
		return nil
	}
	return l.impl.release
}
