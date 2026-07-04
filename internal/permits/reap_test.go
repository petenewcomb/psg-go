// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// destroy removes a cache from its list exactly (the intrusive DLL supports interior
// removal), so a long-lived parent that churns children does not accumulate dead
// entries — the list returns to empty once the children are drained. (The lock-free
// nbcq port could not remove interior nodes and needed lazy reaping; the DLL does not.)
func TestDestroyRemovesChildExactly(t *testing.T) {
	const iters = 1000
	tp := newTestPool(8)

	root := tp.NewCache()
	dr := NewDemand()
	ds := NewDemand()
	rp, _ := root.Acquire(dr, 1)
	rp.Release()

	for range iters {
		sub := root.NewChild() // ephemeral
		pm, err := sub.Acquire(ds, 1)
		require.NoError(t, err)
		require.True(t, pm.Held())
		pm.Release()
		require.True(t, sub.ReleaseRef()) // destroy → unlinked from root.children
	}

	require.Equal(t, 0, root.childrenLen(), "destroyed children must be removed, not accumulated")
}

// Same churn at the Pool-roots level.
func TestDestroyRemovesRootExactly(t *testing.T) {
	const iters = 1000
	tp := newTestPool(8)

	d := NewDemand()
	for range iters {
		c := tp.Pool.NewCache()
		pm, err := c.Acquire(d, 1)
		require.NoError(t, err)
		require.True(t, pm.Held())
		pm.Release()
		require.True(t, c.ReleaseRef())
	}

	require.Equal(t, 0, tp.rootsLen(), "destroyed roots must be removed, not accumulated")
}

// A live cache must survive churn of dead siblings around it (exact removal must touch
// only the destroyed cache's links).
func TestDestroyKeepsLiveSibling(t *testing.T) {
	tp := newTestPool(8)

	root := tp.NewCache()
	dr := NewDemand()
	rp, _ := root.Acquire(dr, 1)
	rp.Release()

	live := tp.newChild(root) // persists across the churn

	for range 100 {
		sub := root.NewChild()
		require.True(t, sub.ReleaseRef())
	}

	require.True(t, root.childrenContains(live), "removing dead siblings must not drop the live child")
	require.Equal(t, 1, root.childrenLen(), "only the live child remains")
}
