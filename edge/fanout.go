// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge

import (
	"context"
	"sync"

	"github.com/petenewcomb/streampool"
)

// FanOut runs fetch over each input concurrently on a child Wave, rate-limited
// by the shared limiter, and returns the collected results. This is the payoff
// of pairing with streampool: an App body (already running on a worker) can
// recursively fan out and block-drain its own sub-wave without deadlock,
// because the pool scales with demand.
//
// The limiter is passed in so it can be SHARED across Apps — and across other
// ops entirely (e.g. a gRPC service in the edgegrpc module) — to express a
// single collective cap on, say, a downstream dependency, no matter which
// transport or endpoint the work arrived through.
func FanOut[In, Out any](
	ctx context.Context,
	limit streampool.Limiter,
	inputs []In,
	fetch func(ctx context.Context, in In) (Out, error),
) ([]Out, error) {
	var sub streampool.Wave // child of the ambient body; drained here, not on root

	var mu sync.Mutex
	out := make([]Out, 0, len(inputs))

	collect := streampool.NewFnSkimmer(
		func(_ context.Context, v Out, err error) error {
			if err != nil {
				return err
			}
			mu.Lock()
			out = append(out, v)
			mu.Unlock()
			return nil
		},
	)

	run := streampool.NewLauncher[In](
		streampool.HandlerFunc[In](func(ctx context.Context, in In, _ error) error {
			v, err := fetch(ctx, in)
			return collect.SubmitResult(ctx, v, err) // ambient → sub wave
		}),
		streampool.WithLimits(limit),
	)

	for _, in := range inputs {
		if err := run.In(&sub).Submit(ctx, in); err != nil {
			return nil, err
		}
	}
	// CloseAndSkimAll returns nil on clean completion; non-nil on ctx cancel or
	// a handler error (ErrWaveDone is only the single-step Skim sentinel).
	if err := sub.CloseAndSkimAll(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
