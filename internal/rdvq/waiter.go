// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// WaitSelectFunc handles select operations on wait channels, returning the the
// received RenotifyFunc or nil if the select exited without receiving one.
type WaitSelectFunc func(waitCh <-chan RenotifyFunc) RenotifyFunc

type NotifyFunc func(RenotifyFunc)

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFuncWithOrphanHandler(
	confirmFn func() bool,
	orphanFn NotifyFunc,
	selectFn WaitSelectFunc,
) RenotifyFunc {
	traceRegion := "rdvq.Waiter.WaitFuncWithOrphanHandler"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		return selectFn(nil)
	}
	var renotifyFn RenotifyFunc
	w.q.PopFrontFunc(wp,
		orphanFn,
		func(ch <-chan RenotifyFunc) SelectResult {
			if confirmFn() {
				renotifyFn = selectFn(ch)
				if renotifyFn != nil {
					return SelectInboxEmptied
				}
			}
			return SelectAborted
		},
	)
	return renotifyFn
}

func (w *Waiters) WaitFunc(confirmFn func() bool, selectFn WaitSelectFunc) RenotifyFunc {
	return w.WaitFuncWithOrphanHandler(confirmFn, w.Notify, selectFn)
}

func (w *Waiters) WaitWithOrphanHandler(
	ctx context.Context,
	confirmFn func() bool,
	orphanFn NotifyFunc,
) (RenotifyFunc, error) {
	traceRegion := "rdvq.Waiter.WaitWithOrphanHandler"

	var err error
	renotifyFn := w.WaitFuncWithOrphanHandler(confirmFn, orphanFn, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
		select {
		case renotifyFn := <-waitCh:
			trace.Logf(ctx, traceRegion, "received renotifyFn from waitCh=%p", waitCh)
			return renotifyFn
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
		}
		return nil
	})
	return renotifyFn, err
}

func (w *Waiters) Wait(ctx context.Context, confirmFn func() bool) (RenotifyFunc, error) {
	return w.WaitWithOrphanHandler(ctx, confirmFn, w.Notify)
}
