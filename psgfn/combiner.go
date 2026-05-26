// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
	"time"
)

// CombinerFactory is a function that creates a new Accumulator instance.
// The framework calls a CombinerFactory whenever it needs a fresh accumulator
// state — once at startup of the first instance and again after any prior
// instance flushes and is discarded.
type CombinerFactory[T any] = func() Accumulator[T]

// Accumulator instances perform stateful partial aggregation of values inside
// a Combiner. Each input is delivered via [Accumulator.Accumulate]; the
// instance owns its accumulated state across calls. When the framework
// finalizes the instance (on a user-requested flush deadline or on
// CombinerPool drain), [Accumulator.Flush] is invoked.
//
// Downstream emission is the body's responsibility: an Accumulator that
// wants to emit aggregated results explicitly calls Submit on whichever
// downstream sinks (Combiner, Gatherer) it has captured via its factory
// closure. The framework does not auto-route any value returned by
// Accumulate or Flush — those methods return only an error.
//
// Errors returned from Accumulate or Flush are surfaced through
// [psg.Pool.GatherAll] (the framework's "unexpected error" channel,
// matching the GatherAll contract for gather-function errors). Expected
// errors that the body wants to forward as values should be passed
// through Submit/SubmitErr on downstream sinks instead.
type Accumulator[T any] interface {
	// Accumulate processes a single input value (paired with an upstream
	// error, which may be nil). Returns the time when the instance's
	// Flush method should be called, or a zero time value if Flush need
	// not be called until the framework drains. If a non-nil error is
	// returned, the framework surfaces it via GatherAll and may then
	// discard this instance; subsequent inputs are handled by a fresh
	// instance from the factory.
	Accumulate(ctx context.Context, value T, err error) (time.Time, error)

	// Flush finalizes the accumulated state. No further calls to
	// Accumulate or Flush are made to this instance after Flush has
	// been called; the framework drops its references and the next
	// input creates a fresh instance from the factory.
	Flush(ctx context.Context) error
}

// FuncAccumulator implements [Accumulator] using function fields. Convenient
// for the common case where state lives in a closure shared between the
// two functions. FlushFn is optional; if nil, Flush is a no-op.
type FuncAccumulator[T any] struct {
	AccumulateFn func(ctx context.Context, value T, err error) (time.Time, error)
	FlushFn      func(ctx context.Context) error
}

// Accumulate calls the AccumulateFn field with the provided arguments.
func (a FuncAccumulator[T]) Accumulate(ctx context.Context, value T, err error) (time.Time, error) {
	return a.AccumulateFn(ctx, value, err)
}

// Flush calls the FlushFn field; nil FlushFn is a no-op.
func (a FuncAccumulator[T]) Flush(ctx context.Context) error {
	if a.FlushFn == nil {
		return nil
	}
	return a.FlushFn(ctx)
}
