// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.
package state

import "sync/atomic"

type DynamicValue[T any] struct {
	state atomic.Pointer[dvState[T]]
}

func (dv *DynamicValue[T]) Load() (T, <-chan struct{}) {
	state := dv.state.Load()
	if state == nil {
		var zero T
		state = newDvState(zero)
		if !dv.state.CompareAndSwap(nil, state) {
			state = dv.state.Load()
		}
	}
	return state.value, state.changeChan
}

func (dv *DynamicValue[T]) Store(v T) {
	oldState := dv.state.Swap(&dvState[T]{
		value:      v,
		changeChan: make(chan struct{}),
	})
	if oldState != nil {
		close(oldState.changeChan)
	}
}

type dvState[T any] struct {
	value      T
	changeChan chan struct{}
}

func newDvState[T any](v T) *dvState[T] {
	return &dvState[T]{
		value:      v,
		changeChan: make(chan struct{}),
	}
}
