// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
)

// funnelEngine returns this Wave's lazily-created funnel engine, building it on
// the first call (double-checked under fEngineMu). The engine is a deliberately
// lazy sub-object — nil until the first NewFunnel — so waves that never funnel
// carry no flush state and spawn no flusher.
func (w *Wave) funnelEngine() *funnelEngine {
	w.ensureArmed() // a zero-value/drained Wave may be funneled before any dispatch/skim
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

// resolveWave returns the op's bound wave if non-nil, otherwise the ambient wave
// attached to ctx (the framework stamps the dispatching wave onto a body's ctx).
// Panics if neither is set — an op constructed with a nil wave (wave-agnostic) must
// be dispatched either via op.In(&wave) or from inside a body whose ctx carries an
// ambient wave.
//
// This is the dispatch-side counterpart to nil-OK construction: op.In(&wave) locks
// dispatch to that wave; a nil-wave op dispatched in-body defers to the ambient
// wave, letting one op instance be reused across many waves.
func resolveWave(opWave *Wave, ctx context.Context) *Wave {
	if opWave != nil {
		return opWave
	}
	meta, ok := metaFromContext(ctx)
	if !ok || meta.wave == nil {
		panic("op constructed with nil wave dispatched without op.In(&wave) and outside any wave body")
	}
	return meta.wave
}
