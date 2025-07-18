// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"fmt"
	"sync/atomic"
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
