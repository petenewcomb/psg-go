// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

type Outbox[T any] struct {
	sensor    schedulerLatencySensor
	ch        chan T
	wasFilled bool
}

// Ch returns the outbox's channel for use in select statements.
// Returns nil if the outbox itself is nil.
// Panics if called when no channel has been allocated, which should only
// happen if Ch() is called outside of a selectFn callback.
func (ob *Outbox[T]) Ch() chan<- T {
	if ob == nil {
		return nil
	}
	ch := ob.ch
	if ch == nil {
		panic("outbox channel is nil")
	}
	return ch
}

func (ob *Outbox[T]) fillPending() {
	ob.wasFilled = false
	ob.sensor.waitStarting()
}

func (ob *Outbox[T]) Filled() {
	ob.sensor.waitEnded()
	ob.wasFilled = true
}

func (ob *Outbox[T]) emptied() {
	ob.sensor.triggered()
}

// WasFilled returns true if Filled() was called.
func (ob *Outbox[T]) WasFilled() bool {
	return ob.wasFilled
}
