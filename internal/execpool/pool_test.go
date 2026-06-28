// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package execpool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// env is a trivial per-worker execution environment carrying the spawn ordinal, so tests
// can observe how many distinct executors were built.
type env struct{ id int }

// newEnvFactory returns a newState that hands each spawned worker a fresh, numbered env
// and counts spawns.
func newEnvFactory(spawns *atomic.Int64) func() env {
	return func() env { return env{id: int(spawns.Add(1))} }
}

// funcTask adapts a func(env) into a Task.
type funcTask func(env)

func (f funcTask) Run(ee env) { f(ee) }

// TestPool_RunsTask: a single pushed task runs on an executor.
func TestPool_RunsTask(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	ran := make(chan struct{})
	chk.NoError(p.PushBack(context.Background(), funcTask(func(env) { close(ran) })))

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("task never ran")
	}
	chk.GreaterOrEqual(spawns.Load(), int64(1), "at least one executor spawned")
	p.Wait()
}

// TestPool_ManyConcurrent: N blocking tasks all run at once — concurrency is uncapped, so
// every task gets its own executor (block-as-demand), and a barrier proves they overlap.
func TestPool_ManyConcurrent(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	const n = 16
	var running atomic.Int64
	release := make(chan struct{})

	var pushed sync.WaitGroup
	for range n {
		pushed.Add(1)
		go func() {
			defer pushed.Done()
			_ = p.PushBack(context.Background(), funcTask(func(env) {
				running.Add(1)
				<-release // hold the executor until every peer is also running
			}))
		}()
	}

	chk.Eventually(func() bool { return running.Load() == n }, 3*time.Second, time.Millisecond,
		"all %d tasks should run concurrently", n)
	close(release)
	pushed.Wait()
	p.Wait()
}

// TestPool_WaitJoinsThenReusable: Wait stops idle executors and joins them, and the pool
// works again afterward (the poolCtx re-arm).
func TestPool_WaitJoinsThenReusable(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	run := func() {
		ran := make(chan struct{})
		chk.NoError(p.PushBack(context.Background(), funcTask(func(env) { close(ran) })))
		<-ran
	}

	run()
	p.Wait() // stops the idle executor and joins it

	run() // reusable after Wait
	p.Wait()
}

// TestPool_ScaleToZeroAndReuse: after the idle timeout an executor with no work exits, so a
// later push spawns a fresh one. Observed via the spawn counter. Slow (real idle window).
func TestPool_ScaleToZeroAndReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("scale-to-zero exercises the real idle timeout")
	}
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	run := func() {
		ran := make(chan struct{})
		chk.NoError(p.PushBack(context.Background(), funcTask(func(env) { close(ran) })))
		<-ran
	}

	run()
	chk.Positive(spawns.Load())
	before := spawns.Load()

	time.Sleep(workerIdleTimeout + 500*time.Millisecond) // let the idle executors exit

	// With the prior executors scaled to zero, this push finds none waiting and must spawn
	// fresh — if they had persisted, the push would be served with no new spawn.
	run()
	chk.Greater(spawns.Load(), before, "fresh executor(s) spawned — the prior ones scaled to zero")
	p.Wait()
}

// TestPool_CapBoundsSpawns: a long run of instant tasks is served by a reused executor, so
// the spawn-concurrency cap + chain + idle-reuse keep the spawn count far below one-per-task
// (the no-goroutine-glut property the cap exists for). Sequential, so there is never
// concurrent demand — one established executor handles essentially all of it.
func TestPool_CapBoundsSpawns(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	const total = 500
	for range total {
		ran := make(chan struct{})
		chk.NoError(p.PushBack(context.Background(), funcTask(func(env) { close(ran) })))
		<-ran
	}
	chk.Less(spawns.Load(), int64(50), "executors are reused, not spawned per task (no glut)")
	p.Wait()
}

// TestPool_PushBackCtxCancel: a producer parked with no executor available (none spawned to
// take it) unblocks with ctx.Err(). Uses a pool whose executors are kept busy so the new
// push has to park; cancelling its ctx must release it.
func TestPool_PushBackCtxCancel(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// A task that blocks until ctx is cancelled, so its executor never frees to take a peer.
	go func() { done <- p.PushBack(ctx, funcTask(func(env) { <-ctx.Done() })) }()

	// Once the first task is running, push another that we will cancel while it is parked.
	parked := make(chan error, 1)
	go func() { parked <- p.PushBack(ctx, funcTask(func(env) {})) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	// The first push was delivered before cancel (it is running), so it returns nil; the
	// running task then unblocks on ctx.Done. The parked one may either be delivered to a
	// freed/new executor or cancelled — both are acceptable; the pool must not hang.
	chk.NoError(<-done)
	select {
	case <-parked:
	case <-time.After(2 * time.Second):
		t.Fatal("parked PushBack neither delivered nor cancelled")
	}
	p.Wait()
}

// TestPool_ConcurrentExactlyOnce: under many concurrent producers every task runs exactly
// once and the pool quiesces. Run with -race for the memory-ordering check.
func TestPool_ConcurrentExactlyOnce(t *testing.T) {
	chk := require.New(t)
	var spawns atomic.Int64
	p := NewPool(newEnvFactory(&spawns))

	const producers, perProducer = 24, 64
	const total = producers * perProducer

	seen := make([]atomic.Int32, total)
	var ran atomic.Int64
	var pushErrs atomic.Int64

	var wg sync.WaitGroup
	for prod := range producers {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := range perProducer {
				v := base + i
				if err := p.PushBack(context.Background(), funcTask(func(env) {
					seen[v].Add(1)
					ran.Add(1)
				})); err != nil {
					pushErrs.Add(1)
				}
			}
		}(prod * perProducer)
	}
	wg.Wait()
	chk.Zero(pushErrs.Load(), "no PushBack should error with an uncancelled ctx")

	chk.Eventually(func() bool { return ran.Load() == total }, 5*time.Second, time.Millisecond,
		"all %d tasks should run", total)
	p.Wait()

	for v := range total {
		chk.Equal(int32(1), seen[v].Load(), "task %d ran exactly once", v)
	}
}
