// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package streampool is a Go worker pool whose tasks return values — and can
// submit more tasks without deadlocking. Results stream back to your code as
// they complete, through bodies that run concurrently while aggregation stays
// as sequential (or as parallel) as you choose.
//
// # Model
//
// Three types compose:
//
//   - [Wave] — a batch of work you await. A zero-value Wave (var w streampool.Wave)
//     is ready to use; there is no constructor and the Wave owns no context.
//     Drain it with [Wave.Skim], [Wave.SkimAll], or [Wave.CloseAndSkimAll], which
//     return [ErrWaveDone] once the wave is complete. A sub-wave is just a
//     zero-value Wave first used inside a body.
//   - Flow — an optional, refcounted, context-borne handle for one logical unit
//     of work that may cross wave boundaries (trace context, audit metadata,
//     cleanup hooks). Most programs never construct one.
//   - Pool — the workers. Internal and fungible: a single process-wide pool is
//     used implicitly and sized automatically. You do not construct or tune it;
//     per-op concurrency is expressed with Limiters (see [WithLimits]).
//
// Work is performed by wave-agnostic ops, defined once and reusable:
// [NewLauncher] (stateless dispatch), [NewFunnel] (stateful aggregation), and
// [NewSkimmer] (terminal sink). A body routes values by calling Submit on a
// downstream op; there is no separate wiring step.
//
// # Routing
//
// Ops carry no wave at construction. Inside a body, op.Submit(ctx, v) targets the
// body's ambient (framework-supplied) wave. At top level — or to redirect into a
// different wave — bind a wave with op.In(&w), e.g. launcher.In(&w).Submit(ctx, v).
// Routing is handle-level and never alters the ctx, so a Flow (and trace context)
// rides along across a redirect.
//
// # Context and cancellation
//
// Cancellation rides context ancestry, not the Wave. A body runs under a context
// descended from the ctx passed to the dispatching Submit/Start call, so
// cancelling that ctx (usually the same ctx you drive the wave with) stops the
// work. There is no Wave.Cancel and no framework force-abort: a Wave has no
// context to cancel. Cancelling the ctx passed to a drain (SkimAll/CloseAndSkimAll)
// makes the drain return that ctx's error; in-flight bodies keep running under
// their own submit ctxs until they return, and the framework cleans up as they do.
//
// User code should propagate the context it is given rather than creating a fresh
// root (context.Background()) inside a body — doing so breaks cancellation,
// backpressure pacing, and the reentrancy guard.
//
// # Safety
//
//   - Reentrancy guard: you cannot Skim a wave you are part of (its own or an
//     ancestor body) — exactly the cycle that would deadlock. Such a call panics
//     with a descriptive message.
//   - Skim queuing: skim work is queued rather than run recursively, so a body
//     that submits more work cannot overflow the stack.
//   - Panic-safe: a panic in user code is recovered and surfaced as an error
//     rather than crashing the process.
package streampool

//go:generate go build -C internal/cmd/benchnorm -o ../../bin/benchnorm
//go:generate go build -C internal/cmd/benchcmp -o ../../bin/benchcmp
//go:generate go build -C internal/cmd/chartgen -o ../../bin/chartgen
//go:generate go build -C internal/cmd/fmttrace -o ../../bin/fmttrace
//go:generate internal/bin/chartgen -o docs/charts bench.txt
