// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool_test

import (
	"testing"

	"github.com/petenewcomb/streampool/internal/omnipool"
)

// meter is a CLEAN pooled type — it holds its counter as a named field and carries NO
// pool/refcount methods of its own. Its trait maps it to that counter, so a [CustomPool] drives
// the reference lifecycle without meter exposing anything — and, because the counter is a128, it
// can still back a Handle via NewCustomHandle (which locates the counter through the trait). This
// is the a128 variant.
type meter struct {
	gc  omnipool.GenRefCounter
	val int
}

type meterTrait struct{}

func (meterTrait) Make() *meter                   { return &meter{} }
func (meterTrait) Reset(m *meter)                 { m.val = 0 }
func (meterTrait) RefCount(m *meter) omnipool.Ref { return m.gc.RefCount() }

// dial is the a64 analogue: a clean type whose trait maps it to a plain [RefCounter].
type dial struct {
	rc  omnipool.RefCounter
	val int
}

type dialTrait struct{}

func (dialTrait) Make() *dial                   { return &dial{} }
func (dialTrait) Reset(d *dial)                 { d.val = 0 }
func (dialTrait) RefCount(d *dial) omnipool.Ref { return d.rc.RefCount() }

func TestCustomPool_GenRefManaged(t *testing.T) {
	p := omnipool.ForCustom(meterTrait{})

	m := p.Get() // refs = 1 (owner)
	m.val = 5

	p.AddRef(m)   // refs = 2 (pool-mediated, via the trait's RefCount accessor)
	m.gc.AddRef() // refs = 3 (direct, since we happen to hold the field here)

	p.Release(m) // refs = 2, no recycle
	p.Release(m) // refs = 1, no recycle
	if m.val != 5 {
		t.Fatalf("reset before last release: val=%d, want 5", m.val)
	}
	p.Release(m) // refs = 0 -> recycle, gen bumped, Reset ran
	if m.val != 0 {
		t.Fatalf("payload not cleared on recycle: val=%d", m.val)
	}
}

// A trait-managed CLEAN type (no accessor method on the object) can still back a Handle, minted
// through the trait — the whole point of NewCustomHandle. Exercises all three trait-managed ref
// ops with no pool call and no accessor on meter.
func TestCustomPool_TraitHandle(t *testing.T) {
	p := omnipool.ForCustom(meterTrait{})

	m := p.Get() // refs = 1 (owner)
	m.val = 7

	h := omnipool.NewCustomHandle(meterTrait{}, m) // weak, +0
	if !h.Valid() {
		t.Fatal("handle to a live object should be Valid")
	}
	omnipool.CustomAddRef(meterTrait{}, m) // refs = 2, pool-free strong clone via the trait

	got, ok := h.Get() // refs = 3, upgrade succeeds
	if !ok || got != m {
		t.Fatalf("Handle.Get on live object: got (%v,%v), want (%p,true)", got, ok, m)
	}

	p.Release(m) // refs = 2
	p.Release(m) // refs = 1
	p.Release(m) // refs = 0 -> recycle, gen bumped
	if h.Valid() {
		t.Fatal("handle should be invalid after the object was recycled")
	}
	if _, ok := h.Get(); ok {
		t.Fatal("Handle.Get after recycle should fail (stale generation)")
	}
}

func TestCustomPool_RefManaged(t *testing.T) {
	p := omnipool.ForCustom(dialTrait{})

	d := p.Get() // refs = 1
	d.val = 9
	p.AddRef(d) // refs = 2

	p.Release(d) // refs = 1, no recycle
	if d.val != 9 {
		t.Fatalf("reset before last release: val=%d, want 9", d.val)
	}
	p.Release(d) // refs = 0 -> recycle
	if d.val != 0 {
		t.Fatalf("payload not cleared on recycle: val=%d", d.val)
	}
}
