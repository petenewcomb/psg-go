// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package execpool provides a demand-spawned, idle-exiting pool of executor goroutines
// that run blocking [Task] bodies handed to them over an unbuffered rendezvous. It is the
// execution half of the dispatch/execution split: schedulers admit work and PushBack it
// here; an executor takes each task and runs it to completion — it may block, that is its
// job — so the schedulers are never stuck behind a blocking body.
//
// It forks the lifecycle of internal/worker (demand spawn + idle-exit + refcount/Wait) but
// is deliberately simpler:
//
//   - the work source is an [rdvq.Handoff] (an unbuffered rendezvous), not a workq.Queue;
//   - the per-worker loop is fixed — PopFront → [Task.Run] — not pluggable;
//   - there is no governor or workq machinery — admission (limiters, backpressure) is the
//     scheduler's job, upstream of the handoff, so by the time work arrives here there is
//     nothing left to throttle against; an executor's only job is to run it fast.
//
// Spawning mirrors internal/worker.Core's demand-driven, CAPPED ramp. Every PushBack that
// finds no waiting executor fires demand (block-as-demand), but a spawn-concurrency cap
// (spawnConcurrencyLimit) bounds simultaneous spin-ups and a freshly established executor
// extends the spawn chain only while demand persists. The cap matters independently of any
// upstream backpressure: goroutine spin-up has real latency, so committing a burst at once
// steals CPU from in-flight work, delays the very pickup it is spawning for, and leaves a
// glut of parked goroutines — because during spin-up existing executors finish and become
// ready to absorb the demand. A parked, not-yet-established executor is itself standby
// capacity (a later sender's direct handoff finds its registered inbox), so holding the
// slot until it receives its first task is what lets that capacity soak up demand before
// more goroutines are committed.
//
// E is the per-worker execution environment: built once per executor by the newState
// passed to [NewPool] and handed to every [Task.Run] that executor runs. A Task carries
// its own context (the body it runs is closed over its own ctx), so the pool threads no
// ctx into Run — the worker context exists only for the executor's own idle/teardown wait.
package execpool

import (
	"context"
	"sync"
	"time"

	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/petenewcomb/streampool/internal/trace"
	"github.com/petenewcomb/streampool/internal/wavestate"
)

// Task is a unit of executable work. Run executes the body against the per-worker
// execution environment E and is responsible for ALL of its own cleanup (a Task that pools
// itself does so at the end of Run). The pool calls Run exactly once per handed-off Task
// and never touches the Task otherwise — Run is the entire contract.
type Task[E any] interface {
	Run(ee E)
}

// Pool is the executor pool. Construct with [NewPool]; the zero value is not usable.
type Pool[E any] struct {
	// newState builds a fresh per-worker execution environment, held for an executor's
	// lifetime and passed to each Task.Run. Supplied by the constructing package.
	newState func() E

	// handoff is the unbuffered scheduler→executor rendezvous. PushBack parks a producer
	// (firing demand) until an executor PopFronts the task.
	handoff rdvq.Handoff[Task[E]]

	workers sync.WaitGroup // every live executor goroutine

	// spawning counts executors between spawn and the result of their first PopFront
	// (a de-stampede that bounds simultaneous spin-ups, not the total executor count).
	spawning wavestate.InFlightCounter

	// lifecycle, all guarded by mu (mirrors internal/worker.Core):
	//   refs       — number of active referrers.
	//   waiting    — a Wait is outstanding; stop workers when refs hits zero.
	//   poolCtx    — cancelled to tell workers to exit, re-armed for reuse. Each worker
	//                captures the current poolCtx at spawn, so a re-arm never reaches an
	//                already-running one.
	mu         sync.Mutex
	refs       int
	waiting    bool
	poolCtx    context.Context //nolint:containedctx // the teardown signal workers derive their ctx from
	poolCancel context.CancelFunc
}

// NewPool constructs an executor pool. Per-worker environments are built by newState. It
// takes no settings: worker behavior is fixed (see workerIdleTimeout / spawnConcurrencyLimit).
//
//nolint:contextcheck // background context used only for tracing
func NewPool[E any](newState func() E) *Pool[E] {
	traceRegion := "execpool.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	// poolCancel is stored on the Pool and called by stopWorkersLocked (Wait/Release
	// teardown); gosec's intraprocedural check can't see that cross-method call.
	//nolint:gosec // G118: poolCancel stored and called in stopWorkersLocked
	poolCtx, poolCancel := context.WithCancel(context.Background())
	p := &Pool[E]{newState: newState, poolCtx: poolCtx, poolCancel: poolCancel}
	p.handoff.Init()
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
}

// PushBack hands task to an executor, blocking until one takes it or ctx is cancelled. If
// no executor is waiting it fires demand (block-as-demand): the producer parks holding the
// task and TrySpawn (capped) brings up an executor, which takes it directly — no buffer
// dwell. The cap means a burst of producers does not spawn a goroutine glut; the chain
// (see runWorker) ramps as fast as executors actually pick work up.
func (p *Pool[E]) PushBack(ctx context.Context, task Task[E]) error {
	var err error
	if p.handoff.PushBackFunc(task, func(waitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
		// selectFn runs only when no executor was waiting — i.e. we are about to park — so
		// fire demand. TrySpawn is capped, so this is a kick that the spawn chain ramps
		// from, not a spawn-per-producer; most parks under load find the cap saturated and
		// rely on the chain (or an existing parked executor) instead.
		p.TrySpawn()
		var rf rdvq.RenotifyFunc
		rf, err = rdvq.BasicWaitSelect(ctx, waitCh)
		return rf
	}) {
		return nil
	}
	return err
}

// ── Refcount + definitive quiesce (forked from internal/worker.Core) ─────────

// Acquire registers a new referrer. On a 0→1 transition it re-arms a poolCtx a prior Wait
// cancelled, so freshly spawned workers aren't instantly stopped.
func (p *Pool[E]) Acquire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		p.rearmStopLocked()
	}
	p.refs++
}

// Release drops a referrer. It stops the workers only when this is the last referrer AND a
// Wait is outstanding; otherwise workers persist and idle-scale-to-zero on their own.
func (p *Pool[E]) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs--
	if p.refs == 0 && p.waiting {
		p.stopWorkersLocked()
	}
}

// Wait performs the definitive quiesce+join: if no referrer is outstanding it stops the
// idle workers now, otherwise the final Release stops them; either way it blocks until
// every executor goroutine has exited. It does NOT cancel running tasks — it waits for
// referrers to finish on their own (so a referrer that never releases blocks it forever,
// like sync.WaitGroup.Wait). The pool is reusable afterward.
func (p *Pool[E]) Wait() {
	p.mu.Lock()
	if p.refs == 0 {
		p.stopWorkersLocked() // nothing in flight: stop idle workers now
	} else {
		p.waiting = true // the last Release will stop them
	}
	p.mu.Unlock()

	p.workers.Wait()

	p.mu.Lock()
	p.waiting = false
	p.rearmStopLocked() // ready for reuse
	p.mu.Unlock()
}

// stopWorkersLocked cancels poolCtx (idempotent), waking idle/blocked workers to exit.
func (p *Pool[E]) stopWorkersLocked() {
	p.poolCancel()
}

// rearmStopLocked replaces a cancelled poolCtx with a fresh one so the pool can be reused.
func (p *Pool[E]) rearmStopLocked() {
	if p.poolCtx.Err() != nil {
		//nolint:gosec // G118: prior poolCancel already called (poolCtx cancelled)
		p.poolCtx, p.poolCancel = context.WithCancel(context.Background())
	}
}

// ── Spawning (capped + chain, forked from internal/worker.Core) ──────────────

// TrySpawn fires demand: it spawns an executor unless the spawn-concurrency cap is already
// saturated (a spin-up is in flight). It is called by PushBack (block-as-demand) and by the
// chain extension in runWorker; the cap de-stampedes both so simultaneous spin-ups stay
// bounded while existing/parked executors absorb demand.
func (p *Pool[E]) TrySpawn() {
	if !p.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		return // a spin-up is already in flight; the chain or a later park ramps further
	}
	p.spawnWorker()
}

func (p *Pool[E]) spawnWorker() {
	// Capture the current poolCtx so a later re-arm (reuse after Wait) never reaches this
	// worker — it will have exited on the context it was born with.
	p.mu.Lock()
	poolCtx := p.poolCtx
	p.mu.Unlock()
	p.workers.Add(1)
	go p.runWorker(poolCtx)
}

//nolint:contextcheck // poolCtx is the captured spawn-time pool context by design
func (p *Pool[E]) runWorker(poolCtx context.Context) {
	defer p.workers.Done()
	traceRegion := "execpool.Pool.runWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	state := p.newState()
	ctx, cancel := context.WithCancel(poolCtx)
	defer cancel()

	// Hold the spawn-concurrency slot from spawn until this worker establishes (receives
	// its first task) or settles (idles/stops). releaseSpawn frees the slot exactly once
	// (latched); on establishment WITH a task it extends the chain — spawning a successor
	// to check for further demand, exactly like internal/worker.Core. Holding the slot
	// across the first park is deliberate: a parked, not-yet-established executor is standby
	// capacity that a later direct handoff can use, so the cap should suppress new spawns
	// while it waits.
	spawning := true
	releaseSpawn := func(extendChain bool) {
		if spawning {
			spawning = false
			p.spawning.Decrement()
			if extendChain {
				p.TrySpawn()
			}
		}
	}
	defer releaseSpawn(false) // safety: settle the slot if we exit before establishing

	// One reused idle timer (no per-pop alloc). It is armed only while parked in
	// PopFrontFunc; a long Task.Run leaves it to fire into the channel, drained on the next
	// loop before re-arming, so busy time never counts toward the idle window.
	idle := time.NewTimer(workerIdleTimeout)
	defer idle.Stop()

	established := false
	for {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(workerIdleTimeout)

		task, ok := p.handoff.PopFrontFunc(func(inboxCh <-chan Task[E]) (Task[E], bool) {
			select {
			case t := <-inboxCh:
				return t, true
			case <-idle.C:
				return nil, false // idle scale-to-zero
			case <-ctx.Done():
				return nil, false // definitive teardown (Wait)
			}
		})

		if !established {
			// The first PopFront establishes the worker and decides the chain: a task means
			// demand was present, so extend (spawn a successor to check for more); idle/stop
			// means none, so end the chain. Keyed on ok (not the select case) so an orphan
			// recovered by PopFrontFunc still counts as established-with-demand.
			established = true
			releaseSpawn(ok)
		}
		if !ok {
			return
		}
		task.Run(state)
	}
}

// ── Fixed worker-behavior tuning (not user-facing) ──────────────────────────

// workerIdleTimeout is how long an executor waits with no task before exiting
// (scale-to-zero). Fixed, not tunable; mirrors internal/worker.workerIdleTimeout.
const workerIdleTimeout = 1 * time.Second

// spawnConcurrencyLimit caps how many executors may be spinning up simultaneously (bounds
// burst spawn, not total executors). The slot is held from spawn until the worker receives
// its first task or settles, so the chain ramps the count as fast as work is actually
// picked up and no faster. Mirrors internal/worker.spawnConcurrencyLimit.
const spawnConcurrencyLimit = 1
