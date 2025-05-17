// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"math/rand/v2"
	"sync"
)

// AfterFunc is a function called after a workflow completes. It receives:
// - ctx: the context from the gather, combine, or flush operation that performed the final [Workflow.Unref]
// - wf: the completed workflow with its cancelled context (usage other than accessing Ctx will panic)
//
// This allows operations that:
//   - Release resources or commit source transactions
//   - Scatter new tasks using the provided ctx with a new workflow (via [New])
//   - Examine the cancellation cause via the workflow context
//   - Collect values from the workflow context
//
// Multiple AfterFuncs may be registered on a single workflow. Their execution order
// is explicitly randomized to prevent dependencies on ordering.
type AfterFunc func(ctx context.Context, wf *Workflow)

// Workflow provides context for a family of tasks within a job. It is
// propagated from tasks to gathers or combines, and then to additional tasks
// scattered from those gathers or combines.
type Workflow struct {
	ctx        context.Context
	mu         sync.Mutex
	refCount   int
	cancel     context.CancelCauseFunc
	afterFuncs []AfterFunc
}

var wfPool sync.Pool = sync.Pool{
	New: func() any {
		return &Workflow{}
	},
}

func New(ctx context.Context) *Workflow {
	wf, _ := wfPool.Get().(*Workflow)
	wf.ctx = ctx
	return wf
}

func (wf *Workflow) Ctx() context.Context {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	return wf.ctx
}

func (wf *Workflow) Cancel(cause error) {
	wf.mu.Lock()
	// It's OK to initialize cancel here without incrementing reference count
	// because we're also going to call it and therefore release the resources
	// it holds.
	wf.ensureInitCancel()
	cancel := wf.cancel
	wf.mu.Unlock()
	cancel(cause)
}

// AfterFunc registers a function to be called after the workflow completes.
// Multiple AfterFuncs may be registered and their execution order is randomized.
// See the [AfterFunc] type documentation for details on usage.
func (wf *Workflow) AfterFunc(fn AfterFunc) {
	wf.mu.Lock()
	wf.afterFuncs = append(wf.afterFuncs, fn)
	wf.mu.Unlock()
}

func (wf *Workflow) ref() {
	wf.mu.Lock()
	wf.ensureInitCancel()
	wf.refCount++
	wf.mu.Unlock()
}

func (wf *Workflow) ensureInitCancel() {
	if wf.refCount == 0 {
		wf.ctx, wf.cancel = context.WithCancelCause(wf.ctx)
	}
}

// Unref decrements the workflow's reference count. If the count reaches zero,
// the workflow is completed, its context is cancelled, and any registered
// AfterFuncs are executed in random order.
func (wf *Workflow) unref(ctx context.Context) {
	wf.mu.Lock()
	if wf.refCount <= 0 {
		panic("Workflow reference count underflow")
	}
	wf.refCount--
	if wf.refCount > 0 {
		wf.mu.Unlock()
		return
	}

	cancel := wf.cancel
	afterFuncs := wf.afterFuncs
	wf.afterFuncs = nil

	// Avoid holding the lock while calling external functions
	wf.mu.Unlock()

	cancel(ErrWorkflowComplete)

	// Shuffle and execute the AfterFuncs
	rand.Shuffle(len(afterFuncs), func(i, j int) {
		afterFuncs[i], afterFuncs[j] = afterFuncs[j], afterFuncs[i]
	})
	for _, fn := range afterFuncs {
		fn(ctx, wf)
	}

	// Clear and return to pool
	wf.mu.Lock() // just in case
	defer wf.mu.Unlock()
	wf.cancel = nil
	if len(wf.afterFuncs) > 0 {
		panic("AfterFunc added to Workflow during calls to its original AfterFuncs")
	}
	wfPool.Put(wf)
}
