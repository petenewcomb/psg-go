// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"

	"github.com/petenewcomb/psg-go/internal/state"
)

// A Pool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a Pool. A Pool must be
// bound to a [Job] before a task can be launched into it.
//
// The zero value of Pool is unbound and has a limit of zero. [NewPool]
// provides a convenient way to create a new pool with a non-zero limit.
type Pool struct {
	concurrencyLimit state.DynamicValue[int]
	job              *Job
	inFlight         state.InFlightCounter
	waiterQueue      state.WaiterQueue
}

// Creates a new [Pool] with the given limit. See [Pool.SetLimit] for the range
// of allowed values and their semantics.
func NewPool(limit int) *Pool {
	p := &Pool{}
	p.concurrencyLimit.Init(limit)
	p.inFlight.Name = fmt.Sprintf("Pool(%p).inFlight", p)
	return p
}

// Sets the active concurrency limit for the pool. A negative value means no
// limit (tasks will always be launched regardless of how many are currently
// running). Zero means no new tasks will be launched (i.e., [Scatter] will block
// indefinitely) until SetLimit is called with a non-zero value. SetLimit is
// always thread-safe, even for a Pool in a single-threaded [Job].
func (p *Pool) SetLimit(limit int) {
	p.concurrencyLimit.Store(limit)
}

func (p *Pool) launch(ctx context.Context, applyBackpressure backpressureFunc, task boundTaskFunc) (bool, error) {
	j := p.job

	// Don't launch if the provided context has been canceled.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Don't launch if the job context has been canceled.
	if err := j.ctx.Err(); err != nil {
		return false, err
	}

	// Try to add to the pool
	limit, _ := p.concurrencyLimit.Load()
	if !p.incrementInFlightIfUnder(limit) {
		if applyBackpressure == nil {
			return false, nil
		}

		type waitResult struct {
			Proceed bool
			Work    workFunc
		}
		wait := func() (waitResult, error) {
			waiter := p.waiterQueue.Add()
			defer waiter.Close()

			// Check again after registering as a waiter, in case capacity
			// became available between the last check and this one.
			limit, limitChangeCh := p.concurrencyLimit.Load()
			if p.incrementInFlightIfUnder(limit) {
				return waitResult{Proceed: true}, nil
			}
			work, err := applyBackpressure(ctx, ctx, waiter, limitChangeCh)
			return waitResult{Work: work}, err
		}
		for {
			res, err := wait()
			if err != nil {
				return false, err
			}
			if res.Work != nil {
				if err := res.Work(ctx); err != nil {
					return false, err
				}
			}
			if res.Proceed {
				break
			}
		}
	}

	// Launch the task in a new goroutine.
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		task(j.ctx)
	}()

	return true, nil
}

type backpressureFunc func(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (workFunc, error)

type boundTaskFunc func(ctx context.Context)

func (p *Pool) incrementInFlightIfUnder(limit int) bool {
	switch {
	case limit < 0:
		p.inFlight.Increment()
		return true
	case limit == 0:
		return false
	default:
		return p.inFlight.IncrementIfUnder(limit)
	}
}

func (p *Pool) decrementInFlight() {
	limit, _ := p.concurrencyLimit.Load()
	if p.inFlight.DecrementAndCheckIfUnder(limit) {
		// Signal any waiting tasks
		p.waiterQueue.Notify()
	}
}
