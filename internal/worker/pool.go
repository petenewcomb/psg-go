// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package worker provides a fungible, demand-driven pool of goroutines that
// drive a shared workq.Queue. Each goroutine holds a per-worker execution
// environment E (a workq.ExecEnv: the pooled rdvq sender/receiver/waiter that
// work runs against) and runs a workq.Worker[E] loop, pulling and executing work
// from the shared queue until it idles out or the pool is stopped.
//
// The pool knows nothing about what the work does — producers Post to the
// workq.Queue, and the queue's unmet-demand signal (wired to the pool's
// DemandFunc) is what asks the pool for another goroutine. So the pool has no
// dependency on the higher-level types (Wave, ops) that create work.
//
// Lifecycle. Workers spawn on demand (uncapped — the only concurrency control
// lives in the work's limiters), PERSIST across batches of work (they
// idle-scale-to-zero only after a real lull), and are torn down definitively
// only via Wait:
//
//   - Acquire/Release maintain a refcount of active referrers (e.g. one per
//     in-flight Wave). A Release that drops the count to zero does NOT stop the
//     workers — they stay warm — unless a Wait is outstanding.
//   - Wait is the graceful quiesce+join: if the refcount is already zero it stops
//     the idle workers now; otherwise it arms "stop on reaching zero" and the
//     final Release stops them. Either way Wait blocks until every worker
//     goroutine has exited. Wait does not cancel running work — it waits for
//     referrers to finish on their own (so it blocks forever if one never
//     Releases, like sync.WaitGroup.Wait). The pool is reusable afterward.
//
// The pool has no context of its own: per-goroutine cancellation is the worker
// context built by newState, under which the work runs.
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/trace"
	"github.com/petenewcomb/psg-go/internal/workq"
)

// Pool is a demand-driven pool of goroutines that drive a shared workq.Queue,
// each holding per-worker state E. Construct with NewPool; the zero value is not
// usable.
type Pool[E workq.ExecEnv] struct {
	queue *workq.Queue

	// newState builds a fresh per-worker execution environment together with the
	// worker context it runs under (E wired into the ctxMeta as the execution
	// environment, a fresh permit-root) and that context's cancel. Supplied by
	// the main package, which owns the ctxMeta wiring — keeping this package
	// independent of it.
	newState func() (state E, workerCtx context.Context, cancel context.CancelFunc)

	workers sync.WaitGroup // every live worker goroutine

	// spawning counts workers between spawn and the result of their first drive
	// (bounds simultaneous spawns — a de-stampede — not the total worker count).
	spawning jobstate.InFlightCounter

	// lifecycle, all guarded by mu (see package doc):
	//   refs    — number of active referrers (e.g. in-flight Waves).
	//   waiting — a Wait is outstanding; stop workers when refs hits zero.
	//   stop    — closed to tell workers to exit; re-armed for reuse. Each worker
	//             captures the current stop at spawn (race-free), so a re-arm
	//             never reaches an already-running worker.
	mu      sync.Mutex
	refs    int
	waiting bool
	stop    chan struct{}
}

// NewPool constructs a pool that drives queue, with per-worker environments built
// by newState. It takes no settings: worker behavior is fixed (see
// workerIdleTimeout / spawnConcurrencyLimit).
//
//nolint:contextcheck // background context used only for tracing
func NewPool[E workq.ExecEnv](
	queue *workq.Queue,
	newState func() (state E, workerCtx context.Context, cancel context.CancelFunc),
) *Pool[E] {
	traceRegion := "worker.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	p := &Pool[E]{queue: queue, newState: newState, stop: make(chan struct{})}
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
}

// DemandFunc returns the unmet-demand callback to wire into the queue (via
// Queue.Init): when the queue signals fresh work with possibly no taker, the
// pool tries to spawn a worker.
func (p *Pool[E]) DemandFunc() workq.RenotifyFunc {
	return p.trySpawnWorker
}

// ── Refcount + definitive quiesce ───────────────────────────────────────────

// Acquire registers a new referrer. On a 0→1 transition it re-arms a stop
// channel a prior Wait closed, so freshly spawned workers aren't instantly
// stopped.
func (p *Pool[E]) Acquire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		p.rearmStopLocked()
	}
	p.refs++
}

// Release drops a referrer. It stops the workers only when this is the last
// referrer AND a Wait is outstanding; otherwise workers persist (and
// idle-scale-to-zero on their own), so they survive between batches.
func (p *Pool[E]) Release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs--
	if p.refs == 0 && p.waiting {
		p.stopWorkersLocked()
	}
}

// Wait performs the definitive quiesce+join. See the package doc.
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

// stopWorkersLocked closes the stop channel once (idempotent within a cycle),
// waking idle/blocked workers to exit. Caller holds p.mu.
func (p *Pool[E]) stopWorkersLocked() {
	select {
	case <-p.stop: // already closed this cycle
	default:
		close(p.stop)
	}
}

// rearmStopLocked replaces a closed stop channel with a fresh one so the pool
// can be reused. Caller holds p.mu.
func (p *Pool[E]) rearmStopLocked() {
	select {
	case <-p.stop:
		p.stop = make(chan struct{})
	default:
	}
}

// ── Spawning ────────────────────────────────────────────────────────────────
//
// Spawn is demand-driven and uncapped. The queue's unmet-demand signal triggers
// the first spawn; the spawn chain (in runWorker) ramps further — a freshly
// spawned worker that finds work spawns a successor, so a burst brings up workers
// as fast as they keep finding work and no faster, and a worker that finds
// nothing breaks the chain. spawnConcurrencyLimit bounds simultaneous spawns to
// de-stampede; the chain (not a demand counter) does the ramp, so there is no
// counter to drift.

func (p *Pool[E]) trySpawnWorker() {
	if spawnConcurrencyLimit < 0 {
		p.spawning.Increment()
	} else if !p.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		return
	}
	p.spawnWorker()
}

func (p *Pool[E]) spawnWorker() {
	// Capture the current stop channel so a later re-arm never reaches this
	// worker (it will have exited on the channel it was born with).
	p.mu.Lock()
	stop := p.stop
	p.mu.Unlock()

	p.workers.Add(1)
	go p.runWorker(stop)
}

// ── Worker loop ─────────────────────────────────────────────────────────────

func (p *Pool[E]) runWorker(stop <-chan struct{}) {
	defer p.workers.Done()

	traceRegion := "worker.Pool.runWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	state, ctx, cancel := p.newState()
	defer cancel()

	w := workq.NewWorker(p.queue, state, ctx,
		workq.WithStop(stop), workq.WithIdleExit(workerIdleTimeout))
	defer w.Release()

	// spawning is true until this worker's first drive completes, after which it
	// leaves the spawning set. The safety defer releases the slot if we never get
	// there (e.g. a panic before the first drive); after the first iteration
	// spawning is false, so it is a no-op.
	spawning := true
	defer func() {
		if spawning {
			p.spawning.Decrement()
		}
	}()

	for {
		_, err := w.DriveOne(ctx)
		if spawning {
			spawning = false
			p.spawning.Decrement()
			if err == nil {
				// Found and ran work: a backlog may remain, so extend the chain.
				p.trySpawnWorker()
			}
		}
		if err != nil {
			// ErrEndOfWork (idle scale-to-zero or definitive stop) or a context
			// cancellation: this worker is done.
			return
		}
	}
}

// ── Fixed worker-behavior tuning (not user-facing) ──────────────────────────

// workerIdleTimeout is how long a worker waits with nothing to do before exiting
// (scale-to-zero). Fixed, not tunable — a sensible default serves all workloads.
// Each idle worker independently waits the timeout and exits, so there is no
// synchronized re-arm stampede to de-correlate (the reason the legacy pool needed
// jitter plus a one-exit-per-window throttle).
const workerIdleTimeout = 1 * time.Second

// spawnConcurrencyLimit caps how many workers may be spawning simultaneously
// (bounds burst spawn, not total workers); <0 means unlimited. The spawn chain
// already self-throttles, so a small constant suffices.
const spawnConcurrencyLimit = 1
