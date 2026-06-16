// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package worker provides a fungible, context-free, demand-driven pool of
// worker goroutines that execute opaque units of work. It is generic over the
// per-worker state E that each worker holds for the lifetime of its goroutine
// (e.g. an execution environment carrying a pooled rdvq sender). The pool knows
// nothing about what a unit does — a unit bakes in all of its own logic and is
// handed only the worker's state — so the pool has no dependency on the
// higher-level types (Wave, ops) that create units.
//
// Lifecycle. Workers spawn on demand (uncapped — the only concurrency control
// lives in the units), PERSIST across batches of work (they idle-scale-to-zero
// only after a real lull), and are torn down definitively only via Wait:
//
//   - Acquire/Release maintain a refcount of active referrers (e.g. one per
//     in-flight Wave). A Release that drops the count to zero does NOT stop the
//     workers — they stay warm — unless a Wait is outstanding.
//   - Wait is the graceful quiesce+join: if the refcount is already zero it
//     stops the idle workers immediately; otherwise it arms "stop on reaching
//     zero" and the final Release stops them. Either way Wait blocks until every
//     worker goroutine has exited. Wait does not cancel running work — it waits
//     for referrers to finish on their own (so it blocks forever if one never
//     Releases, like sync.WaitGroup.Wait). The pool is reusable afterward.
//
// The pool has no context: it never cancels work. Per-unit cancellation, if
// any, is the unit's concern (it runs under whatever context it chooses).
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/trace"
)

// Releaser, if implemented by a pointer to the per-worker state, is called when
// the owning worker goroutine exits, to return any pooled resources. It is
// optional: states needing no cleanup simply don't implement it.
type Releaser interface {
	Release()
}

// Unit is an opaque unit of work a worker runs. Run receives a pointer to the
// worker's per-worker state (for the unit's whole execution) and must free the
// unit when done — the pool does no per-unit cleanup of its own.
type Unit[E any] interface {
	Run(state *E)
}

// Pool is a demand-driven pool of worker goroutines that run Units, each holding
// per-worker state E. Construct with NewPool; the zero value is not usable.
type Pool[E any] struct {
	newState  func() E            // builds a fresh per-worker state
	workQueue rdvq.Queue[Unit[E]] // units handed to workers
	workers   sync.WaitGroup      // every live worker goroutine

	// Demand-driven, uncapped spawn accounting. `demand` counts queued units
	// not yet picked up; `spawning` counts workers between spawn and securing
	// their first unit (bounds spawn concurrency, not the worker count).
	spawning jobstate.InFlightCounter
	demand   jobstate.InFlightCounter

	// lifecycle, all guarded by mu:
	//   refs    — number of active referrers (e.g. in-flight Waves).
	//   waiting — a Wait is outstanding; stop workers when refs hits zero.
	//   stop    — closed to tell workers to exit; re-armed for reuse. Each
	//             worker captures the current stop at spawn (race-free), so a
	//             re-arm never reaches an already-running worker.
	mu      sync.Mutex
	refs    int
	waiting bool
	stop    chan struct{}
}

// NewPool constructs an empty pool whose workers each hold a per-worker state
// built by newState. It takes no context and no settings: worker behavior is
// fixed (see workerIdleTimeout / spawnConcurrencyLimit). If a pointer to the
// state implements Releaser, it is released when the worker exits.
func NewPool[E any](newState func() E) *Pool[E] {
	traceRegion := "worker.NewPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	p := &Pool[E]{newState: newState, stop: make(chan struct{})}
	trace.Logf(context.Background(), traceRegion, "Pool=%p", p)
	return p
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

// ── Dispatch, demand & spawning ─────────────────────────────────────────────
//
// Spawn is purely demand-driven and uncapped: enqueuing a unit registers demand
// and tries to spawn one worker (subject only to the spawn-CONCURRENCY limit,
// which bounds simultaneous spawns, not total workers). A worker that secures
// its first unit propagates the chain — spawning the next iff demand remains —
// so a burst of N units brings up workers as fast as they are needed and no
// faster.

// Enqueue hands a unit to the workers and ensures at least one worker is coming.
func (p *Pool[E]) Enqueue(sender *rdvq.Sender, u Unit[E]) {
	p.demand.Increment()
	p.workQueue.TryPushBack(sender, u, func() {})
	p.trySpawnWorker()
}

// trySpawnWorker spawns a worker if under the spawn-concurrency limit.
func (p *Pool[E]) trySpawnWorker() bool {
	if spawnConcurrencyLimit < 0 {
		p.spawning.Increment()
	} else if !p.spawning.IncrementIfUnder(spawnConcurrencyLimit) {
		return false
	}
	p.spawnWorker()
	return true
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

	// Per-worker state, owned for the worker's whole life and passed to each
	// unit it runs. Released (if it implements Releaser) when the worker exits.
	state := p.newState()
	defer func() {
		if r, ok := any(&state).(Releaser); ok {
			r.Release()
		}
	}()

	var receiver rdvq.Receiver
	defer receiver.Release()

	var idleTimer *time.Timer
	defer func() {
		if idleTimer != nil {
			timerp.Put(idleTimer)
		}
	}()

	spawning := true
	defer func() {
		if spawning {
			p.spawning.Decrement() // safety: release the spawn slot if we never secured work
		}
	}()

	var u Unit[E]
	for {
		if u == nil {
			if got, ok := p.workQueue.TryPopFront(); ok {
				u = got
			}
		}

		if u != nil {
			// Picked a unit off the queue: account demand (one decrement per
			// pickup balances the increment at Enqueue), then propagate the
			// spawn chain iff demand remains.
			p.demand.Decrement()
			if spawning {
				spawning = false
				if p.demand.IsZero() {
					p.spawning.Decrement()
				} else {
					p.spawnWorker()
				}
			}

			u.Run(&state)
			u = nil
			continue
		}

		// Nothing ready: register as a waiter, arm the idle timer, and block.
		idleTimerCh := p.armIdleTimer(&idleTimer)

		exit := false
		got, ok := p.workQueue.PopFrontFunc(
			&receiver,
			func(inboxCh <-chan Unit[E], outboxWaitCh <-chan rdvq.RenotifyFunc) (result rdvq.PopSelectResult[Unit[E]]) {
				select {
				case received := <-inboxCh:
					result.InboxEmptied(received)
				case rf := <-outboxWaitCh:
					result.OutboxReady(rf)
				case <-idleTimerCh:
					// Idle for the full timeout with nothing to do: exit.
					exit = true
				case <-stop:
					// Definitive termination (Wait is reaping the workers).
					exit = true
				}
				return
			},
		)
		if ok {
			u = got
			exit = false // got work; ignore a concurrent stop/idle signal — drain it first
		}
		if exit {
			return
		}
	}
}

// armIdleTimer arms this worker's idle-exit timer for the blocking select. The
// timeout is a fixed constant — not tunable and without jitter: each idle worker
// independently waits the timeout and then exits, so there is no synchronized
// re-arm stampede to de-correlate (the reason the legacy pool needed jitter plus
// a one-exit-per-window throttle). Scale-to-zero is simply "every worker idle
// for workerIdleTimeout exits."
func (p *Pool[E]) armIdleTimer(idleTimer **time.Timer) <-chan time.Time {
	if *idleTimer == nil {
		*idleTimer = timerp.Get()
	}
	timerp.Reset(*idleTimer, workerIdleTimeout)
	return (*idleTimer).C
}

// ── Fixed worker-behavior tuning (not user-facing) ──────────────────────────

// workerIdleTimeout is how long a worker waits with nothing to do before exiting
// (scale-to-zero). Fixed, not tunable — a sensible default serves all workloads.
const workerIdleTimeout = 1 * time.Second

// spawnConcurrencyLimit caps how many workers may be spawning simultaneously
// (bounds burst spawn, not total workers); <0 means unlimited. The spawn chain
// already self-throttles, so a small constant suffices.
const spawnConcurrencyLimit = 1
