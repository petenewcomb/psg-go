// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/execpool"
	"github.com/petenewcomb/streampool/internal/worker"
	"github.com/petenewcomb/streampool/internal/workq"
)

// defaultPool is the single, package-level worker substrate that all Waves
// dispatch into. It owns the shared task/funnel work Queue (embedded) and a
// demand-driven pool of goroutines that drive it. There is deliberately no
// exported pool type and no settings — the only public lifecycle surface is Wait.
//
// SEAM (Wave wiring, not yet landed): Wave dispatch will defaultPool.Acquire on
// first use, defaultPool.Release when its drain completes, and defaultPool.Post
// work; the per-execution worker context stamps the unified exEnv into the
// work's borrowed body context. Until a Wave Acquires it the pool is
// dormant (nothing Posts → no demand → no workers), so newWorkerState's
// placeholder context is never exercised.
var defaultPool = worker.NewPool(newWorkerState)

// bodyExecutorPool is the execution half of the dispatch/execution split (Phase 2b C2):
// a demand-spawned pool of workers that run user bodies handed to them over an unbuffered
// rendezvous. A body PushBack'd here (by taskWork/funnelWork.Execute on a scheduler
// worker) runs to completion on an executor goroutine and Frees itself — so a blocking
// body no longer pins a scheduler worker, keeping admission always live. Bodies reach an
// executor with their per-worker environment passed directly (no workerEnvKey ctx walk);
// the body still runs under its own borrowed body ctx, stamping the executor's env in.
// (Distinct from executorPool in ctxmeta.go, which pools the workq.Executor drive helper.)
var bodyExecutorPool = execpool.NewExecutor(func() *workerExEnv { return &workerExEnv{} })

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
	// Quiesce the scheduler first (it stops admitting and handing bodies off), then the
	// executor (it drains the bodies already handed off). Bodies are strictly downstream
	// of scheduler admission, so this order is the safe one: a scheduler worker blocked
	// handing a body off is still served by the live executor during defaultPool.Wait().
	defaultPool.Wait()
	bodyExecutorPool.Wait()
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

// ExecuteNowOrQueue runs work synchronously on this goroutine, or queues it on
// the shared engine if it postpones — the synchronous-dispatch entry a body uses
// to run sub-work inline. Routed to the shared Queue the default pool owns.
func (ee *workerExEnv) ExecuteNowOrQueue(ctx context.Context, ex workq.Execution, work workq.Work) error {
	return defaultPool.ExecuteNowOrQueue(ctx, ex, work)
}

// workerEnvKey carries the worker's execution environment E on its worker context
// so a body executing on this worker can retrieve E (to stamp into the borrowed
// body context's meta) without it living in a ctxMeta. The worker ctx itself holds
// NO ctxMeta — bodies run under a borrowed body ctx, never the worker ctx.
type workerEnvKey struct{}

// newWorkerState builds a fresh worker environment plus the worker context it runs
// idle/cancel selects under. The context derives from the global pool's poolCtx
// (so definitive teardown cancels idle workers by ancestry) and carries E under
// workerEnvKey. Bodies do NOT run under this context — they run under a body
// context borrowed at dispatch (borrowBodyContext); this context is only the
// worker's own idle/cancel signal.
func newWorkerState(poolCtx context.Context) (*workerExEnv, context.Context, context.CancelFunc) {
	ee := &workerExEnv{}
	ctx, cancel := context.WithCancel(poolCtx)
	ctx = context.WithValue(ctx, workerEnvKey{}, ee)
	return ee, ctx, cancel
}

// workerEnvFromContext returns the worker's execution environment carried on a
// worker context, or nil if ctx is not a worker context (e.g. a top-level or
// legacy worker context). A body uses it to obtain the E to run against.
func workerEnvFromContext(ctx context.Context) *workerExEnv {
	ee, _ := ctx.Value(workerEnvKey{}).(*workerExEnv)
	return ee
}
