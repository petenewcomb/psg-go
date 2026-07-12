// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The straddle tests pin the two orderings of the recycle CAS against a
// concurrent weak-to-strong upgrade, deterministically.

// TestStraddleUpgradeBeforeRecycle: the upgrade's increment lands first, so the
// object is no longer at one reference and the subsequent release does NOT
// recycle — the generation is untouched and the handle stays valid.
func TestStraddleUpgradeBeforeRecycle(t *testing.T) {
	p := For[mo]()
	obj := p.Get()
	_, g := word(obj)

	h := NewHandle(obj)

	got, ok := h.Get() // upgrade lands: refs 1 -> 2
	require.True(t, ok)
	require.Equal(t, obj, got)
	refs, gen := word(obj)
	assert.Equal(t, uint64(2), refs)
	assert.Equal(t, g, gen)

	p.Release(obj) // refs 2 -> 1, no recycle
	refs, gen = word(obj)
	assert.Equal(t, uint64(1), refs)
	assert.Equal(t, g, gen, "generation must not bump while a reference remains")

	_, ok = h.Get() // still the same incarnation
	assert.True(t, ok)

	p.Release(obj) // 2 -> 1
	p.Release(obj) // 1 -> 0, recycle
	_, gen = word(obj)
	assert.Equal(t, g+1, gen)
}

// TestStraddleRecycleBeforeUpgrade: the recycle lands first, bumping the
// generation; the now-stale upgrade fails and touches nothing.
func TestStraddleRecycleBeforeUpgrade(t *testing.T) {
	p := For[mo]()
	obj := p.Get()
	_, g := word(obj)

	h := NewHandle(obj)

	p.Release(obj) // refs 1 -> 0, recycle: (0, g+1)
	refs, gen := word(obj)
	require.Equal(t, uint64(0), refs)
	require.Equal(t, g+1, gen)

	_, ok := h.Get() // stale: generation no longer matches
	assert.False(t, ok)
	refs, gen = word(obj)
	assert.Equal(t, uint64(0), refs, "failed upgrade must not touch refs")
	assert.Equal(t, g+1, gen)
}

// TestStraddleRecycleBetweenLoadAndCAS reproduces the exact mid-flight
// interleaving: an upgrade reads the word, then a recycle happens in the gap
// before its CAS. The stale CAS must fail, and the retrying Handle.Get must then
// observe the bumped generation and report failure — no resurrection.
func TestStraddleRecycleBetweenLoadAndCAS(t *testing.T) {
	p := For[mo]()
	obj := p.Get()
	h := NewHandle(obj)

	// The load Handle.Get would perform first: it observes (1, g), gen matches.
	loaded := obj.w.Load()
	require.Equal(t, h.gen, loaded[genWord])
	require.Equal(t, uint64(1), loaded[refsWord])

	// A releaser recycles in the gap between that load and the CAS.
	p.Release(obj) // (1, g) -> (0, g+1)

	// The upgrade's CAS, built from the now-stale load, must fail.
	staleCAS := obj.w.CompareAndSwap(loaded,
		[2]uint64{loaded[refsWord] + 1, loaded[genWord]})
	assert.False(t, staleCAS, "an upgrade CAS straddling a recycle must fail")

	// The full retry-looped Handle.Get reloads, sees the bumped generation, and
	// fails cleanly.
	_, ok := h.Get()
	assert.False(t, ok)

	w := obj.w.Load()
	assert.Equal(t, uint64(0), w[refsWord])
	assert.Equal(t, h.gen+1, w[genWord])
}
