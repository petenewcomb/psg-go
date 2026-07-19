// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/rdvq"
)

type Waiters = rdvq.Waiters
type Notifier = rdvq.Notifier

type BlockFunc func(ctx context.Context, deadline time.Time, waiters *Waiters,
	confirmWaitFn func() bool) error

type WaitBehavior struct {
	BlockBehavior
	ShouldWait func() bool
}

func ExecuteOrWait(ctx context.Context, ex Execution, deadline time.Time, notifier *Notifier,
	behavior WaitBehavior, workFn WorkFunc) error {
	traceRegion := "workq.ExecuteOrWait"
	defer trace.StartRegion(ctx, traceRegion).End()

	var blockFn BlockFunc

	blockConfirmer := blockConfirmerPool.Get()
	defer blockConfirmerPool.Release(blockConfirmer)
	blockConfirmer.behavior = behavior
	blockConfirmer.ex = ex

	for behavior.ShouldWait() {

		if !ex.ShouldBlockOrPostpone() {
			return nil
		}

		blockFn = behavior.ShouldBlock(ctx)
		if blockFn == nil {
			ex.Listener.AddTo(&notifier.Listeners)

			// Recheck condition in case it changed before the planting could
			// receive a wake.
			if !behavior.ShouldWait() {
				break
			}

			// Return now without executing the wrapped work function and
			// expect to be called again later (e.g., after a wake via the
			// planted relay)
			return nil
		}

		// Blocking path
		if err := blockFn(ctx, deadline, &notifier.Waiters, blockConfirmer.confirmFn); err != nil {
			trace.Logf(ctx, traceRegion, "returning error from blockFn: %v", err)
			return err
		}
	}

	return workFn(ctx, ex)
}

var blockConfirmerPool = omnipool.For[blockConfirmer]()

type blockConfirmer struct {
	behavior       WaitBehavior
	blockingCalled bool
	ex             Execution

	confirmFn func() bool // avoid reallocating closure
}

func (c *blockConfirmer) Init() {
	c.confirmFn = c.confirm
}

func (c *blockConfirmer) Reset() {
	*c = blockConfirmer{
		confirmFn: c.confirmFn,
	}
}

func (c *blockConfirmer) confirm() bool {
	if !c.behavior.ShouldWait() {
		return false
	}
	if !c.blockingCalled {
		c.blockingCalled = true
		c.ex.Blocking()
	}
	return true
}
