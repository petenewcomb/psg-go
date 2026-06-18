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
// The pool owns a poolCtx (exposed via PoolCtx) that is cancelled on definitive
// teardown (Wait → workers exit) and re-armed on reuse; Waves derive their waveCtx
// from it so teardown propagates down the wave tree by context ancestry (wave-5b).
// Per-execution cancellation is separate: work runs under the worker context built
// by newState (and, in wave-5b, a borrowed per-wave exec context).
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
// sharedQueue aliases workq.Queue so the Pool can embed it UNEXPORTED. The
// global pool and the shared task/funnel queue are 1:1 and co-lifetimed, so the
// pool owns and drives the queue outright rather than threading a separate
// *Queue. Embedding unexported means only the queue's exported PRODUCER surface
// (Post, the scheduled/timed methods, ExecuteNowOrQueue) promotes onto the Pool —
// for external producers via the global pool — while the CONSUMING side (the
// work pull + priority drive, unexported in workq) stays internal, reached only
// by the pool's own Workers through &p.sharedQueue. (The skim engine uses a
// standalone Queue driven by user goroutines, not a pool.)
type sharedQueue = workq.Queue

type Pool[E workq.ExecEnv] struct {
	sharedQueue

	// newState builds a fresh per-worker execution environment together with the
	// worker context it runs idle/cancel selects under and that context's cancel.
	// It is handed the pool's poolCtx (captured at spawn) to derive the worker
	// context from, so definitive teardown cancels idle workers by ancestry.
	// Supplied by the main package, which owns the context/E wiring — keeping this
	// package independent of it.
	newState func(poolCtx context.Context) (state E, workerCtx context.Context, cancel context.CancelFunc)

	workers sync.WaitGroup // every live worker goroutine

	// spawning counts workers between spawn and the result of their first drive
	// (bounds simultaneous spawns — a de-stampede — not the total worker count).
	spawning jobstate.InFlightCounter

	// lifecycle, all guarded by mu (see package doc):
	//   refs       — number of active referrers (e.g. in-flight Waves).
	//   waiting    — a Wait is outstanding; stop workers when refs hits zero.
	//   poolCtx    — the pool's context; cancelled to tell workers to exit, and
	//                re-armed (fresh WithCancel) for reuse. Each worker captures
	//                the current poolCtx at spawn (race-free), so a re-arm never
	//                reaches an already-running worker. Exposed via PoolCtx so
	//                Waves derive their waveCtx from it: cancellation propagates
	//                pool teardown down the wave tree by stdlib ancestry (wave-5b).
	//   poolCancel — cancels poolCtx.
	mu         sync.Mutex
	refs       int
	waiting    bool
	poolCtx    context.Context //nolint:containedctx // the pool teardown signal; see PoolCtx
	poolCancel context.CancelFunc
}

// NewPool constructs a pool, initializing its embedded work Queue so the queue's
// unmet-demand signal drives spawning. Per-worker environments are built by
// newState. It takes no settings: worker behavior is fixed (see
// workerIdleTimeout / spawnConcurrencyLimit).
//
//nolint:contextcheck // background context used only for tracing
func NewPool[E workq.ExecEnv](
	newState func(poolCtx context.Context) (state E, workerCtx context.Context, cancel context.CancelFunc),
) *Pool[E] {
	traceRegion := "worker.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	// poolCancel is stored on the pool and called by stopWorkersLocked (Wait/Release
	// teardown); gosec's intraprocedural check can't see that cross-method call.
	//nolint:gosec // G118: poolCancel stored and called in stopWorkersLocked
	poolCtx, poolCancel := context.WithCancel(context.Background())
	p := &Pool[E]{newState: newState, poolCtx: poolCtx, poolCancel: poolCancel}
	p.Init(p.trySpawnWorker) // init the embedded queue; unmet demand → spawn a worker
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
}

// ── Refcount + definitive quiesce ───────────────────────────────────────────

// Acquire registers a new referrer. On a 0→1 transition it re-arms a poolCtx a
// prior Wait cancelled, so freshly spawned workers aren't instantly stopped.
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

// stopWorkersLocked cancels poolCtx (idempotent — context cancel no-ops after the
// first call), waking idle/blocked workers to exit. Caller holds p.mu.
func (p *Pool[E]) stopWorkersLocked() {
	p.poolCancel()
}

// rearmStopLocked replaces a cancelled poolCtx with a fresh one so the pool can be
// reused. Caller holds p.mu.
func (p *Pool[E]) rearmStopLocked() {
	if p.poolCtx.Err() != nil {
		// The old poolCancel was already called (poolCtx is cancelled — that is
		// the rearm precondition), so replacing it leaks nothing.
		//nolint:gosec // G118: prior poolCancel already called (poolCtx cancelled)
		p.poolCtx, p.poolCancel = context.WithCancel(context.Background())
	}
}

// PoolCtx returns the pool's current context. It is cancelled when the pool tears
// down definitively (the last Release with a Wait outstanding, or Wait while idle)
// and re-armed on reuse. Waves derive their waveCtx from it so pool teardown
// propagates down the wave tree by stdlib context ancestry (wave-5b). The returned
// context is valid for the current Acquire/Release cycle; callers Acquire before
// reading it (Acquire re-arms after a prior teardown).
func (p *Pool[E]) PoolCtx() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.poolCtx
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
	// Capture the current poolCtx so a later re-arm never reaches this worker (it
	// will have exited on the context it was born with). The worker derives its
	// idle/cancel context from poolCtx (via newState) and stops on poolCtx.Done().
	p.mu.Lock()
	poolCtx := p.poolCtx
	p.mu.Unlock()

	p.workers.Add(1)
	go p.runWorker(poolCtx)
}

// ── Worker loop ─────────────────────────────────────────────────────────────

//nolint:contextcheck // poolCtx is the captured spawn-time pool context by design
func (p *Pool[E]) runWorker(poolCtx context.Context) {
	defer p.workers.Done()

	traceRegion := "worker.Pool.runWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	state, ctx, cancel := p.newState(poolCtx)
	defer cancel()

	// spawning is true until this worker leaves the spawn set, which happens at
	// WORK-SECURE (onSecure, before the body runs) — NOT after the first drive.
	// Releasing post-body would let a long/blocking body (e.g. a task that
	// synchronously drains a nested subwave) pin the spawn-concurrency slot and
	// deadlock the demand that needs another worker. On securing work the worker
	// also extends the spawn chain (a backlog may remain). The safety defer
	// releases the slot if the worker exits without ever securing (idled out on its
	// first drive) or panics before securing.
	spawning := true
	releaseSpawn := func(extendChain bool) {
		if spawning {
			spawning = false
			p.spawning.Decrement()
			if extendChain {
				p.trySpawnWorker()
			}
		}
	}
	defer releaseSpawn(false)

	// poolCtx.Done() is the definitive-stop signal (pool teardown); the worker
	// captured poolCtx at spawn, so a later re-arm never reaches it. onSecure fires
	// on this same goroutine inside DriveOne, so releaseSpawn's access to spawning
	// is race-free.
	w := workq.NewWorker(&p.sharedQueue, state, ctx,
		workq.WithStop(poolCtx.Done()), workq.WithIdleExit(workerIdleTimeout),
		workq.WithOnSecure(func() { releaseSpawn(true) }))
	defer w.Release()

	for {
		_, err := w.DriveOne(ctx)
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
// (bounds burst spawn, not total workers); <0 means unlimited. The slot is held
// only between spawn and WORK-SECURE (onSecure), never through a body, so a
// blocking body can't pin it (see runWorker). The spawn chain self-throttles the
// ramp; this small constant just de-stampedes a demand burst.
const spawnConcurrencyLimit = 1
