// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

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

	_, topMeta := wave.pool.ctxMeta(ctx)
	require.NotNil(t, topMeta)
	require.Equal(t, topLevelContext, topMeta.ctxType)
	assert.Nil(t, topMeta.parent, "root wave context has no parent")

	var skimMeta *ctxMeta
	skimmer := NewFnSkimmer(wave, func(sctx context.Context, _ int, _ error) error {
		_, skimMeta = wave.pool.ctxMeta(sctx)
		return nil
	})

	var bodyMeta, subTopMeta, subBodyMeta *ctxMeta
	launcher := NewTaskLauncher(wave, func(bodyCtx context.Context) error {
		_, bodyMeta = wave.pool.ctxMeta(bodyCtx)

		// Drive a subwave synchronously from inside the body — the
		// telescoping path the suspend brackets rely on.
		subCtx, subWave := NewWave(bodyCtx)
		_, subTopMeta = subWave.pool.ctxMeta(subCtx)
		subLauncher := NewTaskLauncher(subWave, func(subBodyCtx context.Context) error {
			_, subBodyMeta = subWave.pool.ctxMeta(subBodyCtx)
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

func TestFunnelWorkerContextIsFreshPermitRoot(t *testing.T) {
	ctx, wave := NewWave(context.Background())
	defer wave.CancelAndWait()

	fp := NewFunnelPool(wave.Pool())
	var funnelMeta *ctxMeta
	f := NewFnFunnel(fp, func() Accumulator[int] {
		return FuncAccumulator[int]{
			AccumulateFn: func(fctx context.Context, _ int, _ error) (time.Time, error) {
				_, funnelMeta = wave.pool.ctxMeta(fctx)
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
