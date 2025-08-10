// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
)

type Waiter = rdvq.Waiter

type Waiters struct {
	Coordinator
	rdvq.Waiters
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Init() {
	traceRegion := "workq.Waiters.Init"
	trace.Logf(context.Background(), traceRegion,
		"Waiters=%p, Coordinator=%p, rdvq.Waiters=%p",
		w, &w.Coordinator, &w.Waiters)

	w.Coordinator.Init()
	w.Waiters.Init()
}

type BlockFunc func(ctx context.Context, deadline time.Time, waiters *Waiters,
	confirmWaitFn func() bool) (RenotifyFunc, error)

type WaitBehavior struct {
	BlockBehavior
	ShouldWait func() bool
}

func (w *Waiters) Execute(ctx context.Context, ex Execution, deadline time.Time,
	behavior WaitBehavior, workFn WorkFunc) error {
	traceRegion := "workq.Waiters.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	var renotifyFn RenotifyFunc
	var blockFn BlockFunc
	blockingCalled := false
	for behavior.ShouldWait() {

		if renotifyFn != nil {
			// Can't productively use notification receieved, so pass it along
			renotifyFn()
		}

		if !ex.ShouldBlockOrSubscribe() {
			return nil
		}

		blockFn = behavior.ShouldBlock(ctx)
		if blockFn == nil {
			ex.Subscribe(&w.Coordinator)

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
		confirmFn := func() bool {
			if !behavior.ShouldWait() {
				return false
			}
			if !blockingCalled {
				blockingCalled = true
				ex.Blocking()
			}
			return true
		}
		var err error
		renotifyFn, err = blockFn(ctx, deadline, w, confirmFn)
		if err != nil {
			trace.Logf(ctx, traceRegion, "returning error from blockFn: %v", err)
			return err
		}
	}

	return workFn(ctx, ex)
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "workq.Waiters.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	// By default, notify watchers before waiters, since watchers typically
	// represent in-process work and waiters represent new work.
	w.Coordinator.Notify(func() {
		w.Waiters.Notify(renotifyFn)
	})
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "workq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	w.Coordinator.NotifyAll()
	w.Waiters.NotifyAll()
}
