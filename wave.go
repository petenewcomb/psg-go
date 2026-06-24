// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
)

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
