// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// borrowBodyContext stamps a wave-bound meta onto a child ctx descended from the
// source ctx; metaFromContext resolves it, and the call-specific fields are set.
func TestBorrowBodyContext_StampsMeta(t *testing.T) {
	src := context.Background()
	wave := &Wave{}
	ee := &topLevelExEnv{}

	ctx, m := borrowBodyContext(src, wave, funnelContext, nil, ee)
	require.NotNil(t, m)

	got, ok := metaFromContext(ctx)
	require.True(t, ok)
	require.Same(t, m, got, "metaFromContext must resolve the borrowed meta")
	assert.Same(t, wave, m.wave)
	assert.Equal(t, funnelContext, m.ctxType)
	assert.Nil(t, m.parent, "a borrowed body is always a fresh permit-root")
	assert.Same(t, ee, m.executionEnvironment)

	releaseBodyContext(ctx)
}

// The child ctx object is reused across borrow/release from the same source ctx
// (ctxpool's job); the meta is re-stamped fresh each borrow.
func TestBorrowBodyContext_ReusesChildCtx(t *testing.T) {
	src := context.Background()
	w1 := &Wave{}
	w2 := &Wave{}

	ctx1, _ := borrowBodyContext(src, w1, taskContext, nil, &topLevelExEnv{})
	releaseBodyContext(ctx1)
	ctx2, m2 := borrowBodyContext(src, w2, taskContext, nil, &topLevelExEnv{})
	require.Same(t, ctx1, ctx2, "the child ctx must be reused across borrows of one source")
	assert.Same(t, w2, m2.wave, "the reused child must carry the freshly stamped wave")
	releaseBodyContext(ctx2)
}

// Cancellation rides ancestry: cancelling the source ctx cancels the body ctx, with
// no waveCtx in the path.
func TestBorrowBodyContext_CancellationByAncestry(t *testing.T) {
	src, cancel := context.WithCancel(context.Background())
	ctx, _ := borrowBodyContext(src, &Wave{}, taskContext, nil, &topLevelExEnv{})
	select {
	case <-ctx.Done():
		t.Fatal("body ctx should not be done before the source ctx is cancelled")
	default:
	}
	cancel()
	<-ctx.Done()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestParentJobsForSource(t *testing.T) {
	has := func(m map[*Wave]struct{}, w *Wave) bool { _, ok := m[w]; return ok }

	t.Run("top-level source (no meta) carries none", func(t *testing.T) {
		srcMeta, ok := metaFromContext(context.Background())
		assert.Nil(t, parentWavesForSource(srcMeta, ok, &Wave{}))
	})

	t.Run("same-wave source passes parentWaves through", func(t *testing.T) {
		srcWave := &Wave{}
		ancestor := &Wave{}
		// A source ctx whose meta is bound to srcWave with one ancestor.
		srcCtx, _ := borrowBodyContext(context.Background(), srcWave, taskContext, nil, &topLevelExEnv{})
		m, _ := metaFromContext(srcCtx)
		m.parentWaves = map[*Wave]struct{}{ancestor: {}}

		got := parentWavesForSource(m, true, srcWave)
		assert.True(t, has(got, ancestor))
		assert.False(t, has(got, srcWave), "same-wave must not add the source wave")
		releaseBodyContext(srcCtx)
	})

	t.Run("cross-wave source joins its wave into parentWaves", func(t *testing.T) {
		srcWave := &Wave{}
		target := &Wave{}
		ancestor := &Wave{}
		srcCtx, _ := borrowBodyContext(context.Background(), srcWave, taskContext, nil, &topLevelExEnv{})
		m, _ := metaFromContext(srcCtx)
		m.parentWaves = map[*Wave]struct{}{ancestor: {}}

		got := parentWavesForSource(m, true, target)
		assert.True(t, has(got, ancestor), "the source's own ancestry carries over")
		assert.True(t, has(got, srcWave), "the source wave joins the ancestry")
		assert.False(t, has(m.parentWaves, srcWave), "the source meta's map must not be mutated")
		releaseBodyContext(srcCtx)
	})
}
