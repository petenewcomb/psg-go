// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// TODO: update
// WorkFunc represents a work item that will execute immediately or provide
// notification for later retry. If ex.Starting is nil, the work function is
// being abandoned and must release any acquired resources and exit without
// executing its work. Otherwise, the work function must call ex.Starting before
// starting execution to confirm it will execute, and must not call ex.Starting
// if it cannot execute. If not executing immediately and ex.Subscribe is not
// nil, it must call ex.Subscribe to register for notification when execution
// should be retried (e.g., when resources become available).
//
// IMPORTANT: After calling ex.Subscribe the work function must re-check the
// condition that caused it to not execute and execute anyway if the condition
// allows. This avoids a race in which the condition becomes true between the
// initial check and the registration of the ReadyFn.
type WorkFunc func(context.Context, Execution) error

type Work interface {
	ID() WorkID
	Execute(context.Context, Execution) error
	Close()
}

// Execution provides the interface for a work function to interact with
// the work queue system.
type Execution struct {
	Blocking  func()             // Call before blocking to release resources
	Subscribe func(*Coordinator) // Call to subscribe to ready notifications
	Starting  func()             // Call before starting execution to confirm execution
	Queue     QueueWorkFunc      // Queue additional work items
}

func (ex Execution) ShouldBlockOrSubscribe() bool {
	return ex.Subscribe != nil
}

func (ex Execution) MayQueue() bool {
	return ex.Queue != nil
}

var workIDCounter atomic.Int64

type WorkID int64

func NewWorkID() WorkID {
	return WorkID(workIDCounter.Add(1))
}

type WorkItem struct {
	id WorkID
}

func (wi *WorkItem) Init() {
	wi.id = NewWorkID()
}

func (wi *WorkItem) ID() WorkID {
	return wi.id
}

func (wi *WorkItem) Close() {
}

func (wi *WorkItem) String() string {
	return fmt.Sprintf("WorkItem#%d", wi.id)
}

type WorkFuncItem struct {
	WorkItem
	workFn WorkFunc
}

func NewWorkItem(workFn WorkFunc) *WorkFuncItem {
	wi := &WorkFuncItem{}
	wi.Init(workFn)
	return wi
}

func (wi *WorkFuncItem) Init(workFn WorkFunc) {
	wi.WorkItem.Init()
	wi.workFn = workFn
}

func (wi *WorkFuncItem) Execute(ctx context.Context, ex Execution) error {
	traceRegion := "workq.WorkFuncItem.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", wi)
	return wi.workFn(ctx, ex)
}
