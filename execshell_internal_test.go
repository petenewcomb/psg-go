// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"testing"
)

// newTestShellPool builds an execShellPool over a standalone waveCtx with nil
// job/wave (the pool only stores those pointers into meta; it never dereferences
// them), so the shell mechanism can be exercised without a full Pool/Wave.
func newTestShellPool(t *testing.T) (*execShellPool, context.CancelFunc) {
	t.Helper()
	waveCtx, cancel := context.WithCancel(context.Background())
	p := &execShellPool{}
	p.Init(waveCtx, nil, nil)
	return p, cancel
}

func TestExecShell_BorrowStampsMeta(t *testing.T) {
	p, cancel := newTestShellPool(t)
	defer cancel()

	var ee workerExEnv
	s := p.borrow(taskContext, &ee)

	if s.meta.ctxType != taskContext {
		t.Fatalf("ctxType = %v, want taskContext", s.meta.ctxType)
	}
	if s.meta.executionEnvironment != &ee {
		t.Fatalf("executionEnvironment not stamped")
	}
	if s.meta.parent != nil {
		t.Fatalf("parent = %v, want nil (permit-root)", s.meta.parent)
	}
	if s.meta.heldRequest != nil {
		t.Fatalf("heldRequest = %v, want nil", s.meta.heldRequest)
	}
	// The ctx must carry the same meta under the framework key, so j.ctxMeta and
	// resolveWave resolve it.
	if got := s.ctx.Value(ctxMetaValueKey{}); got != s.meta {
		t.Fatalf("ctx.Value(ctxMetaValueKey{}) = %v, want shell.meta %v", got, s.meta)
	}
}

func TestExecShell_GiveBackReusesAndResets(t *testing.T) {
	p, cancel := newTestShellPool(t)
	defer cancel()

	var ee workerExEnv
	s1 := p.borrow(taskContext, &ee)
	meta1 := s1.meta
	p.giveBack(s1)

	// Spent shell must not pin the exEnv or a limiter request between borrows.
	if meta1.executionEnvironment != nil {
		t.Fatalf("giveBack did not clear executionEnvironment")
	}
	if meta1.heldRequest != nil {
		t.Fatalf("giveBack did not clear heldRequest")
	}

	// Next borrow reuses the same shell (and its meta pointer): self-sizing pool.
	s2 := p.borrow(funnelContext, &ee)
	if s2 != s1 {
		t.Fatalf("borrow after giveBack did not reuse the freed shell")
	}
	if s2.meta != meta1 {
		t.Fatalf("reused shell has a different meta pointer")
	}
	if s2.meta.ctxType != funnelContext {
		t.Fatalf("re-borrow ctxType = %v, want funnelContext", s2.meta.ctxType)
	}
}

func TestExecShell_WaveCancelPropagates(t *testing.T) {
	p, cancel := newTestShellPool(t)
	defer cancel()

	var ee workerExEnv
	s := p.borrow(taskContext, &ee)

	select {
	case <-s.ctx.Done():
		t.Fatal("shell ctx already cancelled before waveCtx cancel")
	default:
	}

	cancel() // tear the wave down

	select {
	case <-s.ctx.Done():
	default:
		t.Fatal("shell ctx not cancelled after waveCtx cancel (ancestry broken)")
	}
}

func TestExecShell_DistinctDoneChannels(t *testing.T) {
	p, cancel := newTestShellPool(t)
	defer cancel()

	var ee workerExEnv
	// Two concurrently-live shells (no giveBack between) must be distinct objects
	// with distinct contexts, so their done channels don't collide on one park.
	s1 := p.borrow(taskContext, &ee)
	s2 := p.borrow(taskContext, &ee)
	if s1 == s2 {
		t.Fatal("two live borrows returned the same shell")
	}
	if s1.ctx == s2.ctx {
		t.Fatal("two live shells share a context (done channel would collide)")
	}
}

func TestRunInShell_RunsBodyUnderShell(t *testing.T) {
	waveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Wave{}
	w.shells.Init(waveCtx, nil, w)

	// A worker context carries the worker's E under workerEnvKey (newWorkerState).
	ee := &workerExEnv{}
	workerCtx := context.WithValue(context.Background(), workerEnvKey{}, ee)

	// Capture the meta state INSIDE the body — the deferred giveBack resets the
	// transient fields once runInShell returns.
	var gotCtx context.Context
	var gotEE executionEnvironment
	var gotType contextType
	var gotWave *Wave
	var haveMeta bool
	err := runInShell(workerCtx, w, taskContext, nil, func(ctx context.Context) error {
		gotCtx = ctx
		m, ok := ctx.Value(ctxMetaValueKey{}).(*ctxMeta)
		haveMeta = ok
		if ok {
			gotEE = m.executionEnvironment
			gotType = m.ctxType
			gotWave = m.wave
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runInShell error: %v", err)
	}
	if !haveMeta {
		t.Fatal("body did not run under a shell ctx carrying a ctxMeta")
	}
	if gotEE != executionEnvironment(ee) {
		t.Fatal("worker E not stamped into the shell meta")
	}
	if gotType != taskContext {
		t.Fatalf("ctxType = %v, want taskContext", gotType)
	}
	if gotWave != w {
		t.Fatal("wave not on the shell meta")
	}
	// The body ran under the shell ctx (a descendant of waveCtx), not the worker
	// ctx: cancelling waveCtx must cancel the ctx the body saw.
	cancel()
	select {
	case <-gotCtx.Done():
	default:
		t.Fatal("body ctx not a descendant of waveCtx")
	}
}

func TestRunInShell_ReturnsShellForReuse(t *testing.T) {
	waveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Wave{}
	w.shells.Init(waveCtx, nil, w)
	ee := &workerExEnv{}
	workerCtx := context.WithValue(context.Background(), workerEnvKey{}, ee)

	var meta1, meta2 *ctxMeta
	_ = runInShell(workerCtx, w, taskContext, nil, func(ctx context.Context) error {
		meta1, _ = ctx.Value(ctxMetaValueKey{}).(*ctxMeta)
		return nil
	})
	_ = runInShell(workerCtx, w, funnelContext, nil, func(ctx context.Context) error {
		meta2, _ = ctx.Value(ctxMetaValueKey{}).(*ctxMeta)
		return nil
	})
	// Sequential bodies reuse the one freed shell (giveBack on return).
	if meta1 == nil || meta1 != meta2 {
		t.Fatal("runInShell did not return the shell for reuse")
	}
	// Transient fields cleared between borrows.
	if meta2.heldRequest != nil {
		t.Fatal("heldRequest leaked across borrows")
	}
}

func TestExecShell_ReleaseCancelsFreeList(t *testing.T) {
	p, cancel := newTestShellPool(t)
	defer cancel()

	var ee workerExEnv
	s := p.borrow(taskContext, &ee)
	p.giveBack(s)

	p.release()

	select {
	case <-s.ctx.Done():
	default:
		t.Fatal("release did not cancel a freed shell's context")
	}
}
