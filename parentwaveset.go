// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"maps"

	"github.com/petenewcomb/streampool/internal/omnipool"
)

// parentWaveSet is a ctxMeta's cross-wave ancestry set — the waves its body is nested
// under across goroutine boundaries — used solely to reject upward dispatch/skim (into an
// ancestor wave from a descendant ctx; see [waveImpl.ctxMeta] / ensureCtxMeta and
// TestTaskCannotSkimParentJob). It is reference-managed ([omnipool.RefCounter]) and pooled,
// so same-wave derivations SHARE one set by pointer (a cheap AddRef, no copy) and it is
// recycled only when the last referencing meta drops it. Only a genuine cross-wave hop
// copies — into a fresh pooled set whose map keeps its backing capacity across recycles
// (Reset clear()s rather than nils it). It carries no Handle of its own: every holder is a
// live meta with a strong reference, so there is no weak/cross-lifetime holder to gen-guard
// (the embedded generation is inert here). The set is immutable once built; the shared map
// is only ever read after publication, so concurrent membership tests are race-free.
//
// The MAP keys are gen-guarded [omnipool.Handle]s: a descendant records ancestor waves it
// does NOT pin, so an ancestor may recycle and be reused while an entry survives — a
// bare-pointer key would then false-match a new incarnation and fire a spurious "child
// wave" panic. Membership is tested by NewHandle(liveWave); the stored handles are compared
// by identity+generation, never Get-upgraded.
type parentWaveSet struct {
	omnipool.RefCounter
	m map[omnipool.Handle[*waveImpl]]struct{}
}

var parentWaveSetPool = omnipool.For[parentWaveSet]()

// Init allocates the map once per physical allocation ([omnipool.Initer]); Reset keeps it.
func (s *parentWaveSet) Init() { s.m = make(map[omnipool.Handle[*waveImpl]]struct{}) }

// Reset clears the map on recycle, preserving its backing capacity for the next cycle
// ([omnipool.Resetter]). It MUST NOT touch the embedded RefCount.
func (s *parentWaveSet) Reset() { clear(s.m) }

// has reports whether wv (given as a freshly-minted, gen-bearing handle) is in the set.
// Nil-safe: a meta with no ancestry carries a nil set.
func (s *parentWaveSet) has(h omnipool.Handle[*waveImpl]) bool {
	if s == nil {
		return false
	}
	_, ok := s.m[h]
	return ok
}

// retainParentWaveSet shares src with a new holder, taking a reference. Nil-safe. The new
// holder balances it with [releaseParentWaveSet] at its Reset.
func retainParentWaveSet(src *parentWaveSet) *parentWaveSet {
	if src != nil {
		src.AddRef() // safe: the sharing meta holds src live, so refs >= 1
	}
	return src
}

// releaseParentWaveSet drops a meta's reference; the set recycles (Reset clears the map)
// when the last reference lands. Nil-safe.
func releaseParentWaveSet(s *parentWaveSet) { parentWaveSetPool.Release(s) }

// derivedParentWaveSet returns the ancestry a body bound to wv should carry, derived from
// a source meta's set (src) and wave (srcWave). A same-wave or wave-less source shares src
// (retained); a cross-wave hop returns a fresh pooled set = src's members plus srcWave. The
// returned set carries a reference the caller owns (released at the owning meta's Reset).
func derivedParentWaveSet(src *parentWaveSet, srcWave, wv *waveImpl) *parentWaveSet {
	if srcWave == nil || srcWave == wv {
		return retainParentWaveSet(src) // same-wave / wave-less: share
	}
	// Cross-wave hop: a fresh set joining srcWave onto src's ancestry.
	s := parentWaveSetPool.Get() // refs == 1, map empty (Init'd or cleared)
	if src != nil {
		maps.Copy(s.m, src.m)
	}
	s.m[omnipool.NewHandle(srcWave)] = struct{}{}
	return s
}
