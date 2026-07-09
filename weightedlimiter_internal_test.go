// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"

	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/stretchr/testify/require"
)

// These cover the weighted limiter's core mechanic at the pool level: an Acquire of
// weight w consumes w of the ceiling, and the weighted overdraft policy (paused → wait,
// oversized → refuse). The dispatch wiring (weigher → heldPermit.weight) is covered
// end-to-end in weightedlimiter_test.go.

func TestWeightedSemaphore_WeightedAcquire(t *testing.T) {
	chk := require.New(t)
	wl := NewWeightedSemaphore(5)
	c := wl.weightedPool().NewCache()

	d := permits.NewDemand()
	p1, err := c.Acquire(d, 3)
	chk.NoError(err)
	chk.True(p1.Held(), "weight 3 fits under ceiling 5")

	d2 := permits.NewDemand()
	p2, err := c.Acquire(d2, 3)
	chk.NoError(err)
	chk.False(p2.Held(), "a second weight-3 would total 6 > 5: must wait, not refuse")

	p1.Release()
	p3, err := c.Acquire(d2, 3)
	chk.NoError(err)
	chk.True(p3.Held(), "after releasing the first, weight 3 fits again")

	p3.Release()
	d.Free()
	d2.Free()
	c.ReleaseRef()
}

func TestWeightedSemaphore_OversizedRefuses(t *testing.T) {
	chk := require.New(t)
	wl := NewWeightedSemaphore(5)
	c := wl.weightedPool().NewCache()

	d := permits.NewDemand()
	pm, err := c.Acquire(d, 6)
	chk.ErrorIs(err, ErrWeightExceedsCapacity, "weight 6 permanently exceeds ceiling 5: refuse, not wait")
	chk.False(pm.Held())

	d.Free()
	c.ReleaseRef()
}

// Reaching Overdraft does NOT imply oversized: the proof requires zero forest inUse but
// not zero HELD, so a demand of weight <= ceiling can arrive here transiently (capacity
// held borrowable by idle caches the gather has not assembled). It must WAIT, not refuse —
// refusing a fitting demand is a terminal failure that strands its postponed work (the
// weighted-sim wedge this regresses). Only n > ceiling is a permanent refusal.
func TestWeightedSemaphore_OverdraftWaitsWhenItFits(t *testing.T) {
	chk := require.New(t)
	r := &weightedSemaphoreResource{}
	r.maxConcurrency.Store(3)

	for _, n := range []int{1, 2, 3} { // n <= ceiling: wait, never refuse
		granted, err := r.Overdraft(n)
		chk.False(granted)
		chk.NoErrorf(err, "weight %d fits ceiling 3: must wait at overdraft, not refuse", n)
	}
	granted, err := r.Overdraft(4) // n > ceiling: permanent refuse
	chk.False(granted)
	chk.ErrorIs(err, ErrWeightExceedsCapacity)

	r.maxConcurrency.Store(0) // paused: wait for a raise, never refuse
	granted, err = r.Overdraft(5)
	chk.False(granted)
	chk.NoError(err)
}

func TestWeightedSemaphore_PausedWaits(t *testing.T) {
	chk := require.New(t)
	wl := NewWeightedSemaphore(0) // paused
	c := wl.weightedPool().NewCache()

	d := permits.NewDemand()
	pm, err := c.Acquire(d, 6)
	chk.NoError(err, "paused (ceiling 0) waits for a raise — no per-unit refusal")
	chk.False(pm.Held())

	d.Free()
	c.ReleaseRef()
}

// WithWeightLimits records the weighted pool and the weigher on the op copy; WithLimits
// and WithWeightLimits are mutually exclusive single-limiter binders (multi-composition
// is a later follow-up).
func TestWithWeightLimits_BindsWeigher(t *testing.T) {
	chk := require.New(t)
	wl := NewWeightedSemaphore(4)
	base := NewFnLauncher(func(_ context.Context, _ int, _ error) error { return nil })

	bound := base.WithWeightLimits(NewWeightLimiter(wl, func(v int) int { return v * 2 }))
	chk.Len(bound.bindings, 1)
	chk.Same(wl.weightedPool(), bound.bindings[0].pool, "the weighted pool is bound")
	chk.NotNil(bound.bindings[0].weigh, "the weigher is recorded")
	chk.Equal(6, bound.bindings[0].weigh(3), "the recorded weigher is the one supplied")

	chk.Empty(base.bindings, "the original op copy is unmodified")

	chk.PanicsWithValue(
		"multi-Limiter composition is not yet implemented (Wave 4 follow-up)",
		func() { bound.WithLimits(NewSemaphore(1)) },
		"binding a second limiter panics",
	)
}
