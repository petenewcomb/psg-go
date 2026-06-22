// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// borrowing on an empty pool mints a fresh shell; returning it makes the next
// borrow reuse the very same shell (and its stable &meta address).
func TestBodyCtxPool_MintAndReuse(t *testing.T) {
	var p bodyCtxPool
	p.Init(context.Background(), nil, nil)

	wave := &Wave{}
	ee := &topLevelExEnv{}

	m1 := p.borrow(wave, taskContext, nil, nil, ee)
	require.NotNil(t, m1)
	require.Same(t, m1, m1.ctx.Value(ctxMetaValueKey{}).(*ctxMeta),
		"the body ctx must carry its own meta by address")
	require.Same(t, &p, m1.pool, "a pool-minted meta must carry its pool back-pointer")

	p.release(m1)
	m2 := p.borrow(wave, taskContext, nil, nil, ee)
	require.Same(t, m1, m2, "a returned meta must be reused, not re-minted")
}

// borrow fully (re-)stamps the meta's transient fields; giveBack clears the ones
// that would otherwise pin resources between borrows.
func TestBodyCtxPool_StampAndClear(t *testing.T) {
	var p bodyCtxPool
	p.Init(context.Background(), nil, nil)

	wave := &Wave{}
	ee := &topLevelExEnv{}
	parent := &ctxMeta{}

	m := p.borrow(wave, funnelContext, parent, nil, ee)
	assert.Same(t, wave, m.job)
	assert.Same(t, wave, m.wave)
	assert.Equal(t, funnelContext, m.ctxType)
	assert.Same(t, parent, m.parent)
	assert.Same(t, ee, m.executionEnvironment)

	p.release(m)
	assert.Nil(t, m.executionEnvironment, "release must clear the execution environment")
	assert.Nil(t, m.heldRequest, "release must clear the held request")
	assert.Nil(t, m.parent, "release must clear the parent link")
}

// the body ctx is a plain descendant of the source ctx, so cancelling the source
// cancels the body — the whole cancellation model, by ancestry, with no waveCtx.
func TestBodyCtxPool_CancellationByAncestry(t *testing.T) {
	srcCtx, cancel := context.WithCancel(context.Background())
	var p bodyCtxPool
	p.Init(srcCtx, nil, nil)

	m := p.borrow(&Wave{}, taskContext, nil, nil, &topLevelExEnv{})
	select {
	case <-m.ctx.Done():
		t.Fatal("body ctx should not be done before the source ctx is cancelled")
	default:
	}

	cancel()
	<-m.ctx.Done()
	require.ErrorIs(t, m.ctx.Err(), context.Canceled)
}

// parentJobsFor mirrors ensureCtxMeta's cross-wave accumulation.
func TestBodyCtxPool_ParentJobsFor(t *testing.T) {
	// NOTE: assert.Contains on a map[*Wave]struct{} is unusable here — testify
	// compares via reflect.DeepEqual, which dereferences pointers, so distinct
	// zero-valued &Wave{} keys all compare equal. Use direct (pointer-identity)
	// map indexing instead.
	has := func(m map[*Wave]struct{}, w *Wave) bool { _, ok := m[w]; return ok }

	t.Run("no source wave passes through (top-level user ctx)", func(t *testing.T) {
		var p bodyCtxPool
		p.Init(context.Background(), nil, nil)
		assert.Nil(t, p.parentJobsFor(&Wave{}))
	})

	t.Run("same target wave passes the source parentJobs through unchanged", func(t *testing.T) {
		srcJob := &Wave{}
		ancestor := &Wave{}
		basis := map[*Wave]struct{}{ancestor: {}}
		var p bodyCtxPool
		p.Init(context.Background(), srcJob, basis)

		got := p.parentJobsFor(srcJob)
		assert.True(t, has(got, ancestor))
		assert.False(t, has(got, srcJob), "same-wave borrow must not add the source wave")
		assert.Len(t, got, 1)
	})

	t.Run("cross target wave joins the source wave to its parentJobs", func(t *testing.T) {
		srcJob := &Wave{}
		ancestor := &Wave{}
		target := &Wave{}
		basis := map[*Wave]struct{}{ancestor: {}}
		var p bodyCtxPool
		p.Init(context.Background(), srcJob, basis)

		got := p.parentJobsFor(target)
		assert.True(t, has(got, ancestor), "the source's own ancestry must carry over")
		assert.True(t, has(got, srcJob), "the source wave itself must join the ancestry")
		assert.False(t, has(basis, srcJob), "the basis map must not be mutated")
		assert.Len(t, basis, 1)
	})
}
