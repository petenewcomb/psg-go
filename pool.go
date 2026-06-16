// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import "github.com/petenewcomb/psg-go/internal/worker"

// defaultPool is the package-level worker substrate that all Waves dispatch
// into. Its per-worker state is taskExEnv — the execution environment (pooled
// rdvq sender + group slot) a task body runs against. There is deliberately no
// exported pool type and no settings: the only public lifecycle surface is Wait.
//
// SEAM (Wave wiring, not yet landed): Wave dispatch will defaultPool.Acquire on
// NewWave and defaultPool.Release when its drain completes, and Enqueue
// worker.Unit values — *taskWork implementing Run(*taskExEnv), where the wave
// logic (borrow the wave exec ctx, set group, run the body, free) lives.
var defaultPool = worker.NewPool(func() taskExEnv { return taskExEnv{} })

// Wait blocks until every worker goroutine of the default pool has exited. It
// waits for all in-flight Waves to finish on their own and then reaps the idle
// workers — it does NOT cancel running work (a Wave that never drains makes Wait
// block forever, like sync.WaitGroup.Wait). The pool is reusable afterward.
//
// (If an exported Pool is ever reintroduced, this becomes sugar for
// DefaultPool.Wait — and, under the planned package rename, streampool.Wait.)
func Wait() { defaultPool.Wait() }
