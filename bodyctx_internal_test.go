// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestWaveImpl mints a fresh, Init'd substrate for tests that exercise the
// internal *waveImpl plumbing directly (borrowBodyContext, parentWavesForSource). It
// carries an owner reference that the test never releases — harmless, the impl is just
// GC'd when the test drops it.
func newTestWaveImpl() *waveImpl { return wavePool.Get() }

// waveImplOf upgrades a live Wave to its substrate for internal-method tests. It leaks
// the upgrade reference (never Released), which is harmless in a test and conveniently
// keeps the impl alive for post-drain assertions.
func waveImplOf(w Wave) *waveImpl { impl, _ := w.h.Get(); return impl }

// borrowBodyContext stamps a wave-bound meta onto a child ctx descended from the
// source ctx; metaFromContext resolves it, and the call-specific fields are set.
func TestBorrowBodyContext_StampsMeta(t *testing.T) {
	src := context.Background()
	wave := newTestWaveImpl()
	ee := &topLevelExEnv{}

	ctx, m := borrowBodyContext(src, nil, wave, funnelContext, nil, ee)
	require.NotNil(t, m)

	got, ok := metaFromContext(ctx)
	require.True(t, ok)
	require.Same(t, m, got, "metaFromContext must resolve the borrowed meta")
	assert.Same(t, wave, m.wave)
	assert.Equal(t, funnelContext, m.ctxType)
	assert.True(t, m.permitRoot, "a borrowed body is always a fresh permit-root")
	assert.Nil(t, m.syncParent(), "synchronous-extent walks must not cross the borrow")
	assert.Same(t, ee, m.executionEnvironment)

	releaseBodyContext(ctx)
}

// The parent link is refcounted: a borrowed body pins its source meta (and so the
// source's ctxpool child) until the body's release, even after the source's own
// owner released it.
func TestBorrowBodyContext_ParentPinnedAcrossSourceRelease(t *testing.T) {
	srcCtx, srcMeta := borrowBodyContext(context.Background(), nil, newTestWaveImpl(), taskContext, nil, &topLevelExEnv{})
	bodyCtx, m := borrowBodyContext(srcCtx, srcMeta, newTestWaveImpl(), funnelContext, nil, &topLevelExEnv{})
	require.Same(t, srcMeta, m.parent)

	// Owner releases the source while the "async" body still holds it.
	releaseBodyContext(srcCtx)
	got, ok := metaFromContext(srcCtx)
	require.True(t, ok)
	require.Same(t, srcMeta, got,
		"the source child ctx must still resolve the pinned meta after its owner release")

	// The body's release drops the pin; only then is the source child freed
	// (its value cleared and the child re-pooled).
	releaseBodyContext(bodyCtx)
	_, ok = metaFromContext(srcCtx)
	require.False(t, ok, "the source child must be freed once the last pin drops")
}

// The child ctx object is reused across borrow/release from the same source ctx
// (ctxpool's job); the meta is re-stamped fresh each borrow.
func TestBorrowBodyContext_ReusesChildCtx(t *testing.T) {
	src := context.Background()
	w1 := newTestWaveImpl()
	w2 := newTestWaveImpl()

	ctx1, _ := borrowBodyContext(src, nil, w1, taskContext, nil, &topLevelExEnv{})
	releaseBodyContext(ctx1)
	ctx2, m2 := borrowBodyContext(src, nil, w2, taskContext, nil, &topLevelExEnv{})
	require.Same(t, ctx1, ctx2, "the child ctx must be reused across borrows of one source")
	assert.Same(t, w2, m2.wave, "the reused child must carry the freshly stamped wave")
	releaseBodyContext(ctx2)
}

// Cancellation rides ancestry: cancelling the source ctx cancels the body ctx, with
// no waveCtx in the path.
func TestBorrowBodyContext_CancellationByAncestry(t *testing.T) {
	src, cancel := context.WithCancel(context.Background())
	ctx, _ := borrowBodyContext(src, nil, newTestWaveImpl(), taskContext, nil, &topLevelExEnv{})
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
	has := func(s *parentWaveSet, w *waveImpl) bool { return s.has(omnipool.NewHandle(w)) }
	// setWith builds a one-entry parentWaveSet the test owns (refs==1).
	setWith := func(ancestor *waveImpl) *parentWaveSet {
		s := parentWaveSetPool.Get()
		s.m[omnipool.NewHandle(ancestor)] = struct{}{}
		return s
	}

	t.Run("top-level source (no meta) carries none", func(t *testing.T) {
		srcMeta, ok := metaFromContext(context.Background())
		assert.Nil(t, parentWavesForSource(srcMeta, ok, newTestWaveImpl()))
	})

	t.Run("same-wave source passes parentWaves through", func(t *testing.T) {
		srcWave := newTestWaveImpl()
		ancestor := newTestWaveImpl()
		// A source ctx whose meta is bound to srcWave with one ancestor.
		srcCtx, _ := borrowBodyContext(context.Background(), nil, srcWave, taskContext, nil, &topLevelExEnv{})
		m, _ := metaFromContext(srcCtx)
		m.parentWaves = setWith(ancestor)

		got := parentWavesForSource(m, true, srcWave)
		assert.True(t, has(got, ancestor))
		assert.False(t, has(got, srcWave), "same-wave must not add the source wave")
		assert.Same(t, m.parentWaves, got, "same-wave shares the source set by pointer")
		releaseParentWaveSet(got)
		releaseBodyContext(srcCtx)
	})

	t.Run("cross-wave source joins its wave into parentWaves", func(t *testing.T) {
		srcWave := newTestWaveImpl()
		target := newTestWaveImpl()
		ancestor := newTestWaveImpl()
		srcCtx, _ := borrowBodyContext(context.Background(), nil, srcWave, taskContext, nil, &topLevelExEnv{})
		m, _ := metaFromContext(srcCtx)
		m.parentWaves = setWith(ancestor)

		got := parentWavesForSource(m, true, target)
		assert.True(t, has(got, ancestor), "the source's own ancestry carries over")
		assert.True(t, has(got, srcWave), "the source wave joins the ancestry")
		assert.False(t, has(m.parentWaves, srcWave), "the source set must not be mutated")
		releaseParentWaveSet(got)
		releaseBodyContext(srcCtx)
	})
}
