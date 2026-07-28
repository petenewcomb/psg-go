// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import "testing"

// gauge is a reference-managed pooled type with ONLY strong holders: it embeds the plain
// [RefCounter] (the a64 counter, no generation) and so CANNOT back a Handle — the lighter
// path used by e.g. streampool's parentWaveSet. (Internal test so the package's unused
// analysis sees the a64 RefCounter exercised, mirroring mo for GenRefCounter.)
type gauge struct {
	RefCounter
	resets int
	val    int
}

func (g *gauge) Reset() { g.val = 0; g.resets++ } // payload-only; must not touch RefCounter

func TestA64RefCount_GetAddRefRelease(t *testing.T) {
	p := For[gauge]()

	obj := p.Get() // refs = 1 (owner)
	obj.val = 7
	resetsBefore := obj.resets

	obj.RefCount().AddRef() // refs = 2
	obj.RefCount().AddRef() // refs = 3

	p.Release(obj) // refs = 2, no recycle
	p.Release(obj) // refs = 1, no recycle
	if obj.val != 7 {
		t.Fatalf("payload cleared before last release: val=%d, want 7", obj.val)
	}
	if obj.resets != resetsBefore {
		t.Fatalf("Reset ran before last release: resets=%d, want %d", obj.resets, resetsBefore)
	}

	p.Release(obj) // refs = 0 -> recycle, Reset runs
	if obj.resets != resetsBefore+1 {
		t.Fatalf("Reset did not run on recycle: resets=%d, want %d", obj.resets, resetsBefore+1)
	}
	if obj.val != 0 {
		t.Fatalf("payload not cleared on recycle: val=%d, want 0", obj.val)
	}
}

func TestA64RefCount_ReleaseReportsRecycle(t *testing.T) {
	p := For[gauge]()

	obj := p.Get()          // refs = 1 (owner)
	obj.RefCount().AddRef() // refs = 2

	if p.Release(obj) { // refs = 1
		t.Fatal("Release with a reference still held reported recycled=true")
	}
	if !p.Release(obj) { // refs = 0 -> recycle
		t.Fatal("Release of the last reference reported recycled=false")
	}
}

func TestA64RefCount_RefExclusive(t *testing.T) {
	p := For[gauge]()

	obj := p.Get() // refs = 1
	if !obj.RefExclusive() {
		t.Fatal("RefExclusive at refs=1: want true (sole holder), got false")
	}
	obj.RefCount().AddRef() // refs = 2
	if obj.RefExclusive() {
		t.Fatal("RefExclusive at refs=2: want false (a second holder exists), got true")
	}
	p.Release(obj) // refs = 1
	if !obj.RefExclusive() {
		t.Fatal("RefExclusive after dropping to refs=1: want true, got false")
	}
	p.Release(obj) // refs = 0 -> recycle
}

func TestA64RefCount_TryAddRef(t *testing.T) {
	p := For[gauge]()

	obj := p.Get() // refs = 1 (live)
	if !obj.TryAddRef() {
		t.Fatal("TryAddRef on a live object (refs=1): want true, got false")
	}
	// TryAddRef succeeded, so refs = 2: it takes two Releases to recycle.
	resetsBefore := obj.resets
	p.Release(obj) // refs = 1, no recycle
	if obj.resets != resetsBefore {
		t.Fatalf("recycled after one release: TryAddRef did not increment (resets=%d, want %d)", obj.resets, resetsBefore)
	}
	p.Release(obj) // refs = 0 -> recycle

	if obj.TryAddRef() {
		t.Fatal("TryAddRef on a recycled object (refs=0): want false (no resurrection), got true")
	}
}

func TestA64RefCount_ReleaseUnderflowPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Release below zero: want panic, got none")
		}
	}()
	p := For[gauge]()
	obj := p.Get()
	p.Release(obj) // -> 0, recycle
	p.Release(obj) // underflow: panic
}

func TestA64RefCount_AddRefFromZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Inc from zero: want panic, got none")
		}
	}()
	p := For[gauge]()
	obj := p.Get()
	p.Release(obj)          // -> 0, recycle
	obj.RefCount().AddRef() // no outstanding reference: panic
}
