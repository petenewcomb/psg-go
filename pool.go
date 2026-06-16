// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/worker"
	"github.com/petenewcomb/psg-go/internal/workq"
)

// defaultPool is the single, package-level worker substrate that all Waves
// dispatch into. It owns the shared task/funnel work Queue (embedded) and a
// demand-driven pool of goroutines that drive it. There is deliberately no
// exported pool type and no settings — the only public lifecycle surface is Wait.
//
// SEAM (Wave wiring, not yet landed): Wave dispatch will defaultPool.Acquire on
// NewWave, defaultPool.Release when its drain completes, and defaultPool.Post
// work; the per-execution worker context that stamps the unified exEnv into the
// work's borrowed wave context is wave-5b. Until a Wave Acquires it the pool is
// dormant (nothing Posts → no demand → no workers), so newWorkerState's
// placeholder context is never exercised.
var defaultPool = worker.NewPool(newWorkerState)

// Wait blocks until every worker goroutine of the default pool has exited. It
// waits for all in-flight Waves to finish on their own and then reaps the idle
// workers — it does NOT cancel running work (a Wave that never drains makes Wait
// block forever, like sync.WaitGroup.Wait). The pool is reusable afterward.
//
// (Under the planned package rename this becomes streampool.Wait.)
func Wait() { defaultPool.Wait() }

// workerExEnv is the context-free unified execution environment each default-pool
// worker holds: the integration surface (pooled rdvq sender + receiver + group/
// queue stacks) that task and funnel bodies run against. It is deliberately
// job/wave-agnostic — per-execution context (job, wave, cancellation) rides the
// work item, which the worker runs under its borrowed wave context (wave-5b),
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

// newWorkerState builds a fresh worker environment plus the context it runs
// under. PLACEHOLDER context: the real per-execution ctxMeta stamping (the work
// borrows its wave context, and this exEnv is stamped as the
// executionEnvironment) is wave-5b. The pool is dormant until the Wave wiring
// lands, so this context is not yet exercised.
func newWorkerState() (*workerExEnv, context.Context, context.CancelFunc) {
	ee := &workerExEnv{}
	ctx, cancel := context.WithCancel(context.Background())
	return ee, ctx, cancel
}
