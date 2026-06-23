// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"fmt"

	"github.com/petenewcomb/streampool/psgopt"
)

// funnelEngine returns this Wave's lazily-created funnel engine, building it on
// the first call (double-checked under fEngineMu). The engine is a deliberately
// lazy sub-object — nil until the first NewFunnel — so waves that never funnel
// carry no flush state and spawn no flusher.
func (w *Wave) funnelEngine() *funnelEngine {
	if fe := w.fEngine.Load(); fe != nil {
		return fe
	}
	w.fEngineMu.Lock()
	defer w.fEngineMu.Unlock()
	if fe := w.fEngine.Load(); fe != nil {
		return fe
	}
	fe := newFunnelEngine(w)
	w.fEngine.Store(fe)
	return fe
}

// WaveOption configures a Wave at construction time.
type WaveOption interface {
	applyToWaveConfig(*waveConfig)
}

type waveConfig struct {
	poolOpts []psgopt.PoolOption
}

// WithPoolOptions forwards construction options to the Wave's substrate.
func WithPoolOptions(opts ...psgopt.PoolOption) WaveOption {
	return withPoolOptionsOption{opts: opts}
}

type withPoolOptionsOption struct {
	opts []psgopt.PoolOption
}

func (o withPoolOptionsOption) applyToWaveConfig(c *waveConfig) {
	c.poolOpts = append(c.poolOpts, o.opts...)
}

// NewWave constructs a Wave and returns it together with a Wave-augmented
// context callers should pass to op dispatches (Start, Submit). The Wave owns
// its substrate over the global worker pool and is torn down by
// [Wave.CancelAndWait].
func NewWave(parent context.Context, opts ...WaveOption) (waveCtx context.Context, wave *Wave) {
	var cfg waveConfig
	for _, opt := range opts {
		opt.applyToWaveConfig(&cfg)
	}

	w := newWaveSubstrate(parent, cfg.poolOpts...)

	// Inject the Wave into the ctxMeta of the returned ctx so op dispatches can
	// find it. topLevelCtxMeta also populates the cached meta's executionEnvironment
	// — otherwise subsequent topLevelCtxMeta calls hit the cache without ever
	// running the updateFn that would set exEnv.
	ctx, meta := w.topLevelCtxMeta(parent, func(ctxType contextType) {
		if ctxType != topLevelContext {
			panic(fmt.Sprintf(
				"NewWave called from %v context but allowed only by top-level context",
				ctxType))
		}
	})
	meta.wave = w

	return ctx, w
}

// resolveWave returns the op's bound wave if non-nil, otherwise looks up the
// wave attached to ctx by [NewWave]. Panics if neither is set — an op
// constructed with nil wave must be dispatched from a ctx that descends from a
// NewWave call.
//
// This is the dispatch-side counterpart to nil-OK construction: constructing
// with a specific *Wave locks dispatch to that wave; constructing with nil
// defers the choice to the dispatching ctx, letting one op instance be reused
// across many waves.
func resolveWave(opWave *Wave, ctx context.Context) *Wave {
	if opWave != nil {
		return opWave
	}
	meta, ok := metaFromContext(ctx)
	if !ok || meta.wave == nil {
		panic("op constructed with nil wave dispatched from a ctx with no wave (call NewWave first)")
	}
	return meta.wave
}
