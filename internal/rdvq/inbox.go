// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

type Inbox[T any] struct {
	sensor     schedulerLatencySensor
	ch         chan T
	wasEmptied bool
}

// Ch returns the inbox's channel for use in select statements. Returns nil if
// the inbox itself is nil. Panics if called when no channel has been allocated,
// which should only happen if Ch() is called outside of a selectFn callback.
func (ib *Inbox[T]) Ch() <-chan T {
	if ib == nil {
		return nil
	}
	ch := ib.ch
	if ch == nil {
		panic("inbox channel is nil")
	}
	return ch
}

func (ib *Inbox[T]) filled() {
	ib.sensor.triggered()
}

func (ib *Inbox[T]) emptyPending() {
	ib.wasEmptied = false
	ib.sensor.waitStarting()
}

func (ib *Inbox[T]) Emptied() {
	ib.sensor.waitEnded()
	ib.wasEmptied = true
}

// WasFilled returns true if Filled() was called.
func (ib *Inbox[T]) WasEmptied() bool {
	return ib.wasEmptied
}
