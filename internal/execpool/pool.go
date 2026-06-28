// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package execpool provides a demand-spawned, idle-exiting goroutine pool whose per-worker
// behavior is supplied as a [Worker]. The pool owns the hard part — the spawn ramp, the
// idle scale-to-zero, the refcount/[Pool.Wait] lifecycle, and the reused per-worker context
// — and drives each worker through a fixed loop: Wait for work, Work it, repeat, Close on
// exit. The work source, what "work" means, and how the worker finds its environment all
// live in the Worker, so one [Pool] backs both halves of the dispatch/execution split: the
// [Executor] here (its Worker waits on an rdvq.Handoff) and the scheduler (its Worker waits
// on a workq.Queue — built on this same Pool in package workq).
//
// Spawning is demand-driven and CAPPED. A demand source registers unmet demand via
// [Pool.RegisterUnmetDemand] when a work item can't be placed on a waiting worker (the
// Executor's is block-as-demand: a PushBack that found no waiting executor) and
// [Pool.UnregisterUnmetDemand] when that item is taken or withdrawn. The pool spawns while
// unmet demand exceeds in-flight spin-ups, bounded by a spawn-concurrency cap; each worker
// that establishes (its first Wait returns) frees a spin-up slot and re-evaluates, so the
// ramp converges exactly on outstanding demand — no over-shoot. The cap matters
// independently of any upstream backpressure: goroutine spin-up has real latency, so
// committing a burst at once steals CPU from in-flight work, delays the very pickup it is
// spawning for, and leaves a glut of parked goroutines — because during spin-up existing
// workers finish and become ready to absorb the demand. A parked, not-yet-established worker
// is itself standby capacity, so holding the spawn slot until its first Wait returns is what
// lets that capacity soak up demand before more goroutines are committed.
package execpool

import (
	"context"
	"sync"
	"time"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/timerp"
	"github.com/petenewcomb/streampool/internal/trace"
	"github.com/petenewcomb/streampool/internal/wavestate"
)

// Worker is the per-worker behavior a [Pool] drives. The pool runs the loop
// `for { Wait; Work } ; Close` on one goroutine, so a Worker is single-threaded and may
// hold mutable state between calls (e.g. the work item Wait received, to run in Work).
//
//   - Wait blocks until work is available — the point at which the worker is idle and
//     ready (for the scheduler this is where work is added to it) — composing its own
//     select with the supplied idle channel and workerCtx.Done() (definitive stop). It
//     stashes the received work for Work and returns true; on idle-timeout or stop it
//     returns false and the pool exits the loop.
//   - Work executes the work Wait received.
//   - Close tears the worker down. The pool guarantees it never touches the Worker after
//     Close, so a Worker may recycle itself there.
//
// workerCtx is reused across the pool's scale-to-zero churn (see ctxpool) and carries the
// Worker as its value; its Done() is the pool's definitive-stop signal.
type Worker interface {
	Wait(workerCtx context.Context, idle <-chan time.Time) bool
	Work(workerCtx context.Context)
	Close(workerCtx context.Context)
}

// Pool is the demand-spawned, idle-exiting goroutine pool. Construct with [NewPool]; the
// zero value is not usable.
type Pool[W Worker] struct {
	// newWorker builds a fresh Worker for a spawning goroutine (it may return a pooled one,
	// since Close releases the prior occupant). Supplied by the concrete pool.
	newWorker func() W

	workers sync.WaitGroup // every live worker goroutine

	// demand counts registered units of unmet demand — work that couldn't be placed on a
	// waiting worker and is awaiting one. The pool spawns toward it; sources balance
	// Register/UnregisterUnmetDemand.
	demand wavestate.InFlightCounter

	// spawning counts workers between spawn and the result of their first Wait (a
	// de-stampede that bounds simultaneous spin-ups, not the total worker count).
	spawning wavestate.InFlightCounter

	// lifecycle, all guarded by mu:
	//   refs    — number of active referrers.
	//   waiting — a Wait is outstanding; stop workers when refs hits zero.
	//   poolCtx — cancelled to tell workers to exit, re-armed for reuse. Each worker
	//             captures the current poolCtx at spawn, so a re-arm never reaches an
	//             already-running one.
	mu         sync.Mutex
	refs       int
	waiting    bool
	poolCtx    context.Context //nolint:containedctx // the teardown signal workers derive their ctx from
	poolCancel context.CancelFunc
}

// NewPool constructs a pool whose goroutines are driven through newWorker's Workers. It
// takes no settings: worker behavior is fixed (see workerIdleTimeout / spawnConcurrencyLimit).
//
//nolint:contextcheck // background context used only for tracing
func NewPool[W Worker](newWorker func() W) *Pool[W] {
	traceRegion := "execpool.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	// poolCancel is stored on the Pool and called by stopWorkers (Wait/Release teardown);
	// gosec's intraprocedural check can't see that cross-method call.
	//nolint:gosec // G118: poolCancel stored and called in stopWorkers
	poolCtx, poolCancel := context.WithCancel(context.Background())
	p := &Pool[W]{newWorker: newWorker, poolCtx: poolCtx, poolCancel: poolCancel}
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
}

// ── Refcount + definitive quiesce ────────────────────────────────────────────

// Acquire registers a new referrer. On a 0→1 transition it re-arms a poolCtx a prior Wait
// cancelled, so freshly spawned workers aren't instantly stopped.
func (p *Pool[W]) Acquire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		p.rearmStop()
	}
	p.refs++
}

// Release drops a referrer. It stops the workers only when this is the last referrer AND a
// Wait is outstanding; otherwise workers persist and idle-scale-to-zero on their own.
func (p *Pool[W]) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs--
	if p.refs == 0 && p.waiting {
		p.stopWorkers()
	}
}

// Wait performs the definitive quiesce+join: if no referrer is outstanding it stops the idle
// workers now, otherwise the final Release stops them; either way it blocks until every
// worker goroutine has exited. It does NOT cancel running work — it waits for referrers to
// finish on their own (so a referrer that never releases blocks it forever, like
// sync.WaitGroup.Wait). The pool is reusable afterward.
func (p *Pool[W]) Wait() {
	p.mu.Lock()
	if p.refs == 0 {
		p.stopWorkers() // nothing in flight: stop idle workers now
	} else {
		p.waiting = true // the last Release will stop them
	}
	p.mu.Unlock()

	p.workers.Wait()

	p.mu.Lock()
	p.waiting = false
	p.rearmStop() // ready for reuse
	p.mu.Unlock()
}

// stopWorkers cancels poolCtx (idempotent), waking idle/blocked workers to exit. Caller
// holds p.mu.
func (p *Pool[W]) stopWorkers() {
	p.poolCancel()
}

// rearmStop replaces a cancelled poolCtx with a fresh one so the pool can be reused. Caller
// holds p.mu.
func (p *Pool[W]) rearmStop() {
	if p.poolCtx.Err() != nil {
		//nolint:gosec // G118: prior poolCancel already called (poolCtx cancelled)
		p.poolCtx, p.poolCancel = context.WithCancel(context.Background())
	}
}

// ── Spawning (capped, demand-driven) ──────────────────────────────────────────

// RegisterUnmetDemand records one work item that couldn't be placed on a waiting worker and
// spawns toward it (capped). Each call must be balanced by exactly one UnregisterUnmetDemand
// when the item is taken by a worker or withdrawn (e.g. the producer's ctx is cancelled).
func (p *Pool[W]) RegisterUnmetDemand() {
	p.demand.Increment()
	p.maybeSpawn()
}

// UnregisterUnmetDemand records that a unit of unmet demand was met (a worker took it) or
// withdrawn. It does not stop an in-flight spin-up — a spawned worker that finds no work
// idles out — it just keeps the demand count honest so the ramp stops at the right size.
func (p *Pool[W]) UnregisterUnmetDemand() {
	p.demand.Decrement()
}

// maybeSpawn spawns one worker if there is unmet demand and the spawn-concurrency cap has a
// free slot. It is called on every demand event (RegisterUnmetDemand) and whenever a worker
// frees a slot by establishing (runWorker), so the pool ramps one spin-up at a time toward
// outstanding demand and no further. (A racy spawn for demand that just vanished simply
// idles out — harmless.)
func (p *Pool[W]) maybeSpawn() {
	if p.demand.IsZero() {
		return // no unmet demand
	}
	if p.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		p.spawnWorker()
	}
}

// Nudge spawns one worker (capped by spawnConcurrencyLimit) regardless of the demand
// counter — for demand sources that are NOT counter-balanced. A counter-balanced source
// (RegisterUnmetDemand/UnregisterUnmetDemand) records work awaiting a worker and the
// worker that takes it un-records it; some sources can't pair that way and only need
// "ensure a worker exists to drain what's ready." A scheduler's Accepted engine is the
// canonical case: a batch promoted to fresh, or a flush coming due after the pool scaled
// to zero, needs a worker even though no Post registered demand. An over-nudge is
// self-correcting — the spawned worker establishes, drains whatever is ready, and idles
// out if there is nothing — so there is no counter to drift. (This is the execpool analog
// of the obsolete worker.Pool's fire-and-forget TrySpawn.)
func (p *Pool[W]) Nudge() {
	if p.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		p.spawnWorker()
	}
}

func (p *Pool[W]) spawnWorker() {
	// Capture the current poolCtx so a later re-arm (reuse after Wait) never reaches this
	// worker — it will have exited on the context it was born with.
	p.mu.Lock()
	poolCtx := p.poolCtx
	p.mu.Unlock()
	p.workers.Add(1)
	go p.runWorker(poolCtx)
}

//nolint:contextcheck // poolCtx is the captured spawn-time pool context by design
func (p *Pool[W]) runWorker(poolCtx context.Context) {
	defer p.workers.Done()
	traceRegion := "execpool.Pool.runWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	w := p.newWorker()

	// The worker context: a ctxpool child of poolCtx, reused across the pool's scale-to-zero
	// churn, carrying the Worker as its value, with Done() == poolCtx.Done() (definitive
	// stop). No independent cancel — the worker exits on stop or idle, and its work runs
	// under its own borrowed context, not this one.
	ctx := ctxpool.WithValue(poolCtx, w)
	defer ctxpool.Free(ctx)

	// Hold the spawn-concurrency slot from spawn until this worker establishes (its first
	// Wait returns) or settles. establish frees the slot exactly once (latched) and
	// re-evaluates demand — spawning a successor only if unmet demand still exceeds in-flight
	// spin-ups. Holding the slot across the first Wait is deliberate: a parked,
	// not-yet-established worker is standby capacity, so the cap should suppress new spawns
	// while it waits.
	spawning := true
	establish := func() {
		if spawning {
			spawning = false
			p.spawning.Decrement()
			p.maybeSpawn()
		}
	}
	defer establish() // safety: settle the slot if we exit before establishing

	// One pooled idle timer (no per-Wait alloc). It is armed before each Wait and counts
	// only while the worker is idle; a long Work leaves it to fire into the channel, drained
	// by the next Reset, so busy time never counts toward the idle window.
	idle := timerp.Get()
	defer timerp.Put(idle)

	for first := true; ; first = false {
		timerp.Reset(idle, workerIdleTimeout)
		ok := w.Wait(ctx, idle.C)
		if first {
			// The first Wait establishes the worker: free the spin-up slot and ramp toward
			// any remaining unmet demand. (Whether this Wait got work is irrelevant to the
			// ramp now — the demand counter drives it.)
			establish()
		}
		if !ok {
			break // idled out or stopped
		}
		w.Work(ctx)
	}
	w.Close(ctx)
}

// ── Fixed worker-behavior tuning (not user-facing) ──────────────────────────

// workerIdleTimeout is how long a worker waits with no work before exiting (scale-to-zero).
// Fixed, not tunable — a sensible default serves all workloads.
const workerIdleTimeout = 1 * time.Second

// spawnConcurrencyLimit caps how many workers may be spinning up simultaneously (bounds
// burst spawn, not total workers). The slot is held from spawn until the worker's first Wait
// returns, so the chain ramps the count as fast as work is actually picked up and no faster.
const spawnConcurrencyLimit = 1
