// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/omnipool"
)

type GenericAfterFunc[C any] func(ctx context.Context, wf *GenericWorkflow[C])
type AfterFunc = GenericAfterFunc[Context]

// Workflow establishes a family of tasks, combines, and gathers within a job.
// It is propagated from tasks to gathers or combines, and then to additional
// tasks scattered from those gathers or combines.
type GenericWorkflow[C any] struct {
	pool     *omnipool.Pool[GenericWorkflow[C]]
	refCount atomic.Int32
	parent   parentWorkflow
	ctx      C
	afterFn  GenericAfterFunc[C]
}

type parentWorkflow interface {
	ref()
	unref(context.Context)
}

type Workflow = GenericWorkflow[Context]

// NewGenericWithParent creates a new root workflow with a user-defined context type and after function.
func NewGeneric[C any](wfCtx C, afterFn GenericAfterFunc[C]) *GenericWorkflow[C] {
	return NewGenericWithParent[C, C](nil, wfCtx, afterFn)
}

// NewGenericWithParent creates a new child workflow with a user-defined context
// type and after function. If called with a nil parent, it returns a root
// workflow just like [NewGeneric].
func NewGenericWithParent[PC, C any](parent *GenericWorkflow[PC], wfCtx C,
	afterFn GenericAfterFunc[C]) *GenericWorkflow[C] {
	pool := omnipool.For[GenericWorkflow[C]]()
	wf := pool.Get()
	if parent != nil {
		wf.parent = parent
	}
	wf.pool = pool
	wf.ctx = wfCtx
	wf.afterFn = afterFn
	return wf
}

// Creates a new child workflow.
func WithParent[PC, C any](parent *GenericWorkflow[PC], wfCtx C) *GenericWorkflow[C] {
	return NewGenericWithParent(parent, wfCtx, nil)
}

// WithAfterFunc creates a new child workflow that runs the given function when
// all associated work has finished.
func WithAfterFunc[C any](parent *GenericWorkflow[C], afterFn GenericAfterFunc[C]) *GenericWorkflow[C] {
	return NewGenericWithParent(parent, parent.Ctx(), afterFn)
}

// New creates a new workflow with its own cancellation domain.
func New(ctx context.Context) *Workflow {
	return NewWithParent[Context](nil, ctx)
}

// NewWithParent creates a new child workflow with its own cancellation domain. If called with
// a nil parent, it returns a root workflow just like [New].
func NewWithParent[PC any](parent *GenericWorkflow[PC], ctx context.Context) *Workflow {
	return NewGenericWithParent(parent, newContext(ctx), func(ctx context.Context, wf *Workflow) {
		wf.Ctx().Cancel(ErrWorkflowEnded)
	})
}

// Creates a child workflow that inherits the cancellation domain of its parent.
func WithContext(parent *Workflow, ctx context.Context) *Workflow {
	return NewGenericWithParent(parent, newChildContext(parent.Ctx(), ctx), nil)
}

func (wf *GenericWorkflow[C]) Ctx() C {
	return wf.ctx
}

func (wf *GenericWorkflow[C]) ref() {
	refCount := wf.refCount.Add(1)
	if refCount == 1 && wf.parent != nil {
		wf.parent.ref()
	}
}

func (wf *GenericWorkflow[C]) unref(ctx context.Context) {
	refCount := wf.refCount.Add(-1)
	if refCount < 0 {
		panic("Workflow reference count underflow")
	}
	if refCount > 0 {
		return
	}

	if wf.afterFn != nil {

		// Re-reference while we run afterFn
		wf.refCount.Add(1)
		defer func() {
			refCount := wf.refCount.Add(-1)
			if refCount < 0 {
				panic("Workflow reference count underflow")
			}
			if refCount == 0 {
				// Now we're really done with this workflow instance.
				if wf.parent != nil {
					wf.parent.unref(ctx)
				}
				wf.pool.Put(wf)
			}
		}()

		wf.afterFn(ctx, wf)
	}
}
