// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingLoop is a minimal WorkerLoop[struct{}] that pulls thunks from workCh, runs them,
// surrenders its spawn slot at first secure / on idle, and idles out after idleTimeout with
// nothing to do — exercising the generic Core in isolation from workq/rdvq.
func countingLoop(workCh <-chan func(), processed *atomic.Int64, idleTimeout time.Duration) WorkerLoop[struct{}] {
	return func(ctx context.Context, _ struct{}, releaseSpawn func(bool), stop <-chan struct{}) {
		for {
			select {
			case <-stop:
				releaseSpawn(false)
				return
			case <-ctx.Done():
				releaseSpawn(false)
				return
			case w := <-workCh:
				releaseSpawn(true) // secured work — extend the spawn chain (idempotent after first)
				processed.Add(1)
				w()
			case <-time.After(idleTimeout):
				releaseSpawn(false) // idled out — scale to zero
				return
			}
		}
	}
}

func newTestCore(workCh <-chan func(), processed *atomic.Int64) *Core[struct{}] {
	newState := func(poolCtx context.Context) (struct{}, context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(poolCtx)
		return struct{}{}, ctx, cancel
	}
	return newCore(newState, countingLoop(workCh, processed, 50*time.Millisecond))
}

// TestCore_DemandSpawnProcessesAllWork: demand spawns workers that drain a backlog, and
// the spawn chain ramps the pool up under load.
func TestCore_DemandSpawnProcessesAllWork(t *testing.T) {
	chk := require.New(t)
	const N = 300
	workCh := make(chan func(), N)
	var processed atomic.Int64
	c := newTestCore(workCh, &processed)

	c.Acquire()
	defer func() { c.Release(); c.Wait() }()

	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		workCh <- wg.Done
	}
	c.TrySpawn() // one demand pulse; the spawn chain ramps the rest while work remains

	wg.Wait() // every item ran
	chk.Equal(int64(N), processed.Load())
}

// TestCore_WaitJoinsAndReuses: Wait joins all workers (idle-exited or stopped), and the
// Core is reusable afterward (poolCtx re-armed).
func TestCore_WaitJoinsAndReuses(t *testing.T) {
	chk := require.New(t)
	workCh := make(chan func(), 64)
	var processed atomic.Int64
	c := newTestCore(workCh, &processed)

	run := func(n int) {
		c.Acquire()
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			workCh <- wg.Done
		}
		c.TrySpawn()
		wg.Wait()
		c.Release()
		c.Wait() // joins every worker; must return (no leak / no deadlock)
	}

	run(20)
	chk.Equal(int64(20), processed.Load())
	run(20) // reuse after Wait
	chk.Equal(int64(40), processed.Load())
}

// TestCore_ScaleToZeroWhenIdle: with referrers still held but no work, workers idle out on
// their own (scale to zero) — so a later Wait returns promptly with nothing to join.
func TestCore_ScaleToZeroWhenIdle(t *testing.T) {
	chk := require.New(t)
	workCh := make(chan func(), 8)
	var processed atomic.Int64
	c := newTestCore(workCh, &processed)

	c.Acquire()
	done := make(chan struct{})
	workCh <- func() { close(done) }
	c.TrySpawn()
	<-done // a worker spawned and ran the item

	// No more work: the worker(s) idle out (idleTimeout=50ms) even though a referrer is
	// still held. Give them well past the timeout, then Wait should join instantly.
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	c.Release()
	c.Wait()
	chk.Less(time.Since(start), 100*time.Millisecond, "idle workers should already have exited")
}
