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
	confirmWaitFn func() bool) (RenotifyFunc, error)

type WaitBehavior struct {
	BlockBehavior
	ShouldWait func() bool
}

func ExecuteOrWait(ctx context.Context, ex Execution, deadline time.Time, notifier *Notifier,
	behavior WaitBehavior, workFn WorkFunc) error {
	traceRegion := "workq.ExecuteOrWait"
	defer trace.StartRegion(ctx, traceRegion).End()

	var renotifyFn RenotifyFunc
	var blockFn BlockFunc

	blockConfirmer := blockConfirmerPool.Get()
	defer blockConfirmerPool.Put(blockConfirmer)
	blockConfirmer.behavior = behavior
	blockConfirmer.ex = ex

	for behavior.ShouldWait() {

		if renotifyFn != nil {
			// Can't productively use notification receieved, so pass it along
			renotifyFn()
		}

		if !ex.ShouldBlockOrPostpone() {
			return nil
		}

		blockFn = behavior.ShouldBlock(ctx)
		if blockFn == nil {
			ex.AddToListeners(&notifier.Listeners)

			// Recheck condition in case it changed before the subscription
			// was registered and could receive the notification.
			if !behavior.ShouldWait() {
				break
			}

			// Return now without executing the wrapped work function and
			// expect to be called again later (e.g., after notification via
			// the subscription)
			return nil
		}

		// Blocking path
		var err error
		renotifyFn, err = blockFn(ctx, deadline, &notifier.Waiters, blockConfirmer.confirmFn)
		if err != nil {
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
