// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentHeldRequest_Walk(t *testing.T) {
	_, s := newTestSemaphore(t, 2)
	rOuter := s.newRequest(nil)
	rInner := s.newRequest(nil)

	require.Nil(t, (&ctxMeta{}).currentHeldRequest())

	root := &ctxMeta{heldRequest: rOuter}
	mid := &ctxMeta{parent: root}
	leaf := &ctxMeta{parent: mid}
	require.Same(t, rOuter, leaf.currentHeldRequest(),
		"walk must reach a handle stamped at the chain root")
	require.Same(t, rOuter, root.currentHeldRequest())

	// Stop-at-first: with a (hypothetical future) inner stamp, the
	// innermost handle wins — the outer one belongs to an enclosing,
	// already-suspended episode.
	mid2 := &ctxMeta{parent: root, heldRequest: rInner}
	leaf2 := &ctxMeta{parent: mid2}
	require.Same(t, rInner, leaf2.currentHeldRequest(),
		"walk must stop at the first (innermost) stamped handle")
}

// TestPermitScopingChains pins the parent-link topology through real
// dispatch flows: derivations chain (top-level→skim, body→subwave
// top-level), and worker contexts are fresh permit-roots even when the
// pool's base ctx carries a foreign pool's meta.
func TestPermitScopingChains(t *testing.T) {
	ctx := context.Background()
	var wave Wave

	// Mint (or fetch) the wave's top-level meta from ctx. A zero-value Wave
	// self-initializes on this first topLevelCtxMeta call; ctxMeta below then
	// reads back the meta now stamped on ctx.
	ctx, _ = wave.topLevelCtxMeta(ctx, func(contextType) {})
	_, topMeta := wave.ctxMeta(ctx)
	require.NotNil(t, topMeta)
	require.Equal(t, topLevelContext, topMeta.ctxType)
	assert.Nil(t, topMeta.parent, "root wave context has no parent")

	// Body/funnel/skim metas are pooled (bodyMetaPool / derived) and recycled when
	// their work frees, so capture the chain properties DURING execution — a pointer
	// held past drain reads a recycled (or reused) meta. Identity comparisons
	// (parent links) are likewise evaluated while both ends are live.
	var (
		skimSeen, bodySeen, subBodySeen                                      bool
		skimCtxType, bodyCtxType, subTopCtxType, subBodyCtxType              contextType
		skimParentIsTop, bodyParentNil, subTopParentIsBody, subBodyParentNil bool
		subTopNoHeld                                                         bool
	)

	skimmer := NewFnSkimmer(func(sctx context.Context, _ int, _ error) error {
		_, skimMeta := wave.ctxMeta(sctx)
		skimSeen = true
		skimCtxType = skimMeta.ctxType
		skimParentIsTop = skimMeta.parent == topMeta
		return nil
	})

	launcher := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := wave.ctxMeta(bodyCtx)
		bodySeen = true
		bodyCtxType = bodyMeta.ctxType
		bodyParentNil = bodyMeta.parent == nil

		// Drive a subwave synchronously from inside the body — the
		// telescoping path the suspend brackets rely on. A zero-value
		// subWave mints its top-level meta on first dispatch/skim into it;
		// topLevelCtxMeta is that chokepoint. Driving it from bodyCtx (which
		// carries the body's meta of a DIFFERENT wave) makes ensureCtxMeta
		// record the body meta as parent — the body→subwave chaining the
		// assertions below pin.
		var subWave Wave
		subCtx, subTopMeta := subWave.topLevelCtxMeta(bodyCtx, func(contextType) {})
		subTopCtxType = subTopMeta.ctxType
		subTopParentIsBody = subTopMeta.parent == bodyMeta
		// The chain a subwave parking point would walk: from the subjob's
		// top-level meta up to the body — where a handle will be stamped
		// (task #3) — and no further handle beyond it.
		subTopNoHeld = subTopMeta.currentHeldRequest() == nil
		subLauncher := NewTaskLauncher(func(subBodyCtx context.Context) error {
			_, subBodyMeta := subWave.ctxMeta(subBodyCtx)
			subBodySeen = true
			subBodyCtxType = subBodyMeta.ctxType
			subBodyParentNil = subBodyMeta.parent == nil
			return nil
		})
		if err := subLauncher.In(&subWave).Start(subCtx); err != nil {
			return err
		}
		if err := subWave.CloseAndSkimAll(subCtx); err != nil {
			return err
		}

		return skimmer.Submit(bodyCtx, 1)
	})

	require.NoError(t, launcher.In(&wave).Start(ctx))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.True(t, bodySeen)
	assert.Equal(t, taskContext, bodyCtxType)
	assert.True(t, bodyParentNil, "task worker context must be a fresh permit-root")

	assert.Equal(t, topLevelContext, subTopCtxType)
	assert.True(t, subTopParentIsBody, "body→subwave derivation must chain parent to the body's meta")
	assert.True(t, subTopNoHeld, "no handle stamped yet anywhere on the chain")

	require.True(t, subBodySeen)
	assert.Equal(t, taskContext, subBodyCtxType)
	assert.True(t, subBodyParentNil,
		"subjob worker must be fresh-rooted even though the subjob's base ctx carries the parent body's meta")

	require.True(t, skimSeen)
	assert.Equal(t, skimContext, skimCtxType)
	assert.True(t, skimParentIsTop, "top-level→skim derivation must chain parent")
}

// TestHeldRequestStampedDuringBodies pins the #2+#3 end-to-end property:
// a limited body's request handle is stamped on the worker meta, and a
// subwave context inside that body finds the SAME handle via the parent
// walk — exactly what the suspend brackets (#4) will rely on.
func TestHeldRequestStampedDuringBodies(t *testing.T) {
	ctx := context.Background()
	var wave Wave

	var bodyReq, subwaveSeenReq request
	limited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := wave.ctxMeta(bodyCtx)
		bodyReq = bodyMeta.currentHeldRequest()

		// A zero-value subWave mints its top-level meta on first
		// dispatch/skim; topLevelCtxMeta is that chokepoint and chains the
		// derived meta's parent to bodyCtx's (cross-wave) body meta, so the
		// subwave context finds the body's held handle via the parent walk.
		var subWave Wave
		subCtx, subTopMeta := subWave.topLevelCtxMeta(bodyCtx, func(contextType) {})
		subwaveSeenReq = subTopMeta.currentHeldRequest()
		return subWave.CloseAndSkimAll(subCtx)
	}, WithLimits(NewSemaphore(1)))
	require.NoError(t, limited.In(&wave).Start(ctx))

	var unlimitedReq request = &directRequest{} // sentinel, overwritten
	unlimited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := wave.ctxMeta(bodyCtx)
		unlimitedReq = bodyMeta.currentHeldRequest()
		return nil
	})
	require.NoError(t, unlimited.In(&wave).Start(ctx))

	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.NotNil(t, bodyReq, "limited body must see its stamped handle")
	require.Same(t, bodyReq, subwaveSeenReq,
		"a subwave context inside the body must find the body's handle via the parent walk")
	require.Nil(t, unlimitedReq, "unlimited body must see no handle")

	var funnelReq request
	ctx2 := context.Background()
	var wave2 Wave
	fp := &wave2
	f := NewFnFunnel(fp, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, m := wave2.ctxMeta(fctx)
				funnelReq = m.currentHeldRequest()
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	}, WithLimits(NewSemaphore(1)))
	require.NoError(t, f.Submit(ctx2, 1))
	require.NoError(t, wave2.CloseAndSkimAll(ctx2))
	require.NotNil(t, funnelReq, "limited Accumulate body must see its stamped handle")
}

func TestFunnelWorkerContextIsFreshPermitRoot(t *testing.T) {
	ctx := context.Background()
	var wave Wave

	fp := &wave
	// The funnel worker meta is pooled (bodyMetaPool) and recycled when the work is
	// freed, so capture its properties DURING the body, not via a pointer held past
	// drain.
	var funnelSeen bool
	var funnelCtxType contextType
	var funnelParentNil bool
	f := NewFnFunnel(fp, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, funnelMeta := wave.ctxMeta(fctx)
				funnelSeen = true
				funnelCtxType = funnelMeta.ctxType
				funnelParentNil = funnelMeta.parent == nil
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	})

	require.NoError(t, f.Submit(ctx, 1))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.True(t, funnelSeen)
	assert.Equal(t, funnelContext, funnelCtxType)
	assert.True(t, funnelParentNil,
		"funnel worker context must be a fresh permit-root")
}
