// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// mo is a reference-managed type used by the internal RefCount tests. Its Reset
// is payload-only and must not touch the embedded RefCount.
type mo struct {
	GenRefCounter
	payload int
}

func (m *mo) Reset() { m.payload = 0 }

func word(m *mo) (refs, gen uint64) {
	w := m.w.Load()
	return w[refsWord], w[genWord]
}

// TestRefCountModel drives random Get/Inc/Release/NewHandle/Handle.Get
// sequences against a reference model, asserting the exact (refs, gen) word
// after every operation and that Handle.Get succeeds exactly when the object it
// names is still the incarnation the handle was minted against.
func TestRefCountModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := For[mo]()

		// A live object we currently hold references to. Pointers are unique
		// across lives: Get never returns an object that still has references.
		type live struct {
			obj  *mo
			refs uint64
			gen  uint64
		}
		type handle struct {
			h       Handle[*mo]
			obj     *mo
			mintGen uint64
		}
		var lives []*live
		var handles []handle

		findLive := func(o *mo) *live {
			for _, l := range lives {
				if l.obj == o {
					return l
				}
			}
			return nil
		}

		t.Repeat(map[string]func(*rapid.T){
			"get": func(t *rapid.T) {
				obj := p.Get()
				refs, gen := word(obj)
				assert.Equal(t, uint64(1), refs, "Get must hand out exactly one reference")
				assert.Nil(t, findLive(obj), "Get returned an object that still has references")
				lives = append(lives, &live{obj: obj, refs: 1, gen: gen})
			},
			"addRef": func(t *rapid.T) {
				if len(lives) == 0 {
					t.Skip("no live object")
				}
				l := lives[rapid.IntRange(0, len(lives)-1).Draw(t, "live")]
				l.obj.RefCount().AddRef()
				l.refs++
				refs, gen := word(l.obj)
				assert.Equal(t, l.refs, refs)
				assert.Equal(t, l.gen, gen)
			},
			"release": func(t *rapid.T) {
				if len(lives) == 0 {
					t.Skip("no live object")
				}
				i := rapid.IntRange(0, len(lives)-1).Draw(t, "live")
				l := lives[i]
				p.Release(l.obj)
				l.refs--
				refs, gen := word(l.obj)
				if l.refs == 0 {
					// Last reference: recycle bumps the generation and zeroes refs.
					assert.Equal(t, uint64(0), refs)
					assert.Equal(t, l.gen+1, gen)
					lives = append(lives[:i], lives[i+1:]...)
				} else {
					assert.Equal(t, l.refs, refs)
					assert.Equal(t, l.gen, gen)
				}
			},
			"newHandle": func(t *rapid.T) {
				if len(lives) == 0 {
					t.Skip("no live object")
				}
				l := lives[rapid.IntRange(0, len(lives)-1).Draw(t, "live")]
				handles = append(handles, handle{h: NewHandle(l.obj), obj: l.obj, mintGen: l.gen})
			},
			"handleGet": func(t *rapid.T) {
				if len(handles) == 0 {
					t.Skip("no handle")
				}
				hh := handles[rapid.IntRange(0, len(handles)-1).Draw(t, "handle")]
				got, ok := hh.h.Get()

				// The upgrade succeeds iff the object is still live at the exact
				// generation the handle captured.
				l := findLive(hh.obj)
				want := l != nil && l.gen == hh.mintGen
				assert.Equal(t, want, ok, "Handle.Get result disagrees with the model")

				if ok {
					assert.Equal(t, hh.obj, got, "Handle.Get returned the wrong object")
					l.refs++
					refs, gen := word(l.obj)
					assert.Equal(t, l.refs, refs)
					assert.Equal(t, l.gen, gen)
				}
			},
		})

		// Drain remaining references so every object returns to the pool.
		for _, l := range lives {
			for l.refs > 0 {
				p.Release(l.obj)
				l.refs--
			}
		}
	})
}
