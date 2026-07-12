// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/petenewcomb/atomic128-go"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// TestMain disables native a128 under -race (its inline asm is invisible to the
// race detector, producing false positives on memory ordered through the word)
// and honors PSGNATIVEA128, since this package does not import nbcq (whose init
// does the same). Once the native-disable policy moves into the atomic128 fork,
// this stopgap can go away.
func TestMain(m *testing.M) {
	if raceEnabled {
		atomic128.DisableNative()
	}
	if s := os.Getenv("PSGNATIVEA128"); s != "" {
		if v, err := strconv.ParseBool(s); err == nil && !v {
			atomic128.DisableNative()
		}
	}
	os.Exit(m.Run())
}

// widget is a reference-managed pooled type: it embeds RefCount and so satisfies
// RefCounted (the promoted accessor), even from this external test package.
type widget struct {
	omnipool.RefCount
	val int
}

func (w *widget) Reset() { w.val = 0 } // payload-only; must not touch RefCount

func TestRefCountSmoke(t *testing.T) {
	p := omnipool.For[widget]()

	obj := p.Get() // refs = 1
	obj.val = 42

	h := omnipool.NewHandle(obj) // weak, captures gen

	got, ok := h.Get() // upgrade to a second strong reference
	if !ok || got != obj {
		t.Fatalf("Handle.Get on live object: got (%v,%v), want (%p,true)", got, ok, obj)
	}

	omnipool.AddRef(obj) // refs = 3

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
			t.Fatal("AddRef from zero: want panic, got none")
		}
	}()
	p := omnipool.For[widget]()
	obj := p.Get()
	p.Release(obj)       // -> 0, recycle
	omnipool.AddRef(obj) // no outstanding reference: panic
}
