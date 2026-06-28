// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import "github.com/petenewcomb/streampool/internal/rdvq"

// Listeners is an alias for rdvq.Listeners for convenience
type Listeners = rdvq.Listeners

// NotifyFunc is an alias for rdvq.NotifyFunc for convenience
type NotifyFunc = rdvq.NotifyFunc

// Execution provides the interface for a work function to interact with
// the work queue system.
type Execution struct {
	Blocking       func()           // Call before blocking to release resources
	AddToListeners func(*Listeners) // If non-nil, call to subscribe to ready notifications
	Starting       func()           // Call before starting execution to confirm execution

	// HandedOff, if called (after Starting), declares that the work item's ownership
	// has been transferred to another runner — the executor pool in the dispatch/
	// execution split: the work was PushBack'd to an executor that will run it and Free
	// it. The driving controller then must NOT Free it (that would race the executor's
	// use and double-free). Without this call, a Started item is Freed by the controller
	// as usual (e.g. an abandoned dispatch whose handoff failed).
	HandedOff func()

	started func() bool
}

func (ex Execution) ShouldBlockOrPostpone() bool {
	return ex.AddToListeners != nil
}

func (ex Execution) Started() bool {
	return ex.started()
}

type Executor struct {
	baseEx       Execution // avoid closure reallocations
	wasStarted   bool
	wasHandedOff bool
}

func (e *Executor) BaseEx() Execution {
	if e.baseEx.started == nil {
		e.baseEx = Execution{
			Blocking:  e.Blocking,
			Starting:  e.Starting,
			HandedOff: e.HandedOff,
			started:   e.started,
		}
	}
	return e.baseEx
}

func (e *Executor) Blocking() {
}

func (e *Executor) Starting() {
	e.wasStarted = true
}

// HandedOff records that the executed item was handed to another runner (the executor
// pool), so the controller must not Free it. See [Execution.HandedOff].
func (e *Executor) HandedOff() {
	e.wasHandedOff = true
}

func (e *Executor) WasHandedOff() bool {
	return e.wasHandedOff
}

func (e *Executor) started() bool {
	return e.wasStarted
}

func (e *Executor) Reset() {
	e.wasStarted = false
	e.wasHandedOff = false
}
