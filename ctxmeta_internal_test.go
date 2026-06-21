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
// dispatch flows: derivations chain (top-level→skim, body→NewWave→subwave
// top-level), and worker contexts are fresh permit-roots even when the
// pool's base ctx carries a foreign pool's meta.
func TestPermitScopingChains(t *testing.T) {
	ctx, wave := NewWave(context.Background())
	defer wave.CancelAndWait()

	_, topMeta := wave.ctxMeta(ctx)
	require.NotNil(t, topMeta)
	require.Equal(t, topLevelContext, topMeta.ctxType)
	assert.Nil(t, topMeta.parent, "root wave context has no parent")

	var skimMeta *ctxMeta
	skimmer := NewFnSkimmer(func(sctx context.Context, _ int, _ error) error {
		_, skimMeta = wave.ctxMeta(sctx)
		return nil
	})

	var bodyMeta, subTopMeta, subBodyMeta *ctxMeta
	launcher := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta = wave.ctxMeta(bodyCtx)

		// Drive a subwave synchronously from inside the body — the
		// telescoping path the suspend brackets rely on.
		subCtx, subWave := NewWave(bodyCtx)
		_, subTopMeta = subWave.ctxMeta(subCtx)
		subLauncher := NewTaskLauncher(func(subBodyCtx context.Context) error {
			_, subBodyMeta = subWave.ctxMeta(subBodyCtx)
			return nil
		})
		if err := subLauncher.Start(subCtx); err != nil {
			return err
		}
		if err := subWave.CloseAndSkimAll(subCtx); err != nil {
			return err
		}

		return skimmer.Submit(bodyCtx, 1)
	})

	require.NoError(t, launcher.Start(ctx))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.NotNil(t, bodyMeta)
	assert.Equal(t, taskContext, bodyMeta.ctxType)
	assert.Nil(t, bodyMeta.parent,
		"task worker context must be a fresh permit-root")

	require.NotNil(t, subTopMeta)
	assert.Equal(t, topLevelContext, subTopMeta.ctxType)
	assert.Same(t, bodyMeta, subTopMeta.parent,
		"body→NewWave derivation must chain parent to the body's meta")

	require.NotNil(t, subBodyMeta)
	assert.Equal(t, taskContext, subBodyMeta.ctxType)
	assert.Nil(t, subBodyMeta.parent,
		"subjob worker must be fresh-rooted even though the subjob's base ctx carries the parent body's meta")

	require.NotNil(t, skimMeta)
	assert.Equal(t, skimContext, skimMeta.ctxType)
	assert.Same(t, topMeta, skimMeta.parent,
		"top-level→skim derivation must chain parent")

	// The chain a subwave parking point would walk: from the subjob's
	// top-level meta up to the body — where a handle will be stamped
	// (task #3) — and no further handle beyond it.
	assert.Nil(t, subTopMeta.currentHeldRequest(),
		"no handle stamped yet anywhere on the chain")
}

// TestHeldRequestStampedDuringBodies pins the #2+#3 end-to-end property:
// a limited body's request handle is stamped on the worker meta, and a
// subwave context inside that body finds the SAME handle via the parent
// walk — exactly what the suspend brackets (#4) will rely on.
func TestHeldRequestStampedDuringBodies(t *testing.T) {
	ctx, wave := NewWave(context.Background())
	defer wave.CancelAndWait()

	var bodyReq, subwaveSeenReq request
	limited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := wave.ctxMeta(bodyCtx)
		bodyReq = bodyMeta.currentHeldRequest()

		subCtx, subWave := NewWave(bodyCtx)
		_, subTopMeta := subWave.ctxMeta(subCtx)
		subwaveSeenReq = subTopMeta.currentHeldRequest()
		return subWave.CloseAndSkimAll(subCtx)
	}, WithLimits(NewSemaphore(1)))
	require.NoError(t, limited.Start(ctx))

	var unlimitedReq request = &directRequest{} // sentinel, overwritten
	unlimited := NewTaskLauncher(func(bodyCtx context.Context) error {
		_, bodyMeta := wave.ctxMeta(bodyCtx)
		unlimitedReq = bodyMeta.currentHeldRequest()
		return nil
	})
	require.NoError(t, unlimited.Start(ctx))

	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.NotNil(t, bodyReq, "limited body must see its stamped handle")
	require.Same(t, bodyReq, subwaveSeenReq,
		"a subwave context inside the body must find the body's handle via the parent walk")
	require.Nil(t, unlimitedReq, "unlimited body must see no handle")

	var funnelReq request
	ctx2, wave2 := NewWave(context.Background())
	defer wave2.CancelAndWait()
	fp := wave2
	f := NewFnFunnel(fp, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, m := wave2.ctxMeta(fctx)
				funnelReq = m.currentHeldRequest()
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	}, nil, WithLimits(NewSemaphore(1)))
	defer f.Close()
	require.NoError(t, f.Submit(ctx2, 1))
	require.NoError(t, wave2.CloseAndSkimAll(ctx2))
	require.NotNil(t, funnelReq, "limited Accumulate body must see its stamped handle")
}

func TestFunnelWorkerContextIsFreshPermitRoot(t *testing.T) {
	ctx, wave := NewWave(context.Background())
	defer wave.CancelAndWait()

	fp := wave
	var funnelMeta *ctxMeta
	f := NewFnFunnel(fp, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, funnelMeta = wave.ctxMeta(fctx)
				return time.Time{}, nil
			},
			FlushFn: func(context.Context) error { return nil },
		}
	}, nil)
	defer f.Close()

	require.NoError(t, f.Submit(ctx, 1))
	require.NoError(t, wave.CloseAndSkimAll(ctx))

	require.NotNil(t, funnelMeta)
	assert.Equal(t, funnelContext, funnelMeta.ctxType)
	assert.Nil(t, funnelMeta.parent,
		"funnel worker context must be a fresh permit-root")
}
