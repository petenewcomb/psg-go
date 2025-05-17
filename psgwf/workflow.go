// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"sync"
	"sync/atomic"
)

// Workflow provides context for a family of tasks within a job. It is
// propagated from tasks to gathers or combines, and then to additional tasks
// scattered from those gathers or combines.
type Workflow struct {
	Ctx   context.Context
	inner *wfInner
}

type wfInner struct {
	refCount atomic.Int64
	cancel   context.CancelCauseFunc
}

var wfInnerPool sync.Pool

func New(ctx context.Context) Workflow {
	inner, _ := wfInnerPool.Get().(*wfInner)
	if inner == nil {
		inner = &wfInner{}
	}
	ctx, inner.cancel = context.WithCancelCause(ctx)
	inner.refCount.Store(1)
	return Workflow{
		Ctx:   ctx,
		inner: inner,
	}
}

func (wf *Workflow) Cancel(cause error) {
	wf.inner.cancel(cause)
}

func (wf *Workflow) ref() {
	wf.inner.refCount.Add(1)
}

func (wf *Workflow) Unref() {
	if wf.inner.refCount.Add(-1) == 0 {
		inner := wf.inner
		wf.inner = nil

		inner.cancel(ErrWorkflowComplete)
		inner.cancel = nil
		wfInnerPool.Put(inner)
	}
}
