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

func TestCurrentHeldPermit_Walk(t *testing.T) {
	hOuter := &heldPermit{}
	hInner := &heldPermit{}

	require.Nil(t, (&ctxMeta{}).currentHeldPermit())

	root := &ctxMeta{held: hOuter}
	mid := &ctxMeta{parent: root}
	leaf := &ctxMeta{parent: mid}
	require.Same(t, hOuter, leaf.currentHeldPermit(),
		"walk must reach a handle stamped at the chain root")
	require.Same(t, hOuter, root.currentHeldPermit())

	// Stop-at-first: an inner stamp wins — the outer one belongs to an enclosing,
	// already-suspended episode.
	mid2 := &ctxMeta{parent: root, held: hInner}
	leaf2 := &ctxMeta{parent: mid2}
	require.Same(t, hInner, leaf2.currentHeldPermit(),
		"walk must stop at the first (innermost) stamped handle")
}

// TestPermitScopingChains pins the parent-link topology through real
// dispatch flows: derivations chain (top-level→skim, body→subwave
// top-level), and worker contexts are fresh permit-roots even when the
// pool's base ctx carries a foreign pool's meta.
func TestPermitScopingChains(t *testing.T) {
	ctx := context.Background()
	wave := NewWave()

	// Mint (or fetch) the wave's top-level meta from ctx. A zero-value Wave
	// self-initializes on this first topLevelCtxMeta call; ctxMeta below then
	// reads back the meta now stamped on ctx.
	ctx, _, _ = waveImplOf(wave).topLevelCtxMeta(ctx, func(contextType) {})
	_, topMeta := waveImplOf(wave).ctxMeta(ctx)
	require.NotNil(t, topMeta)
	require.Equal(t, topLevelContext, topMeta.ctxType)
	assert.Nil(t, topMeta.parent, "root wave context has no parent")

	// Body/funnel/skim metas are pooled (bodyMetaPool / derived) and recycled when
	// their work frees, so capture the chain properties DURING execution — a pointer
	// held past drain reads a recycled (or reused) meta. Identity comparisons
	// (parent links) are likewise evaluated while both ends are live.
	var (
		skimSeen, bodySeen, subBodySeen                                        bool
		skimCtxType, bodyCtxType, subTopCtxType, subBodyCtxType                contextType
		skimParentIsTop, bodyPermitRoot, subTopParentIsBody, subBodyPermitRoot bool
		subTopNoHeld                                                           bool
	)

	skimmer := NewFnSkimmer(func(sctx context.Context, _ int, _ error) error {
		// The handler runs under a PER-ITEM child of the drive's skim meta
		// (docs/decisions/driver-contexts.md): child → drive skim meta →
		// top-level meta, all one synchronous extent (no permitRoot between).
		_, skimMeta := waveImplOf(wave).ctxMeta(sctx)
		skimSeen = true
		skimCtxType = skimMeta.ctxType
		skimParentIsTop = skimMeta.syncParent() != nil &&
			skimMeta.syncParent().ctxType == skimContext &&
			skimMeta.syncParent().syncParent() == topMeta
		return nil
	})

	launcher := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := waveImplOf(wave).ctxMeta(bodyCtx)
		bodySeen = true
		bodyCtxType = bodyMeta.ctxType
		bodyPermitRoot = bodyMeta.permitRoot && bodyMeta.syncParent() == nil

		// Drive a subwave synchronously from inside the body — the
		// telescoping path the suspend brackets rely on. A zero-value
		// subWave mints its top-level meta on first dispatch/skim into it;
		// topLevelCtxMeta is that chokepoint. Driving it from bodyCtx (which
		// carries the body's meta of a DIFFERENT wave) makes ensureCtxMeta
		// record the body meta as parent — the body→subwave chaining the
		// assertions below pin.
		subWave := NewWave()
		subCtx, subTopMeta, _ := waveImplOf(subWave).topLevelCtxMeta(bodyCtx, func(contextType) {})
		subTopCtxType = subTopMeta.ctxType
		subTopParentIsBody = subTopMeta.parent == bodyMeta
		// The chain a subwave parking point would walk: from the subwave's
		// top-level meta up to the body. This launcher is unlimited, so no handle
		// is stamped anywhere on the chain.
		subTopNoHeld = subTopMeta.currentHeldPermit() == nil
		subLauncher := NewTaskLauncher(func(subBodyCtx context.Context) error {
			_, subBodyMeta := waveImplOf(subWave).ctxMeta(subBodyCtx)
			subBodySeen = true
			subBodyCtxType = subBodyMeta.ctxType
			subBodyPermitRoot = subBodyMeta.permitRoot && subBodyMeta.syncParent() == nil
			return nil
		})
		if err := subLauncher.In(subWave).Start(subCtx); err != nil {
			return err
		}
		if err := subWave.CloseAndSkimAll(subCtx); err != nil {
			return err
		}

		return skimmer.Submit(bodyCtx, 1)
	})

	require.NoError(t, launcher.In(wave).Start(ctx))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.True(t, bodySeen)
	assert.Equal(t, taskContext, bodyCtxType)
	assert.True(t, bodyPermitRoot, "task worker context must be a fresh permit-root")

	assert.Equal(t, topLevelContext, subTopCtxType)
	assert.True(t, subTopParentIsBody, "body→subwave derivation must chain parent to the body's meta")
	assert.True(t, subTopNoHeld, "no handle stamped yet anywhere on the chain")

	require.True(t, subBodySeen)
	assert.Equal(t, taskContext, subBodyCtxType)
	assert.True(t, subBodyPermitRoot,
		"subwave worker must be fresh-rooted even though the subwave's base ctx carries the parent body's meta")

	require.True(t, skimSeen)
	assert.Equal(t, skimContext, skimCtxType)
	assert.True(t, skimParentIsTop,
		"per-item skim meta must chain synchronously through the drive skim meta to the top-level meta")
}

// TestHeldPermitStampedDuringBodies pins the end-to-end stamp+walk property: a limited
// body's permit handle is stamped on the worker meta, and a subwave context inside that
// body finds the SAME handle via the parent walk — exactly what the suspend brackets
// rely on.
func TestHeldPermitStampedDuringBodies(t *testing.T) {
	ctx := context.Background()
	wave := NewWave()

	var bodyHeld, subwaveSeenHeld *heldPermit
	limited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := waveImplOf(wave).ctxMeta(bodyCtx)
		bodyHeld = bodyMeta.currentHeldPermit()

		// A zero-value subWave mints its top-level meta on first
		// dispatch/skim; topLevelCtxMeta is that chokepoint and chains the
		// derived meta's parent to bodyCtx's (cross-wave) body meta, so the
		// subwave context finds the body's held handle via the parent walk.
		subWave := NewWave()
		subCtx, subTopMeta, _ := waveImplOf(subWave).topLevelCtxMeta(bodyCtx, func(contextType) {})
		subwaveSeenHeld = subTopMeta.currentHeldPermit()
		return subWave.CloseAndSkimAll(subCtx)
	}).WithLimits(NewSemaphore(1))
	require.NoError(t, limited.In(wave).Start(ctx))

	var unlimitedHeld = &heldPermit{} // sentinel, overwritten
	unlimited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := waveImplOf(wave).ctxMeta(bodyCtx)
		unlimitedHeld = bodyMeta.currentHeldPermit()
		return nil
	})
	require.NoError(t, unlimited.In(wave).Start(ctx))

	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.NotNil(t, bodyHeld, "limited body must see its stamped handle")
	require.Same(t, bodyHeld, subwaveSeenHeld,
		"a subwave context inside the body must find the body's handle via the parent walk")
	require.Nil(t, unlimitedHeld, "unlimited body must see no handle")

	var funnelHeld *heldPermit
	ctx2 := context.Background()
	wave2 := NewWave()
	f := NewFnFunnel(wave2, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, m := waveImplOf(wave2).ctxMeta(fctx)
				funnelHeld = m.currentHeldPermit()
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	}).WithLimits(NewSemaphore(1))
	require.NoError(t, f.Submit(ctx2, 1))
	require.NoError(t, wave2.CloseAndSkimAll(ctx2))
	require.NotNil(t, funnelHeld, "limited Accumulate body must see its stamped handle")
}

func TestFunnelWorkerContextIsFreshPermitRoot(t *testing.T) {
	ctx := context.Background()
	wave := NewWave()

	// The funnel worker meta is pooled (bodyMetaPool) and recycled when the work is
	// freed, so capture its properties DURING the body, not via a pointer held past
	// drain.
	var funnelSeen bool
	var funnelCtxType contextType
	var funnelPermitRoot bool
	f := NewFnFunnel(wave, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, funnelMeta := waveImplOf(wave).ctxMeta(fctx)
				funnelSeen = true
				funnelCtxType = funnelMeta.ctxType
				funnelPermitRoot = funnelMeta.permitRoot && funnelMeta.syncParent() == nil
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	})

	require.NoError(t, f.Submit(ctx, 1))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.True(t, funnelSeen)
	assert.Equal(t, funnelContext, funnelCtxType)
	assert.True(t, funnelPermitRoot,
		"funnel worker context must be a fresh permit-root")
}
