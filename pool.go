// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/state"
)

// backpressureMode defines how Pool.launch handles a full pool
type backpressureMode int

const (
	backpressureDecline backpressureMode = iota // Return without launching
	backpressureGather                          // Block and gather tasks
	backpressureWaiter                          // Block until notified by a waiter
)

// A Pool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a Pool. A Pool must be
// bound to a [Job] before a task can be launched into it.
//
// The zero value of Pool is unbound and has a limit of zero. [NewPool]
// provides a convenient way to create a new pool with a non-zero limit.
type Pool struct {
	limit          atomic.Int64
	job            *Job
	inFlight       state.InFlightCounter
	waitersChannel chan chan<- struct{}
}

// Creates a new [Pool] with the given limit. See [Pool.SetLimit] for the range
// of allowed values and their semantics.
func NewPool(limit int) *Pool {
	p := &Pool{
		waitersChannel: make(chan chan<- struct{}),
	}
	p.limit.Store(int64(limit))
	return p
}

// registerWaiter registers for notification when capacity becomes available.
// Returns a receive-only channel that will be signaled when capacity is available.
func (p *Pool) registerWaiter(ctx context.Context) (<-chan struct{}, error) {
	// Buffer size 1 ensures signals aren't lost if sent before receiver is ready
	notificationChannel := make(chan struct{}, 1)

	// Register this waiter with the pool
	select {
	case p.waitersChannel <- notificationChannel:
		return notificationChannel, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.job.ctx.Done():
		return nil, p.job.ctx.Err()
	}
}

// notifyWaiter signals one waiter (if any) that capacity is available.
func (p *Pool) notifyWaiter() {
	select {
	case notificationChannel := <-p.waitersChannel:
		select {
		case notificationChannel <- struct{}{}:
		default:
			// Receiver might be gone or already notified
		}
	default:
		// No waiters
	}
}

// Sets the active concurrency limit for the pool. A negative value means no
// limit (tasks will always be launched regardless of how many are currently
// running). Zero means no new tasks will be launched (i.e., [Scatter] will block
// indefinitely) until SetLimit is called with a non-zero value. SetLimit is
// always thread-safe, even for a Pool in a single-threaded [Job].
func (p *Pool) SetLimit(limit int) {
	if p.limit.Swap(int64(limit)) == 0 && limit != 0 {
		j := p.job
		if j != nil {
			j.wakeGatherers()
		}
	}
}

// waitForCapacity waits until a slot may be available in the pool.
// Returns nil when capacity might be available, or an error if waiting was interrupted.
func (p *Pool) waitForCapacity(ctx context.Context) error {
	j := p.job

	notificationChannel, err := p.registerWaiter(ctx)
	if err != nil {
		return err
	}

	// Wait for capacity notification or context cancellation
	select {
	case <-notificationChannel:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-j.ctx.Done():
		return j.ctx.Err()
	}
}

func (p *Pool) launch(ctx context.Context, backpressureMode backpressureMode, task boundTaskFunc) (bool, error) {
	j := p.job
	if j == nil {
		panic("pool not bound to a job")
	}

	// Validate backpressureMode early
	if backpressureMode < backpressureDecline || backpressureMode > backpressureWaiter {
		panic("invalid backpressure mode")
	}

	if j.isTaskContext(ctx) {
		// Don't launch if the provided context is a task context within the
		// current job, since that may lead to deadlock.
		panic("Scatter called from within TaskFunc; move call to GatherFunc instead")
	}

	// Don't launch if the provided context has been canceled.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Don't launch if the job context has been canceled.
	if err := j.ctx.Err(); err != nil {
		return false, err
	}

	// Register the task with the job to make sure that any calls to gather will
	// block until the task is completed.
	j.inFlight.Increment()

	// Bookkeeping: make sure that the job-scope count incremented above gets
	// decremented unless the launch actually happens
	launched := false
	defer func() {
		if !launched {
			j.decrementInFlight()
		}
	}()

	// Try to add to the pool
	for !p.incrementInFlightIfUnderLimit() {
		switch backpressureMode {
		case backpressureDecline:
			// Just decline to launch (TryScatter behavior)
			return false, nil
		case backpressureGather:
			// Use gathering to create backpressure and make room for new tasks
			_, err := j.GatherOne(ctx)
			if err != nil {
				return false, err
			}
		case backpressureWaiter:
			// Wait for capacity without requiring gathering (for Combiner)
			if err := p.waitForCapacity(ctx); err != nil {
				return false, err
			}
		}
	}

	// Launch the task in a new goroutine.
	launched = true
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		task(j.ctx)
	}()

	return true, nil
}

type boundTaskFunc func(ctx context.Context)

func (p *Pool) incrementInFlightIfUnderLimit() bool {
	limit := p.limit.Load()
	switch {
	case limit < 0:
		p.inFlight.Increment()
		return true
	case limit == 0:
		return false
	default:
		return p.inFlight.IncrementIfUnder(int(limit))
	}
}

func (p *Pool) decrementInFlight() {
	if p.inFlight.DecrementAndCheckIfUnder(int(p.limit.Load())) {
		// Signal any waiting tasks that don't use gather-based backpressure
		p.notifyWaiter()
	}
}
