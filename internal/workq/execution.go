// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import "github.com/petenewcomb/psg-go/internal/rdvq"

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

	started func() bool
}

func (ex Execution) ShouldBlockOrPostpone() bool {
	return ex.AddToListeners != nil
}

func (ex Execution) Started() bool {
	return ex.started()
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
