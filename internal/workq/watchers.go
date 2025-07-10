// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

type NotifyFunc = rdvq.NotifyFunc

type Watchers struct {
	q nbcq.Queue[NotifyFunc]
}

func (w *Watchers) Init() {
	w.q.Init(watchersPool)
}

//nolint:contextcheck // background context used only for tracing
func (w *Watchers) Add(notifyFn NotifyFunc) {
	traceRegion := "workq.Watchers.Add"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Watchers=%p", w)

	if notifyFn != nil {
		w.q.PushBack(watchersPool, notifyFn)
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *Watchers) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "workq.Watchers.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Watchers=%p", w)

	if notifyFn, ok := w.q.PopFront(watchersPool); ok {
		notifyFn(func() {
			w.Notify(renotifyFn)
		})
	} else {
		renotifyFn()
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *Watchers) NotifyAll() {
	traceRegion := "workq.Watchers.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Watchers=%p", w)

	for {
		notifyFn, ok := w.q.PopFront(watchersPool)
		if !ok {
			break
		}
		notifyFn(func() {})
	}
}

var watchersPool = &nbcq.Pool[NotifyFunc]{}
