// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
)

// WorkFunc represents a work item that will execute immediately or provide
// notification for later retry. The work function must call ex.Starting()
// before starting execution to confirm it will execute, and must not call
// ex.Starting() if it cannot execute. If not executing immediately and
// ex.ReadyFn is not nil, it must return quickly and later call ex.ReadyFn
// when execution should be retried (e.g., when resources become available).
//
// IMPORTANT: After registering (queuing) a ReadyFn to be called later, the work
// function must re-check the condition that caused it to not execute and
// execute anyway if the condition allows. This avoids a race in which the
// condition becomes true between the initial check and the registration of the
// ReadyFn.
type WorkFunc func(ctx context.Context, ex Execution) error

// Execution provides the interface for a work function to interact with
// the work queue system.
type Execution struct {
	Blocking func()        // Call before blocking to release resources
	Starting func()        // Call before starting execution to confirm execution
	ReadyFn  NotifyFunc    // Call when ready to retry (can be nil for non-blocking)
	Queue    QueueWorkFunc // Queue additional work items
}
