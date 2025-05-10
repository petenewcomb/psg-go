// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.
package state

import "sync/atomic"

type dvState[T any] struct {
	value      T
	changeChan chan struct{}
}

type DynamicValue[T any] struct {
	state atomic.Pointer[dvState[T]]
}

func (dv *DynamicValue[T]) Init(v T) {
	dv.state.Store(&dvState[T]{
		value:      v,
		changeChan: make(chan struct{}),
	})
}

func (dv *DynamicValue[T]) Load() (T, <-chan struct{}) {
	state := dv.state.Load()
	return state.value, state.changeChan
}

func (dv *DynamicValue[T]) Store(v T) {
	oldState := dv.state.Swap(&dvState[T]{
		value:      v,
		changeChan: make(chan struct{}),
	})
	close(oldState.changeChan)
}
