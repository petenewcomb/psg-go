// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"time"
)

// AccumulatorFactory creates per-instance Accumulators for a
// [Funnel]. The framework calls [AccumulatorFactory.NewAccumulator]
// whenever it needs fresh accumulator state — once at startup of
// the first instance and again after any prior instance flushes
// and is discarded.
//
// There is no factory-level Close hook. A factory is wave-scoped and
// the framework guarantees a well-defined instance lifetime (see
// [Accumulator]): an instance is never touched after its Flush, and
// every outstanding instance is flushed before the wave drains to
// Done. So any factory-level resources (shared connections,
// registries, pooled allocations) can be released by the owner after
// the drain returns — the user owns the factory and has that sync
// point — without a framework-mediated callback.
type AccumulatorFactory[T any] interface {
	NewAccumulator() Accumulator[T]
}

// AccumulatorFactoryFunc[T] is the minimal function adapter that makes
// a bare `func() Accumulator[T]` satisfy [AccumulatorFactory].
type AccumulatorFactoryFunc[T any] func() Accumulator[T]

// NewAccumulator satisfies [AccumulatorFactory].
func (f AccumulatorFactoryFunc[T]) NewAccumulator() Accumulator[T] { return f() }

// FuncAccumulatorFactory implements [AccumulatorFactory] using a
// function field.
type FuncAccumulatorFactory[T any] struct {
	NewAccumulatorFn func() Accumulator[T]
}

// NewAccumulator satisfies [AccumulatorFactory]; delegates to
// NewAccumulatorFn.
func (f FuncAccumulatorFactory[T]) NewAccumulator() Accumulator[T] {
	return f.NewAccumulatorFn()
}

// NewAccumulatorFactory is the type-inference-friendly constructor
// for a closure-based [AccumulatorFactory]. T is inferred from
// newAccumulator's return type, sparing the user the [T] annotation.
func NewAccumulatorFactory[T any](
	newAccumulator func() Accumulator[T],
) FuncAccumulatorFactory[T] {
	return FuncAccumulatorFactory[T]{NewAccumulatorFn: newAccumulator}
}

// Accumulator instances perform stateful partial aggregation of
// values inside a [Funnel]. Each input is delivered via
// [Accumulator.Accumulate]; the instance owns its accumulated state
// across calls. When the framework finalizes the instance (on a
// user-requested flush deadline or when the wave drains),
// [Accumulator.Flush] is invoked.
//
// Downstream emission is the body's responsibility: an Accumulator
// that wants to emit aggregated results explicitly calls Submit on
// whichever downstream sinks (Funnel, Skimmer) it has captured via
// its factory closure. The framework does not auto-route any value
// returned by Accumulate or Flush — those methods return only an
// error.
//
// Errors returned from Accumulate or Flush are surfaced through
// [Wave.SkimAll] (the framework's "unexpected error" channel,
// matching the SkimAll contract for skim-function errors). Expected
// errors that the body wants to forward as values should be passed
// through Submit/SubmitErr on downstream sinks instead.
type Accumulator[T any] interface {
	// Accumulate processes a single input value (paired with an
	// upstream error, which may be nil). Returns the time when the
	// instance's Flush method should be called, or a zero time value
	// if Flush need not be called until the framework drains. If a
	// non-nil error is returned, the framework surfaces it via
	// SkimAll and may then discard this instance; subsequent inputs
	// are handled by a fresh instance from the factory.
	Accumulate(ctx context.Context, value T, err error) (time.Time, error)

	// Flush finalizes the accumulated state. No further calls to
	// Accumulate or Flush are made to this instance after Flush has
	// been called; the framework drops its references and the next
	// input creates a fresh instance from the factory.
	Flush(ctx context.Context) error
}

// FuncAccumulator implements [Accumulator] using function fields.
// Convenient for the common case where state lives in a closure
// shared between the two functions. FlushFn is optional; if nil,
// Flush is a no-op.
type FuncAccumulator[T any] struct {
	AccumulateFn func(ctx context.Context, value T, err error) (time.Time, error)
	FlushFn      func(ctx context.Context) error
}

// Accumulate calls the AccumulateFn field with the provided
// arguments.
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

// NewAccumulator is the type-inference-friendly constructor for a
// closure-based Accumulator. T is inferred from the accumulate
// closure's signature, sparing the user the [T] annotation. Pass
// nil for flush if the accumulator doesn't need a final flush.
// Returns the concrete [FuncAccumulator][T] (which satisfies
// [Accumulator][T]); callers who want struct-level access keep it,
// and callers who treat the result as an Accumulator interface get
// that automatically via structural typing.
func NewAccumulator[T any](
	accumulate func(ctx context.Context, value T, err error) (time.Time, error),
	flush func(ctx context.Context) error,
) FuncAccumulator[T] {
	return FuncAccumulator[T]{AccumulateFn: accumulate, FlushFn: flush}
}

// ErrAccumulator is the err-only case of [Accumulator] — alias
// for [Accumulator][struct{}], named for intent. Typically the
// return type of a factory closure that creates err-receiving
// accumulators (often via [NewErrAccumulator]).
type ErrAccumulator = Accumulator[struct{}]

// ErrAccumulatorFactory is the err-only case of
// [AccumulatorFactory] — alias for [AccumulatorFactory][struct{}],
// named for intent. Typically constructed via
// [NewErrAccumulatorFactory] (stateless case) or built around
// [NewAccumulatorFactory] with a [NewErrAccumulator] inside the
// factory closure (per-instance state case).
type ErrAccumulatorFactory = AccumulatorFactory[struct{}]

// FuncErrAccumulator is the err-only adapter for
// [Accumulator][struct{}] — receives only the upstream err on each
// Accumulate call, without a value parameter. Saves the framework
// adding a signature-adapter closure when the user's body doesn't
// care about the void value.
type FuncErrAccumulator struct {
	AccumulateFn func(ctx context.Context, err error) (time.Time, error)
	FlushFn      func(ctx context.Context) error
}

// Accumulate satisfies [Accumulator][struct{}]; delegates to
// AccumulateFn, dropping the void value parameter.
func (a FuncErrAccumulator) Accumulate(ctx context.Context, _ struct{}, err error) (time.Time, error) {
	return a.AccumulateFn(ctx, err)
}

// Flush satisfies [Accumulator][struct{}]; calls FlushFn if non-nil,
// otherwise no-op.
func (a FuncErrAccumulator) Flush(ctx context.Context) error {
	if a.FlushFn == nil {
		return nil
	}
	return a.FlushFn(ctx)
}

// NewErrAccumulator is the convenience constructor for a closure-
// based err-receiving accumulator. Pass nil for flush if the
// accumulator doesn't need a final flush.
func NewErrAccumulator(
	accumulate func(ctx context.Context, err error) (time.Time, error),
	flush func(ctx context.Context) error,
) FuncErrAccumulator {
	return FuncErrAccumulator{AccumulateFn: accumulate, FlushFn: flush}
}

// FuncErrAccumulatorFactory is the err-only adapter for
// [AccumulatorFactory][struct{}] that stores per-accumulator
// function fields directly (rather than going through a factory
// closure). Each NewAccumulator call constructs a fresh
// [FuncErrAccumulator] with the stored fields copied in — no
// framework-added closure allocation. Suitable for the common
// stateless-aggregation case where all accumulator instances share
// the same Accumulate / Flush logic. For per-instance state, use
// [FuncAccumulatorFactory] with a [NewErrAccumulator] inside the
// factory closure.
type FuncErrAccumulatorFactory struct {
	AccumulateFn func(ctx context.Context, err error) (time.Time, error)
	FlushFn      func(ctx context.Context) error
}

// NewAccumulator satisfies [AccumulatorFactory][struct{}]; returns
// a fresh [FuncErrAccumulator] with this factory's AccumulateFn /
// FlushFn fields copied in.
func (f FuncErrAccumulatorFactory) NewAccumulator() Accumulator[struct{}] {
	return FuncErrAccumulator(f)
}

// NewErrAccumulatorFactory is the convenience constructor for an
// err-only [AccumulatorFactory][struct{}] that stores per-
// accumulator fns directly (no factory closure overhead). Pass nil
// for flush if not needed.
func NewErrAccumulatorFactory(
	accumulate func(ctx context.Context, err error) (time.Time, error),
	flush func(ctx context.Context) error,
) FuncErrAccumulatorFactory {
	return FuncErrAccumulatorFactory{AccumulateFn: accumulate, FlushFn: flush}
}
