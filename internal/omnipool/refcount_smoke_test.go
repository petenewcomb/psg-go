// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool_test

import (
	"testing"

	"github.com/petenewcomb/streampool/internal/omnipool"
)

// The atomic128 fork forces its mutex fallback under the race detector (native
// asm is TSan-invisible), so -race exercises the fallback with no test setup
// here; PSGNATIVEA128 native-disable, used off the race path, lives in nbcq.

// widget is a generation-guarded pooled type (embeds GenRefCounter), so it can back a
// [omnipool.Handle] — the a128 path.
type widget struct {
	omnipool.GenRefCounter
	val int
}

func (w *widget) Reset() { w.val = 0 } // payload-only; must not touch GenRefCounter

func TestRefCountSmoke(t *testing.T) {
	p := omnipool.For[widget]()

	obj := p.Get() // refs = 1
	obj.val = 42

	h := omnipool.NewHandle(obj) // weak, captures gen

	got, ok := h.Get() // upgrade to a second strong reference
	if !ok || got != obj {
		t.Fatalf("Handle.Get on live object: got (%v,%v), want (%p,true)", got, ok, obj)
	}

	obj.GenRefCount().Inc() // refs = 3

	p.Release(obj) // refs = 2
	p.Release(obj) // refs = 1
	p.Release(obj) // refs = 0 -> recycle, gen bumped

	if _, ok := h.Get(); ok {
		t.Fatal("Handle.Get after recycle: want false (stale generation), got true")
	}

	// A fresh handle on a reused incarnation must work again.
	obj2 := p.Get()
	h2 := omnipool.NewHandle(obj2)
	if _, ok := h2.Get(); !ok {
		t.Fatal("Handle.Get on new incarnation: want true, got false")
	}
	p.Release(obj2) // drop h2's upgrade
	p.Release(obj2) // drop the Get reference -> recycle
}

func TestRefCountUnderflowPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Release below zero: want panic, got none")
		}
	}()
	p := omnipool.For[widget]()
	obj := p.Get()
	p.Release(obj) // -> 0, recycle
	p.Release(obj) // underflow: panic
}

func TestAddRefFromZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Inc from zero: want panic, got none")
		}
	}()
	p := omnipool.For[widget]()
	obj := p.Get()
	p.Release(obj)          // -> 0, recycle
	obj.GenRefCount().Inc() // no outstanding reference: panic
}
