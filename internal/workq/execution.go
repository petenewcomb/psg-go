// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import "github.com/petenewcomb/streampool/internal/rdvq"

// Listeners is an alias for rdvq.Listeners for convenience
type Listeners = rdvq.Listeners

// Execution provides the interface for a work function to interact with
// the work queue system.
type Execution struct {
	Blocking func() // Call before blocking to release resources
	Starting func() // Call before starting execution to confirm execution

	// Listener is the wake relay of the queue whose controller is driving this
	// execution — the identity a gated work arms as its registered demand's
	// attendant (a listen-capable postpone) or plants as fallback interest
	// before the final re-attempt of a one-shot miss. Set for every
	// controller-driven execution; listen capability is CanListen, not this
	// field's presence.
	Listener *rdvq.Listener

	// CanListen marks a listen-capable pass: a miss may leave its demand
	// registered, attended by the queue's relay. False on opportunistic
	// non-listening sweeps and inline one-shot tries, whose misses must
	// withdraw the demand (attendance-backed registration).
	CanListen bool

	// Queue queues a work item as fresh on the queue whose controller is
	// driving this execution. A work that runs user code installs it into the
	// meta's execution environment for the duration, so a nested dispatch made
	// inside the body (a skim handler) lands its sub-work on the driving queue
	// — synchronously, on the controller's own goroutine, so fresh stays
	// controller-written. Non-nil for controller-driven executions; nil for
	// inline one-shot tries.
	Queue QueueWorkFunc

	started func() bool
}

func (ex Execution) ShouldBlockOrPostpone() bool {
	return ex.CanListen
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
