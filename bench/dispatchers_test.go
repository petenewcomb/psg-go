// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package bench

import (
	"context"
	"sync"

	"github.com/petenewcomb/streampool"
)

// ── Baseline: unbounded goroutines ────────────────────────────────────────────
// A goroutine per task, no cap. Lowest dispatch latency (nothing to wait for) but
// goroutine count and memory grow with offered load — the explosion bounded pools
// exist to prevent. The control for "what does no backpressure cost in memory?"

type unboundedDispatcher struct{ wg sync.WaitGroup }

func (d *unboundedDispatcher) name() string { return "unbounded" }
func (d *unboundedDispatcher) start(int)    {}
func (d *unboundedDispatcher) submit(_ context.Context, task func()) {
	d.wg.Add(1)
	go func() { defer d.wg.Done(); task() }()
}
func (d *unboundedDispatcher) drain() { d.wg.Wait() }
func (d *unboundedDispatcher) stop()  {}

// ── Baseline: channel semaphore ───────────────────────────────────────────────
// A goroutine per task, gated by a buffered-channel semaphore of size capacity.
// Bounds concurrency but still spawns a goroutine per task (parked on the slow
// body), so the goroutine count tracks in-flight tasks, not capacity.

type semaphoreDispatcher struct {
	sem chan struct{}
	wg  sync.WaitGroup
}

func (d *semaphoreDispatcher) name() string { return "chan-semaphore" }
func (d *semaphoreDispatcher) start(capacity int) {
	d.sem = make(chan struct{}, capacity)
}
func (d *semaphoreDispatcher) submit(_ context.Context, task func()) {
	d.sem <- struct{}{} // blocks at capacity (backpressure)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() { <-d.sem }()
		task()
	}()
}
func (d *semaphoreDispatcher) drain() { d.wg.Wait() }
func (d *semaphoreDispatcher) stop()  {}

// ── Baseline: naive fixed worker pool ─────────────────────────────────────────
// capacity worker goroutines draining a task channel. This is the
// "dispatcher-pinned / no split" stand-in: a worker running a slow body is
// unavailable to start the next task, so under a heavy body tail the dispatch
// latency tail tracks the body tail. The apples-to-apples control streampool's
// split is meant to beat on responsiveness.

type naivePoolDispatcher struct {
	tasks chan func()
	wg    sync.WaitGroup
}

func (d *naivePoolDispatcher) name() string { return "naive-pool" }
func (d *naivePoolDispatcher) start(capacity int) {
	d.tasks = make(chan func(), capacity)
	for range capacity {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			for t := range d.tasks {
				t()
			}
		}()
	}
}
func (d *naivePoolDispatcher) submit(_ context.Context, task func()) {
	d.tasks <- task // blocks when the buffer is full and all workers are busy
}
func (d *naivePoolDispatcher) drain() {
	close(d.tasks)
	d.wg.Wait()
}
func (d *naivePoolDispatcher) stop() {}

// ── Subject: streampool ───────────────────────────────────────────────────────
// A Wave with a Launcher capped at capacity via a semaphore limiter. Bodies run on
// the shared executor pool (the dispatch/execution split); the scheduler that
// admits work is never pinned by a blocking body. Submit applies backpressure
// (skims + blocks on the limiter) exactly like the other bounded systems.

type streampoolDispatcher struct {
	wave     streampool.Wave
	launcher streampool.Launcher[func()]
}

func (d *streampoolDispatcher) name() string { return "streampool" }
func (d *streampoolDispatcher) start(capacity int) {
	var opts []streampool.OpOption
	if capacity > 0 {
		opts = append(opts, streampool.WithLimits(streampool.NewSemaphore(capacity)))
	}
	d.launcher = streampool.NewFnLauncher(
		func(_ context.Context, task func(), _ error) error {
			task()
			return nil
		}, opts...).In(&d.wave)
}
func (d *streampoolDispatcher) submit(ctx context.Context, task func()) {
	_ = d.launcher.Submit(ctx, task)
}
func (d *streampoolDispatcher) drain() {
	_ = d.wave.CloseAndSkimAll(context.Background())
}
func (d *streampoolDispatcher) stop() {}
