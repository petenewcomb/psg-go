// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
)

type Waiters struct {
	Watchers
	rdvq.Waiters
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Init() {
	traceRegion := "workq.Waiters.Init"
	w.Watchers.Init()
	w.Waiters.Init()

	trace.Logf(context.Background(), traceRegion, "Waiters=%p, Watchers=%p, rdvq.Waiters=%p", w, &w.Watchers, &w.Waiters)
}

type WaitBehavior interface {
	ShouldWait() bool
	ShouldBlock() bool
	Block(ctx context.Context, waitCh <-chan RenotifyFunc) (RenotifyFunc, error)
}

func (w *Waiters) Wrap(workFn WorkFunc, behaviorFn func(context.Context) WaitBehavior) WorkFunc {
	wrappedFn := func(ctx context.Context, ex Execution) error {
		defer trace.StartRegion(ctx, "workq.Waiters.wrappedFn").End()
		trace.Logf(ctx, "workq.Waiters.wrappedFn", "starting w=%p", w)

		behavior := behaviorFn(ctx)
		var renotifyFn RenotifyFunc
		shouldBlock := behavior.ShouldBlock
		for behavior.ShouldWait() {
			if renotifyFn != nil {
				// Can't productively use notification receieved, so pass it along
				renotifyFn()
			}

			readyFn := ex.ReadyFn
			trace.Logf(ctx, "workq.Waiters.wrappedFn", "behavior.ShouldWait()=true, readyFn=%p", readyFn)
			if readyFn == nil {
				return nil
			}

			if !shouldBlock() {
				trace.Logf(ctx, "workq.Waiters.wrappedFn", "behavior.ShouldBlock()=false")

				// This execution attempt is a confirmation before the
				// worker waits, so arrange for the readyFn to be called
				w.Add(func(renotifyFn RenotifyFunc) {
					trace.Logf(ctx, "workq.Waiters.wrappedFn", "calling readyFn")
					readyFn(func() {
						trace.Logf(ctx, "workq.Waiters.wrappedFn", "falling back to w.Notify")
						w.Notify(renotifyFn)
					})
					trace.Logf(ctx, "workq.Waiters.wrappedFn", "called readyFn")
				})

				// Recheck condition in case it changed before the readyFn was
				// registered and could receive the notification.
				if !behavior.ShouldWait() {
					trace.Logf(ctx, "workq.Waiters.wrappedFn", "added to watchers but ShouldWait is now false: continuing")
					break
				}

				// Return now without executing the wrapped work function and
				// expect to be called again later (e.g., after ex.ReadyFn has
				// been called)
				trace.Logf(ctx, "workq.Waiters.wrappedFn", "added to watchers, returning nil")
				return nil
			}

			trace.Logf(ctx, "workq.Waiters.wrappedFn", "behavior.ShouldBlock()=true")
			waiter := w.New(func() bool {
				shouldWait := behavior.ShouldWait()
				if shouldWait {
					// Committed now.
					ex.Blocking()
					shouldBlock = func() bool { return true }
				} else {
					trace.Logf(ctx, "workq.Waiters.wrappedFn", "called WaitFunc but ShouldWait is now false: aborting wait")
				}
				return shouldWait
			})

			trace.Logf(ctx, "workq.Waiters.wrappedFn", "behavior.ShouldBlock()=true, calling Block")
			var err error
			renotifyFn = waiter.WaitFuncWithOrphanHandler(w.Notify, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
				var renotifyFn RenotifyFunc
				trace.Logf(ctx, "workq.Waiters.wrappedFn", "calling Block(), waitCh=%p", waitCh)
				renotifyFn, err = behavior.Block(ctx, waitCh)
				trace.Logf(ctx, "workq.Waiters.wrappedFn", "Block() returned renotifyFn=%v, err=%v", renotifyFn, err)
				return renotifyFn
			})
			if err != nil {
				trace.Logf(ctx, "workq.Waiters.wrappedFn", "returning error from Block(): %v", err)
				return err
			}
		}

		trace.Logf(ctx, "workq.Waiters.wrappedFn", "calling workFn")
		err := workFn(ctx, ex)
		trace.Logf(ctx, "workq.Waiters.wrappedFn", "workFn returned err=%v", err)
		return err
	}
	return wrappedFn
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "workq.Waiters.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	// By default, notify watchers before waiters, since watchers typically
	// represent in-process work and waiters represent new work.
	w.Watchers.Notify(func() {
		w.Waiters.Notify(renotifyFn)
	})
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "workq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	w.Watchers.NotifyAll()
	w.Waiters.NotifyAll()
}
