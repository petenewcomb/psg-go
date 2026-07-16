// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package dll_test

import (
	"testing"

	"github.com/petenewcomb/streampool/internal/dll"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

type node struct {
	dll.Links[*node]
	id int
}

// contents walks the list front to back via Front/Next.
func contents(l *dll.List[*node]) []int {
	var ids []int
	for n := l.Front(); n != nil; n = l.Next(n) {
		ids = append(ids, n.id)
	}
	return ids
}

func TestZeroValueIsEmpty(t *testing.T) {
	chk := require.New(t)
	var l dll.List[*node]
	chk.Nil(l.Front())
	chk.Empty(contents(&l))
}

func TestPushBackAndFrontOrder(t *testing.T) {
	chk := require.New(t)
	var l dll.List[*node]
	a, b, c := &node{id: 1}, &node{id: 2}, &node{id: 3}
	l.PushBack(a)
	l.PushBack(b)
	l.PushFront(c)
	chk.Equal([]int{3, 1, 2}, contents(&l))
	chk.True(a.Linked())
	chk.True(b.Linked())
	chk.True(c.Linked())
}

func TestRemove(t *testing.T) {
	chk := require.New(t)
	var l dll.List[*node]
	a, b, c := &node{id: 1}, &node{id: 2}, &node{id: 3}
	l.PushBack(a)
	l.PushBack(b)
	l.PushBack(c)

	chk.True(l.Remove(b)) // middle
	chk.Equal([]int{1, 3}, contents(&l))
	chk.False(b.Linked())
	chk.False(l.Remove(b)) // already unlinked

	chk.True(l.Remove(a)) // front
	chk.Equal([]int{3}, contents(&l))

	chk.True(l.Remove(c)) // back, leaving empty
	chk.Empty(contents(&l))
	chk.Nil(l.Front())
}

func TestRelinkAfterRemove(t *testing.T) {
	chk := require.New(t)
	var l dll.List[*node]
	a, b := &node{id: 1}, &node{id: 2}
	l.PushBack(a)
	l.PushBack(b)
	chk.True(l.Remove(a))
	l.PushBack(a)
	chk.Equal([]int{2, 1}, contents(&l))
}

func TestMoveBetweenLists(t *testing.T) {
	chk := require.New(t)
	var fifo, claimants dll.List[*node]
	a := &node{id: 1}
	fifo.PushBack(a)
	chk.True(fifo.Remove(a))
	claimants.PushBack(a)
	chk.Empty(contents(&fifo))
	chk.Equal([]int{1}, contents(&claimants))
}

func TestDoublePushPanics(t *testing.T) {
	chk := require.New(t)
	var l, other dll.List[*node]
	a := &node{id: 1}
	l.PushBack(a)
	chk.Panics(func() { l.PushBack(a) })
	chk.Panics(func() { l.PushFront(a) })
	chk.Panics(func() { other.PushBack(a) })
}

func TestCrossListRemovePanics(t *testing.T) {
	chk := require.New(t)
	var l, other dll.List[*node]
	a := &node{id: 1}
	l.PushBack(a)
	chk.Panics(func() { other.Remove(a) })
}

// TestByProperty drives a list and a reference slice through random
// push/remove sequences and checks they always agree.
func TestByProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var l dll.List[*node]
		var model []*node
		nextID := 0

		modelIDs := func() []int {
			ids := make([]int, len(model))
			for i, n := range model {
				ids[i] = n.id
			}
			return ids
		}

		t.Repeat(map[string]func(*rapid.T){
			"pushBack": func(t *rapid.T) {
				n := &node{id: nextID}
				nextID++
				l.PushBack(n)
				model = append(model, n)
			},
			"pushFront": func(t *rapid.T) {
				n := &node{id: nextID}
				nextID++
				l.PushFront(n)
				model = append([]*node{n}, model...)
			},
			"remove": func(t *rapid.T) {
				if len(model) == 0 {
					t.Skip()
				}
				i := rapid.IntRange(0, len(model)-1).Draw(t, "i")
				n := model[i]
				if !l.Remove(n) {
					t.Fatalf("Remove(%d) returned false for a linked item", n.id)
				}
				if n.Linked() {
					t.Fatalf("item %d still Linked after Remove", n.id)
				}
				model = append(model[:i], model[i+1:]...)
			},
			"": func(t *rapid.T) {
				if got, want := contents(&l), modelIDs(); len(got) != len(want) {
					t.Fatalf("list %v != model %v", got, want)
				} else {
					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("list %v != model %v", got, want)
						}
					}
				}
			},
		})
	})
}
