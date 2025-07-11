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

type BlockFunc func(ctx context.Context, waitCh <-chan RenotifyFunc) (RenotifyFunc, error)

func (w *Waiters) Wrap(workFn WorkFunc, shouldWait func() bool, shouldBlock func(context.Context) BlockFunc) WorkFunc {
	return func(ctx context.Context, ex Execution) error {
		traceRegion := "workq.Waiters.wrappedFn"
		defer trace.StartRegion(ctx, traceRegion).End()
		trace.Logf(ctx, traceRegion, "Waiters=%p", w)

		var renotifyFn RenotifyFunc
		var blockFn BlockFunc
		blockingCalled := false
		for shouldWait() {

			if renotifyFn != nil {
				// Can't productively use notification receieved, so pass it along
				renotifyFn()
			}

			readyFn := ex.ReadyFn
			if readyFn == nil {
				// Non-blocking execution requested, so we "wait" by exiting
				// without calling the wrapped work function.
				return nil
			}

			blockFn = shouldBlock(ctx)
			if blockFn == nil {
				// Arrange for the readyFn to be called when this Waiters
				// instance is notified.
				w.Add(func(renotifyFn RenotifyFunc) {
					readyFn(func() {
						w.Notify(renotifyFn)
					})
				})

				// Recheck condition in case it changed before the readyFn was
				// registered and could receive the notification.
				if !shouldWait() {
					break
				}

				// Return now without executing the wrapped work function and
				// expect to be called again later (e.g., after ex.ReadyFn has
				// been called)
				return nil
			}

			// Blocking path
			waiter := w.New(func() bool {
				if !shouldWait() {
					return false
				}
				if !blockingCalled {
					blockingCalled = true
					ex.Blocking()
				}
				return true
			})
			var err error
			renotifyFn = waiter.WaitFuncWithOrphanHandler(w.Notify, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
				var renotifyFn RenotifyFunc
				renotifyFn, err = blockFn(ctx, waitCh)
				return renotifyFn
			})
			if err != nil {
				trace.Logf(ctx, traceRegion, "returning error from blockFn: %v", err)
				return err
			}
		}

		return workFn(ctx, ex)
	}
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
