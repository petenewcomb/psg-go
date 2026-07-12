// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package nbcq

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"unsafe"

	"github.com/petenewcomb/atomic128-go"
)

func init() {
	if s := os.Getenv("PSGNATIVEA128"); s != "" {
		v, err := strconv.ParseBool(s)
		if err != nil {
			panic(fmt.Sprintf("Invalid value %q for PSGNATIVEA128; must be one accepted by strconv.ParseBool", s))
		}
		if !v {
			atomic128.DisableNative()
		} else if !atomic128.HasNative() {
			panic(fmt.Sprintf("PSGNATIVEA128 explicitly set to %q but platform not supported", s))
		}
	}
}

type atomicPointer[T any] struct {
	// ap is the source of truth for the held pointer[T]
	a128 atomic128.Uint128

	// ptr is used to ensure that a reference to the node[T] is not lost due to
	// it being stored in the a128 word as a uint64 and therefore opaque to the
	// garbage collector
	np atomic.Pointer[node[T]]
}

func (ap *atomicPointer[T]) Store(val pointer[T]) {
	ap.a128.Store(asPair(val))
	ap.np.Store(val.ptr)
}

func (ap *atomicPointer[T]) Load() pointer[T] {
	for {
		// Load the ptr from np first, so that we can be sure that if it
		// matches, the uintptr value we retrieve from a128 is still valid
		ptr := ap.np.Load()
		pair := ap.a128.Load()

		//nolint:gosec // unsafe calls have been audited
		if uintptr(unsafe.Pointer(ptr)) == uintptr(pair[0]) {
			return pointer[T]{ptr: ptr, count: pair[1]}
		}
	}
}

//nolint:gocritic // new shadows predeclared identifier, but follows the pattern in sync.Atomic
func (ap *atomicPointer[T]) CompareAndSwap(old pointer[T], new pointer[T]) bool {
	oldPair := asPair(old)
	newPair := asPair(new)
	if !ap.a128.CompareAndSwap(oldPair, newPair) {
		return false
	}
	ap.updateNodePtr(old.ptr, newPair, new.ptr)
	return true
}

func (ap *atomicPointer[T]) updateNodePtr(oldPtr *node[T], newPair [2]uint64, newPtr *node[T]) {
	// Don't release newPtr (by returning) until np is updated or a128 no longer
	// holds the value we just set.
	for !ap.np.CompareAndSwap(oldPtr, newPtr) {
		// oldPtr must have been updated, but a128 might not have been
		oldPtr = ap.np.Load()
		currentPair := ap.a128.Load()
		if currentPair != newPair {
			// a128 has a new value, newPtr no longer needed
			break
		}
	}
}

func asPair[T any](p pointer[T]) [2]uint64 {
	//nolint:gosec // unsafe calls have been audited
	return [2]uint64{uint64(uintptr(unsafe.Pointer(p.ptr))), p.count}
}
