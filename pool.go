// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/execpool"
	"github.com/petenewcomb/streampool/internal/workq"
)

// The dispatch/execution split (Phase 2b C2). Two package-level pools all Waves share:
//
//   - defaultPool — the SCHEDULER (workq.Scheduler): admits work (governor + limiter
//     permits) and hands the admitted BODY to the executor. It never runs a user body, so
//     it is always promptly available (the always-live-dispatcher invariant). It drives the
//     fresh/postponed priority engine plus scheduled flushes; NESTED admission is dropped to
//     its intake Handoff (see [workerExEnv.ExecuteNowOrQueue]) whose block-as-demand spawns
//     workers by COUNTER (bounded, not per-call), while top-level admission runs inline on
//     the per-wave workQueue. (On execpool — "worker.Pool is obsolete", now retired here.)
//   - bodyExecutor — the EXECUTOR: a demand-spawned pool of workers that run the blocking
//     user bodies (task, funnel-accumulate, funnel-flush) handed to them over an unbuffered
//     rendezvous. It MAY block — that is its job — so a blocking body never pins a scheduler.
//     Each *PostWork.Execute PushBacks its body here; the body runs Run(ee) against the
//     worker's environment and frees itself. Funnel-flush is the same shape: the scheduler
//     surfaces a due flush (funnelInstance.Execute) and hands the flush body to the executor
//     (funnelInstance.Run), so a blocking user Flush never pins a scheduler either (CP-B1b).
var defaultPool = workq.NewScheduler()

// bodyExecutor runs user bodies off the scheduler. Per-worker environments are fresh
// workerExEnv values (the body's wave/cancellation ride its borrowed body context, not the
// worker — so the executor needs no per-worker context).
var bodyExecutor = execpool.NewExecutor(func() *workerExEnv { return &workerExEnv{} })

// Wait blocks until every worker goroutine of the default pool has exited. It
// waits for all in-flight Waves to finish on their own and then reaps the idle
// workers — it does NOT cancel running work (a Wave that never drains makes Wait
// block forever, like sync.WaitGroup.Wait). The pool is reusable afterward.
//
// The worker join makes this the one quiescent point at which clearing the
// process-wide ctxpool reuse caches is safe: with no workers left there are no
// body-context borrowers, so the caches now only pin cached child contexts (and
// their values) until each parent ctx is GC'd via AfterFunc. Clearing reclaims
// them eagerly. ctxpool.Clear swaps in a fresh map, so a Wave dispatched after
// Wait returns (the pool is reusable) simply repopulates clean.
//
// (Under the planned package rename this becomes streampool.Wait.)
func Wait() {
	// Reap the executor first (it runs bodies, which the scheduler feeds), then the
	// scheduler. Both join only their idle workers — neither cancels in-flight work — so
	// this is safe only at quiescence (every Wave drained on its own, as SkimAll enforces).
	bodyExecutor.Wait()
	defaultPool.Wait()
	ctxpool.Clear()
}

// workerExEnv is the context-free unified execution environment each default-pool
// worker holds: the integration surface (pooled rdvq sender + receiver + group/
// queue stacks) that task and funnel bodies run against. It is deliberately
// wave-agnostic — per-execution context (wave, cancellation) rides the
// work item, which the worker runs under its borrowed body context,
// stamping this exEnv in. taskExEnv + cpWorker collapse into this as the task and
// funnel engines are cut over onto the shared pool. Lock/Unlock are no-ops: the
// exEnv is per-goroutine and runs one body at a time.
type workerExEnv struct {
	integrationExEnv
}

var _ executionEnvironment = (*workerExEnv)(nil)

func (ee *workerExEnv) Lock()   {}
func (ee *workerExEnv) Unlock() {}

// ExecuteNowOrQueue is the synchronous-dispatch entry a body uses to run sub-work — but on
// an EXECUTOR goroutine it must NOT run admission inline. Inline admission would, on success,
// PushBack the sub-body to the executor and block THIS executor goroutine waiting for another
// executor: a self-deadlock once the executor pool is saturated. So drop the scatter-work to
// the scheduler's intake: Post blocks only until a scheduler worker ACCEPTS it (the intake
// Handoff's block-as-demand spawns one by COUNTER if none waits — bounded, NOT a per-call
// spawn), then the scheduler runs admission and the blocking handoff off this goroutine.
// Brief, deadlock-free (the scheduler is a separate pool), and demand-bounded. (ex is unused:
// the scatter-work re-runs under the scheduler controller's own Execution.)
func (ee *workerExEnv) ExecuteNowOrQueue(ctx context.Context, _ workq.Execution, work workq.Work) error {
	return defaultPool.Post(ctx, work)
}
