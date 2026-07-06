// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"fmt"
	"math"

	"github.com/petenewcomb/streampool/internal/permits"
)

// WeightedLimiter is a weight-capable concurrency limiter: its overdraft policy can
// answer a demand that does not fit the current capacity — WAIT while paused, REFUSE when
// permanently oversized — which is what makes it safe to WEIGH. Bind one to an op with
// [NewWeightLimiter] + Launcher.WithWeightLimits.
//
// Weight-CAPABILITY is a property of the limiter; the WEIGHER is a property of the
// (op, limiter) binding. A plain [Limiter] ([NewSemaphore]) is NOT a WeightedLimiter and
// cannot be weighed — the compile-time guard against silently weighing a wait-only pool,
// where a w > cap demand would park forever. The two do not cross-assign in either
// direction; genuine weight-1 use of a weighted pool is spelled explicitly as
// NewWeightLimiter(wl, func(T) int { return 1 }).
//
// Sealed like [Limiter]: constructed only via framework functions ([NewWeightedSemaphore]
// today; weighted rate limiters later). Deliberately an interface rather than a concrete
// struct — future weight-capable limiters implement it with no breaking change.
type WeightedLimiter interface {
	// weightedPool returns the permit Pool backing this limiter. Unexported: the seal —
	// only this package implements WeightedLimiter.
	weightedPool() *permits.Pool
}

// weightedSemaphore is the concrete pointer implementation of [WeightedLimiter] (a
// pointer so the interface value never boxes/allocates — it holds the pointer in its data
// word). The weighted analogue of the plain [Limiter] handle: a thin holder of the permit
// Pool, whose Resource is a weightedSemaphoreResource.
type weightedSemaphore struct {
	pool *permits.Pool
}

func (w *weightedSemaphore) weightedPool() *permits.Pool { return w.pool }

// NewWeightedSemaphore returns a WeightedLimiter that caps the total WEIGHT of
// simultaneously-held permits at n. Use n < 0 for unlimited; n == 0 blocks (pauses) every
// dispatch until raised. A demand of weight w is admitted only when w fits under the
// ceiling; a demand permanently heavier than a nonzero ceiling (w > n) is refused with
// [ErrWeightExceedsCapacity] rather than parked forever.
//
// Share one WeightedLimiter across ops for a collective weight budget; each op supplies
// its own weigher via [NewWeightLimiter].
//
//nolint:contextcheck // permits.NewPool.Init uses background context for tracing only
func NewWeightedSemaphore(n int) WeightedLimiter {
	if n < -1 {
		panic(fmt.Sprintf("max concurrency %d is less than minimum allowed value of -1", n))
	}
	if n > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency %d exceeds maximum allowed value of %d", n, math.MaxInt32))
	}
	r := &weightedSemaphoreResource{}
	r.maxConcurrency.Store(int32(n))
	p := permits.NewPool(r)
	// A capacity raise frees weight at the Resource without any cache returning a permit,
	// a multi-permit event of unknown usable size — seed the Pool's wake chain (see
	// NewSemaphore).
	r.capacityChangedFn = p.ChainProbe
	return &weightedSemaphore{pool: p}
}

// WeightLimiter pairs a weight-capable limiter with a weigher for one op-value type T — a
// reusable typed binding: define once, share across same-T ops. The weigher is a property
// of the (op, limiter) binding (the same [WeightedLimiter] can be weighed differently by
// different ops); weight-capability is a property of the limiter. Build one with
// [NewWeightLimiter] and bind it with Launcher.WithWeightLimits.
type WeightLimiter[T any] struct {
	limiter WeightedLimiter
	weigh   func(T) int
}

// NewWeightLimiter binds weigh to l. weigh maps each dispatched value to the weight
// (permits) its work should acquire from l; it is called once per dispatch and must
// return a positive weight. For a genuinely weight-1 use of a weighted pool, pass
// func(T) int { return 1 }.
func NewWeightLimiter[T any](l WeightedLimiter, weigh func(T) int) WeightLimiter[T] {
	if l == nil {
		panic("NewWeightLimiter: limiter must be non-nil")
	}
	if weigh == nil {
		panic("NewWeightLimiter: weigh func must be non-nil")
	}
	return WeightLimiter[T]{limiter: l, weigh: weigh}
}

// weightedSemaphoreResource shares the plain semaphore's accounting (embedded
// [semaphoreResource]: TryAcquire/Release/setMaxConcurrency, weight-aware already) but
// carries the WEIGHTED overdraft policy in place of the plain wait-only one.
type weightedSemaphoreResource struct {
	semaphoreResource
}

// Overdraft implements [permits.OverdraftResource] with the weighted policy, overriding
// the embedded plain (false,nil)=wait. It refuses ONLY a permanently-oversized demand —
// weight strictly beyond a nonzero ceiling (n > limit), which can never fit at any point —
// with a per-unit [ErrWeightExceedsCapacity]. Every other demand WAITS, exactly like the
// plain semaphore.
//
// Crucially, reaching here does NOT imply n > limit. The overdraft proof requires zero
// forest inUse, but NOT zero held: capacity checked out from the Resource yet sitting idle
// in caches (cache-don't-return) counts against the ceiling while the forest inUse is
// zero, so a demand of weight n <= limit can reach here transiently when the gather did
// not (yet) assemble that borrowable capacity. Refusing it would be a terminal failure of
// a demand that will fit once a release or steal re-drives it — so it must wait. Paused
// (limit 0) waits too (a SetMaxConcurrency raise admits it); unlimited (limit < 0) never
// reaches overdraft. Refuse never grants, so it is safe pre-step-4.
//
// STILL OPEN (step-4 policy): a weighted *concurrency* cap could soft-GRANT brief
// over-concurrency for an oversized demand instead of refusing; a memory budget refuses
// (hard wall). Layer 1 refuses the oversized case uniformly.
func (s *weightedSemaphoreResource) Overdraft(n int) (bool, error) {
	limit := s.maxConcurrency.Load()
	if limit > 0 && int64(n) > int64(limit) {
		return false, fmt.Errorf("%w: weight %d exceeds capacity %d", ErrWeightExceedsCapacity, n, limit)
	}
	return false, nil // paused, or transient (n <= limit, capacity held borrowable): wait
}
