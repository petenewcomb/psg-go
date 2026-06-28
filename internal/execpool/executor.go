// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package execpool

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/rdvq"
)

// Task is a unit of executable work run by an executor. Run executes the body against the
// per-worker execution environment E and is responsible for ALL of its own cleanup (a Task
// that pools itself does so at the end of Run). The executor calls Run exactly once per
// handed-off Task and never touches the Task otherwise — Run is the entire contract.
type Task[E any] interface {
	Run(ee E)
}

// Executor is the execution half of the dispatch/execution split: a [Pool] of workers that
// run blocking [Task] bodies handed to them over an unbuffered rendezvous. It may block —
// that is its job — so the schedulers that PushBack here are never stuck behind a body.
// Construct with [NewExecutor]; the zero value is not usable.
type Executor[E any] struct {
	pool    *Pool[*executorWorker[E]]
	handoff rdvq.Handoff[Task[E]]
}

// NewExecutor constructs an executor pool. Per-worker environments are built by newState;
// each Task.Run is handed the E of the worker that runs it.
func NewExecutor[E any](newState func() E) *Executor[E] {
	x := &Executor[E]{}
	x.handoff.Init()
	x.pool = NewPool(func() *executorWorker[E] {
		return &executorWorker[E]{handoff: &x.handoff, ee: newState()}
	})
	return x
}

// PushBack hands task to an executor, blocking until one takes it or ctx is cancelled. If no
// executor is waiting it fires demand (block-as-demand): the producer parks holding the task
// and TrySpawn (capped) brings up an executor, which takes it directly — no buffer dwell.
// The cap means a burst of producers does not spawn a goroutine glut; the chain ramps as
// fast as executors actually pick work up.
func (x *Executor[E]) PushBack(ctx context.Context, task Task[E]) error {
	var err error
	if x.handoff.PushBackFunc(task, func(waitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
		// selectFn runs only when no executor was waiting — i.e. the producer is about to
		// park — so fire demand. TrySpawn is capped, so this is a kick the spawn chain ramps
		// from, not a spawn-per-producer.
		x.pool.TrySpawn()
		var rf rdvq.RenotifyFunc
		rf, err = rdvq.BasicWaitSelect(ctx, waitCh)
		return rf
	}) {
		return nil
	}
	return err
}

// Acquire, Release, and Wait are the executor's lifecycle, delegated to the underlying pool.
func (x *Executor[E]) Acquire() { x.pool.Acquire() }
func (x *Executor[E]) Release() { x.pool.Release() }
func (x *Executor[E]) Wait()    { x.pool.Wait() }

// executorWorker is the executor's [Worker]: it waits on the shared Handoff, runs the task
// it receives, and is single-threaded (one goroutine), so it stashes the received task
// between Wait and Work.
type executorWorker[E any] struct {
	handoff *rdvq.Handoff[Task[E]]
	ee      E
	task    Task[E] // received in Wait, run in Work
}

// Wait blocks on the Handoff for the next task, composing the pool's idle channel and
// workerCtx.Done() (stop) into the receive select. It stashes the task and reports whether
// one arrived (false ⇒ idle-out or stop).
func (w *executorWorker[E]) Wait(workerCtx context.Context, idle <-chan time.Time) bool {
	task, ok := w.handoff.PopFrontFunc(func(inboxCh <-chan Task[E]) (Task[E], bool) {
		select {
		case t := <-inboxCh:
			return t, true
		case <-idle:
			return nil, false // idle scale-to-zero
		case <-workerCtx.Done():
			return nil, false // definitive teardown (Wait)
		}
	})
	w.task = task
	return ok
}

// Work runs the task Wait received against this worker's environment.
func (w *executorWorker[E]) Work(_ context.Context) {
	task := w.task
	w.task = nil
	task.Run(w.ee)
}

// Close has nothing to release: the env is reused for this worker's whole life and a Task
// frees itself in Run. (A pooled executorWorker would recycle here.)
func (w *executorWorker[E]) Close(_ context.Context) {}
