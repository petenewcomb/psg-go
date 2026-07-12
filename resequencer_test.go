// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestResequencer scatters work that completes in arbitrary order and verifies
// the resequencer delivers every result to the handler in ascending sequence
// order. The handler appends to a slice with no locking, so the race detector
// would flag any violation of the single-instance (serial) guarantee.
func TestResequencer(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	const n = 200

	wave := streampool.NewWave()
	var got []int
	rs := streampool.NewFnResequencer[int](wave, 0,
		func(_ context.Context, v int, err error) error {
			got = append(got, v) // serial: one instance, no lock needed
			return err
		})

	// Each task sleeps a pseudo-varied amount so completions interleave, then
	// submits its result at its in-order sequence position.
	src := streampool.NewLauncher[int](streampool.HandlerFunc[int](
		func(ctx context.Context, i int, _ error) error {
			time.Sleep(time.Duration(i%7) * time.Millisecond)
			return rs.Submit(ctx, uint64(i), i) //nolint:gosec // G115: i is a non-negative loop index
		}))

	for i := n - 1; i >= 0; i-- { // submit in reverse to stress reordering
		chk.NoError(src.In(wave).Submit(ctx, i))
	}
	chk.NoError(wave.CloseAndSkimAll(ctx))

	chk.Len(got, n)
	for i := 0; i < n; i++ {
		chk.Equal(i, got[i], "result %d delivered out of order", i)
	}
}

// TestRangeResequencer submits variable-width contiguous segments out of order
// and verifies they are delivered in offset order (the next expected offset
// advances by each segment's length).
func TestRangeResequencer(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	type seg struct {
		offset, length uint64
		val            string
	}
	// Contiguous tiling of [0,15): offsets 0,3,5,10,11.
	segs := []seg{
		{0, 3, "a"},
		{3, 2, "b"},
		{5, 5, "c"},
		{10, 1, "d"},
		{11, 4, "e"},
	}

	wave := streampool.NewWave()
	var got []string
	rs := streampool.NewFnRangeResequencer[string](wave, 0,
		func(_ context.Context, s string, err error) error {
			got = append(got, s)
			return err
		})

	src := streampool.NewLauncher[int](streampool.HandlerFunc[int](
		func(ctx context.Context, i int, _ error) error {
			time.Sleep(time.Duration((i*3)%5) * time.Millisecond) // interleave completions
			s := segs[i]
			return rs.Submit(ctx, s.offset, s.length, s.val)
		}))

	for _, i := range []int{3, 0, 4, 1, 2} { // submit shuffled
		chk.NoError(src.In(wave).Submit(ctx, i))
	}
	chk.NoError(wave.CloseAndSkimAll(ctx))

	chk.Equal([]string{"a", "b", "c", "d", "e"}, got)
}

// TestResequencerStartOffset verifies a resequencer whose sequence numbers begin
// at a non-zero base delivers in order from that base (and would stall if it
// instead waited for 0).
func TestResequencerStartOffset(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	const base = 1000
	const n = 50

	wave := streampool.NewWave()
	var got []int
	rs := streampool.NewFnResequencer[int](wave, base,
		func(_ context.Context, v int, err error) error {
			got = append(got, v)
			return err
		})

	src := streampool.NewLauncher[int](streampool.HandlerFunc[int](
		func(ctx context.Context, k int, _ error) error {
			time.Sleep(time.Duration(k%5) * time.Millisecond)
			return rs.Submit(ctx, uint64(base+k), base+k) //nolint:gosec // G115: base+k is non-negative
		}))

	for k := n - 1; k >= 0; k-- {
		chk.NoError(src.In(wave).Submit(ctx, k))
	}
	chk.NoError(wave.CloseAndSkimAll(ctx))

	chk.Len(got, n)
	for k := 0; k < n; k++ {
		chk.Equal(base+k, got[k], "result %d delivered out of order", k)
	}
}
