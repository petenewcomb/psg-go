// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package worker provides a fungible, demand-driven pool of goroutines. Each goroutine
// holds a per-worker execution environment E (a workq.ExecEnv) and runs a pluggable
// per-worker loop until it idles out or the pool is stopped.
//
// The lifecycle and spawn machinery live in the generic [Core]; the per-worker loop and
// the demand source are supplied. Two instances sit on the same Core: the scheduler
// [Pool] here drives a shared workq.Queue via a workq.Worker loop (its demand is the
// queue's unmet-demand signal); the executor pool (dispatch/execution split, C2) drives
// an rdvq.Handoff via a PopFront→Run loop (its demand is the Handoff's block-as-demand).
// The Core knows nothing about either — it just spawns, ramps, and reaps goroutines.
//
// Lifecycle. Workers spawn on demand (uncapped — the only concurrency control lives in
// the work's limiters), PERSIST across batches of work (they idle-scale-to-zero only
// after a real lull), and are torn down definitively only via Wait:
//
//   - Acquire/Release maintain a refcount of active referrers (e.g. one per in-flight
//     Wave). A Release that drops the count to zero does NOT stop the workers — they stay
//     warm — unless a Wait is outstanding.
//   - Wait is the graceful quiesce+join: if the refcount is already zero it stops the idle
//     workers now; otherwise it arms "stop on reaching zero" and the final Release stops
//     them. Either way Wait blocks until every worker goroutine has exited. Wait does not
//     cancel running work — it waits for referrers to finish on their own (so it blocks
//     forever if one never Releases, like sync.WaitGroup.Wait). The pool is reusable after.
//
// The Core owns a poolCtx that is cancelled on definitive teardown (Wait → workers exit)
// and re-armed on reuse; each worker derives its idle/cancel context from it (via
// newState). Per-execution cancellation is separate: a body runs under a context borrowed
// at dispatch (descended from its submit ctx), not under the worker context.
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/petenewcomb/streampool/internal/trace"
	"github.com/petenewcomb/streampool/internal/wavestate"
	"github.com/petenewcomb/streampool/internal/workq"
)

// WorkerLoop is a per-worker drive loop. It runs on its own goroutine until the worker
// idles out or stop fires, holding the per-worker state and deriving any blocking from
// the worker ctx. It MUST call releaseSpawn to surrender the spawn-concurrency slot once
// it has settled — releaseSpawn(true) at WORK-SECURE (it found work; extend the spawn
// chain for any backlog), releaseSpawn(false) when it parks without immediate work (it
// settled out of the stampede; no chain). The Core fires releaseSpawn(false) as a safety
// net if the loop returns without ever calling it.
type WorkerLoop[E workq.ExecEnv] func(
	ctx context.Context, state E, releaseSpawn func(extendChain bool), stop <-chan struct{},
)

// Core is the generic demand-spawned, idle-exiting goroutine pool: the lifecycle (refcount
// + definitive quiesce) and spawn machinery, with the per-worker loop and demand source
// supplied. It owns no work queue — that belongs to the concrete pool built on it.
// Construct via a concrete pool's constructor (see [NewPool]); the zero value is not usable.
type Core[E workq.ExecEnv] struct {
	// newState builds a fresh per-worker execution environment together with the worker
	// context it runs idle/cancel selects under and that context's cancel. It is handed
	// the Core's poolCtx (captured at spawn) to derive the worker context from, so
	// definitive teardown cancels idle workers by ancestry. Supplied by the main package,
	// which owns the context/E wiring — keeping this package independent of it.
	newState func(poolCtx context.Context) (state E, workerCtx context.Context, cancel context.CancelFunc)

	// loop is the pluggable per-worker drive loop (scheduler / executor).
	loop WorkerLoop[E]

	workers sync.WaitGroup // every live worker goroutine

	// spawning counts workers between spawn and the result of their first drive
	// (bounds simultaneous spawns — a de-stampede — not the total worker count).
	spawning wavestate.InFlightCounter

	// lifecycle, all guarded by mu (see package doc):
	//   refs       — number of active referrers (e.g. in-flight Waves).
	//   waiting    — a Wait is outstanding; stop workers when refs hits zero.
	//   poolCtx    — the pool's context; cancelled to tell workers to exit, and re-armed
	//                (fresh WithCancel) for reuse. Each worker captures the current poolCtx
	//                at spawn (race-free), so a re-arm never reaches an already-running one.
	//   poolCancel — cancels poolCtx.
	mu         sync.Mutex
	refs       int
	waiting    bool
	poolCtx    context.Context //nolint:containedctx // the pool teardown signal workers derive their ctx from
	poolCancel context.CancelFunc
}

// newCore builds a Core with the given per-worker state factory and drive loop.
//
//nolint:contextcheck // background context used only for tracing
func newCore[E workq.ExecEnv](
	newState func(poolCtx context.Context) (state E, workerCtx context.Context, cancel context.CancelFunc),
	loop WorkerLoop[E],
) *Core[E] {
	// poolCancel is stored on the Core and called by stopWorkersLocked (Wait/Release
	// teardown); gosec's intraprocedural check can't see that cross-method call.
	//nolint:gosec // G118: poolCancel stored and called in stopWorkersLocked
	poolCtx, poolCancel := context.WithCancel(context.Background())
	return &Core[E]{newState: newState, loop: loop, poolCtx: poolCtx, poolCancel: poolCancel}
}

// ── Refcount + definitive quiesce ───────────────────────────────────────────

// Acquire registers a new referrer. On a 0→1 transition it re-arms a poolCtx a prior Wait
// cancelled, so freshly spawned workers aren't instantly stopped.
func (c *Core[E]) Acquire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refs == 0 {
		c.rearmStopLocked()
	}
	c.refs++
}

// Release drops a referrer. It stops the workers only when this is the last referrer AND a
// Wait is outstanding; otherwise workers persist (and idle-scale-to-zero on their own), so
// they survive between batches.
func (c *Core[E]) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs--
	if c.refs == 0 && c.waiting {
		c.stopWorkersLocked()
	}
}

// Wait performs the definitive quiesce+join. See the package doc.
func (c *Core[E]) Wait() {
	c.mu.Lock()
	if c.refs == 0 {
		c.stopWorkersLocked() // nothing in flight: stop idle workers now
	} else {
		c.waiting = true // the last Release will stop them
	}
	c.mu.Unlock()

	c.workers.Wait()

	c.mu.Lock()
	c.waiting = false
	c.rearmStopLocked() // ready for reuse
	c.mu.Unlock()
}

// stopWorkersLocked cancels poolCtx (idempotent — context cancel no-ops after the first
// call), waking idle/blocked workers to exit. Caller holds c.mu.
func (c *Core[E]) stopWorkersLocked() {
	c.poolCancel()
}

// rearmStopLocked replaces a cancelled poolCtx with a fresh one so the pool can be reused.
// Caller holds c.mu.
func (c *Core[E]) rearmStopLocked() {
	if c.poolCtx.Err() != nil {
		// The old poolCancel was already called (poolCtx is cancelled — that is the rearm
		// precondition), so replacing it leaks nothing.
		//nolint:gosec // G118: prior poolCancel already called (poolCtx cancelled)
		c.poolCtx, c.poolCancel = context.WithCancel(context.Background())
	}
}

// ── Spawning ────────────────────────────────────────────────────────────────
//
// Spawn is demand-driven and uncapped. The demand source calls TrySpawn (for the scheduler
// pool, the queue's unmet-demand signal); the spawn chain (in runWorker) ramps further — a
// freshly spawned worker that finds work spawns a successor, so a burst brings up workers
// as fast as they keep finding work and no faster, and a worker that finds nothing breaks
// the chain. spawnConcurrencyLimit bounds simultaneous spawns to de-stampede; the chain
// (not a demand counter) does the ramp, so there is no counter to drift.

// TrySpawn is the demand entry point: the concrete pool wires its demand source to it (the
// scheduler pool via the workq.Queue's unmet-demand signal). It spawns a worker unless the
// spawn-concurrency limit is already saturated.
func (c *Core[E]) TrySpawn() {
	if spawnConcurrencyLimit < 0 {
		c.spawning.Increment()
	} else if !c.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		return
	}
	c.spawnWorker()
}

func (c *Core[E]) spawnWorker() {
	// Capture the current poolCtx so a later re-arm never reaches this worker (it will have
	// exited on the context it was born with). The worker derives its idle/cancel context
	// from poolCtx (via newState) and stops on poolCtx.Done().
	c.mu.Lock()
	poolCtx := c.poolCtx
	c.mu.Unlock()

	c.workers.Add(1)
	go c.runWorker(poolCtx)
}

// ── Worker goroutine ────────────────────────────────────────────────────────

//nolint:contextcheck // poolCtx is the captured spawn-time pool context by design
func (c *Core[E]) runWorker(poolCtx context.Context) {
	defer c.workers.Done()

	traceRegion := "worker.Core.runWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	state, ctx, cancel := c.newState(poolCtx)
	defer cancel()

	// spawning is true until this worker leaves the spawn set, which happens at WORK-SECURE
	// (the loop calls releaseSpawn(true), before the body runs) — NOT after the first drive.
	// Releasing post-body would let a long/blocking body (e.g. a task that synchronously
	// drains a nested subwave) pin the spawn-concurrency slot and deadlock the demand that
	// needs another worker. On securing work the worker also extends the spawn chain (a
	// backlog may remain). The safety defer releases the slot if the loop returns without
	// ever securing (idled out on its first drive) or panics before securing. releaseSpawn
	// is called only from this goroutine (the loop runs here), so spawning is race-free.
	spawning := true
	releaseSpawn := func(extendChain bool) {
		if spawning {
			spawning = false
			c.spawning.Decrement()
			if extendChain {
				c.TrySpawn()
			}
		}
	}
	defer releaseSpawn(false)

	// poolCtx.Done() is the definitive-stop signal (pool teardown); the worker captured
	// poolCtx at spawn, so a later re-arm never reaches it.
	c.loop(ctx, state, releaseSpawn, poolCtx.Done())
}

// ── Scheduler pool ──────────────────────────────────────────────────────────

// sharedQueue aliases workq.Queue so the Pool can embed it UNEXPORTED. The global pool and
// the shared task/funnel queue are 1:1 and co-lifetimed, so the pool owns and drives the
// queue outright rather than threading a separate *Queue. Embedding unexported means only
// the queue's exported PRODUCER surface (Post, the scheduled/timed methods,
// ExecuteNowOrQueue) promotes onto the Pool — for external producers via the global pool —
// while the CONSUMING side (the work pull + priority drive, unexported in workq) stays
// internal, reached only by the pool's own workers through &p.sharedQueue.
type sharedQueue = workq.Queue

// Pool is the scheduler pool: a [Core] driving a shared workq.Queue via the workq.Worker
// loop. Producers Post to the embedded Queue, whose unmet-demand signal drives spawning.
// Construct with NewPool; the zero value is not usable.
type Pool[E workq.ExecEnv] struct {
	*Core[E]
	sharedQueue
}

// NewPool constructs a scheduler pool, wiring the embedded queue's unmet-demand signal to
// the Core's spawn entry. Per-worker environments are built by newState. It takes no
// settings: worker behavior is fixed (see workerIdleTimeout / spawnConcurrencyLimit).
//
//nolint:contextcheck // background context used only for tracing
func NewPool[E workq.ExecEnv](
	newState func(poolCtx context.Context) (state E, workerCtx context.Context, cancel context.CancelFunc),
) *Pool[E] {
	traceRegion := "worker.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	p := &Pool[E]{}
	p.Core = newCore(newState, p.driveQueue)
	p.Init(p.TrySpawn) // the embedded queue's unmet demand → the Core's spawn entry
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
}

// driveQueue is the scheduler [WorkerLoop]: a workq.Worker pulling and executing work from
// the shared queue until idle-exit or definitive stop, surrendering the spawn slot at
// work-secure (extend the chain) or on a park (no chain). onWait is critical with the
// deadline-watching worker that parks past the idle timeout — without it that worker would
// hold the slot forever and starve new spawns.
func (p *Pool[E]) driveQueue(ctx context.Context, state E, releaseSpawn func(extendChain bool), stop <-chan struct{}) {
	w := workq.NewWorker(&p.sharedQueue, state, ctx,
		workq.WithStop(stop), workq.WithIdleExit(workerIdleTimeout),
		workq.WithOnSecure(func() { releaseSpawn(true) }),
		workq.WithOnWait(func() { releaseSpawn(false) }))
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
// (scale-to-zero). Fixed, not tunable — a sensible default serves all workloads. Each idle
// worker independently waits the timeout and exits, so there is no synchronized re-arm
// stampede to de-correlate (the reason the legacy pool needed jitter plus a
// one-exit-per-window throttle).
const workerIdleTimeout = 1 * time.Second

// spawnConcurrencyLimit caps how many workers may be spawning simultaneously (bounds burst
// spawn, not total workers); <0 means unlimited. The slot is held only between spawn and
// WORK-SECURE, never through a body, so a blocking body can't pin it (see runWorker). The
// spawn chain self-throttles the ramp; this small constant just de-stampedes a demand burst.
const spawnConcurrencyLimit = 1
