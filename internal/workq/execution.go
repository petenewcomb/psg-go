// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

// Execution provides the interface for a work function to interact with
// the work queue system.
type Execution struct {
	Blocking  func()             // Call before blocking to release resources
	Subscribe func(*Coordinator) // Call to subscribe to ready notifications
	Starting  func()             // Call before starting execution to confirm execution
	Queue     QueueWorkFunc      // Queue additional work items

	started func() bool
}

func (ex Execution) ShouldBlockOrSubscribe() bool {
	return ex.Subscribe != nil
}

func (ex Execution) Started() bool {
	return ex.started()
}

func (ex Execution) MayQueue() bool {
	return ex.Queue != nil
}

type Executor struct {
	baseEx     Execution // avoid closure reallocations
	wasStarted bool
}

func (e *Executor) BaseEx() Execution {
	if e.baseEx.started == nil {
		e.baseEx = Execution{
			Blocking: e.Blocking,
			Starting: e.Starting,
			started:  e.started,
		}
	}
	return e.baseEx
}

func (e *Executor) Blocking() {
}

func (e *Executor) Starting() {
	e.wasStarted = true
}

func (e *Executor) started() bool {
	return e.wasStarted
}

func (e *Executor) Reset() {
	e.wasStarted = false
}
