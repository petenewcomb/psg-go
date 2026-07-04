// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/ctxpool"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/workq"
)

// flowInstance is the lifetime identity behind one FollowUp registration
// (docs/decisions/flow-design.md, "Lifetime semantics"). Instances are fully
// internal — no user handle exists; the minted key/tag is only the shaping
// identity. One instance is created per FollowUp option per WithFlow scope,
// and its reference count tracks the carriers of that registration:
//
//   - +1 held by the registering scope from entry to exit (the lexical cover
//     that makes the attach window race-free — parent-covers-children);
//   - +1 per work item whose body ctx was borrowed under a rider set
//     containing the instance (taken at dispatch inside borrowBodyContext,
//     released at completion inside releaseBodyContext);
//   - +1 while the instance's own follow-up runs (fire borrows the fn ctx
//     with the same symmetric ref — the provisional ref that lets fn's own
//     dispatches attach before the count can resolve to zero).
//
// A release that reaches zero arms a firing pass. The pass runs fn once,
// then resolves: count still zero → true end (nothing extended the flow —
// quiescent forever, since no carrier remains to take a new ref); count
// positive → an extension is outstanding, so the pass disarms and the
// extension's own last release arms the next pass ("fires at each nominal
// end"). Extensions that complete within the pass count as observed by it.
//
// Instances are currently GC-owned; pooling + generation stamps arrive with
// the CP-F4 allocation pass (a firing is cold — once per flow end — so the
// alloc is off the hot path).
type flowInstance struct {
	// fn is the user follow-up. It deliberately returns nothing: a follow-up
	// has no wave to surface an error through, so an error return would be a
	// silent discard dressed as an API; user error handling belongs inside fn
	// (typically by dispatching into a wave fn drains).
	fn func(context.Context)
	// fnRiders is the single-entry rider set the fn ctx carries: the firing
	// instance's own bundle (identity, the bundle value if any, and this
	// instance alone — not sibling registrations, whose lifecycles are their
	// own). fn's dispatches inherit it ambiently, which is what makes
	// extension work.
	fnRiders *flowRiders
	count    atomic.Int64
	active   atomic.Bool
}

func (in *flowInstance) ref() {
	in.count.Add(1)
}

// unref releases one carrier; the release that reaches zero arms a firing
// pass. inline selects where the pass runs: true only at WithFlow scope exit,
// where running user code is semantically the user's own call site (and gives
// "an empty scope fires at return" deterministically); false everywhere else
// — work-item completion paths run inside Free/release machinery where the
// item's wave reference has not yet dropped, so user code must not run (an fn
// draining that wave would deadlock) and the pass is handed to the executor
// through the scheduler instead.
func (in *flowInstance) unref(inline bool) {
	if in.count.Add(-1) != 0 {
		return
	}
	in.arm(inline)
}

func (in *flowInstance) arm(inline bool) {
	if !in.active.CompareAndSwap(false, true) {
		// A pass is already armed or running; its post-fire recheck covers
		// this zero-crossing.
		return
	}
	if inline {
		in.firingPass()
		return
	}
	wk := flowFireWorkPool.Get()
	wk.Init(workq.NewGroupID())
	wk.inst = in
	defaultPool.ForceFresh(wk)
}

// firingPass owns the active flag. It fires fn while the instance is
// quiescent and resolves the activation per the nominal-end semantics above.
func (in *flowInstance) firingPass() {
	for {
		if in.count.Load() == 0 {
			in.fire()
			if in.count.Load() == 0 {
				// True end: fn extended nothing (or its extensions already
				// completed and were observed by this pass). No carrier
				// remains and none can appear — the fn ctx is released — so
				// the instance is quiescent forever.
				in.active.Store(false)
				return
			}
		}
		// Carriers outstanding (an extension, or a spurious arm that raced a
		// ref): disarm, then close the missed-wake window — a carrier may have
		// reached zero between the count load and the disarm, its arm
		// suppressed by our active flag.
		in.active.Store(false)
		if in.count.Load() == 0 && in.active.CompareAndSwap(false, true) {
			continue
		}
		return
	}
}

// fire runs fn under a fresh framework ctx rooted at context.Background: a
// follow-up belongs to no wave and no request — cancellation of the work that
// *ended* must not cancel the reaction to its end. The ctx carries fnRiders,
// and the borrow takes the same symmetric ref every body borrow takes — the
// provisional ref: fn's dispatches attach under its cover, and the deferred
// release drops it (never to zero mid-pass: the pass's own recheck follows).
// A panic in fn propagates, like every user body; the deferred release keeps
// the count sound on the unwind.
//
//nolint:contextcheck // Background root by design; see the doc comment above
func (in *flowInstance) fire() {
	m := bodyMetaPool.Get()
	m.ctxType = topLevelContext
	m.riders = in.fnRiders
	flowRefRiders(m.riders)
	ctx := ctxpool.WithValue(context.Background(), m)
	defer releaseBodyContext(ctx)
	in.fn(ctx)
}

// flowRefRiders / flowUnrefRiders take and release one carrier reference on
// every instance in a rider set. Paired by construction: borrowBodyContext
// and fire ref; releaseBodyContext unrefs. Derived metas (ensureCtxMeta) and
// the flush sever clone inherit rider sets WITHOUT refs and are released via
// paths that do not unref (releaseTopLevelContext) or carry nil riders — they
// are synchronous extents covered by their enclosing carrier's ref.
func flowRefRiders(r *flowRiders) {
	if r == nil {
		return
	}
	for i := range r.entries {
		for _, in := range r.entries[i].insts {
			in.ref()
		}
	}
}

func flowUnrefRiders(r *flowRiders) {
	if r == nil {
		return
	}
	for i := range r.entries {
		for _, in := range r.entries[i].insts {
			in.unref(false)
		}
	}
}

// flowFireWork routes a firing pass through the scheduler onto the executor
// (the funnelInstance.Execute pattern): the pass runs user code, so it gets a
// pool worker and never runs inside completion/release machinery. It embeds a
// bare workq.WorkItem (not poolWork): a follow-up belongs to no wave, so it
// takes no wave work reference.
type flowFireWork struct {
	workq.WorkItem
	inst *flowInstance
}

// Execute is the scheduler side: hand the pass to the executor. TryPushBack
// first; when the scheduler worker is prepared to park, a blocking PushBack.
func (wk *flowFireWork) Execute(ctx context.Context, ex workq.Execution) error {
	if bodyExecutor.TryPushBack(wk) {
		ex.Starting()
		return nil
	}
	if !ex.ShouldBlockOrPostpone() {
		return nil // postpone; retried (and blocked) when the scheduler worker parks
	}
	err := bodyExecutor.PushBack(ctx, wk)
	if err == nil {
		ex.Starting()
	}
	return err
}

// Run is the execpool.Task entry: recycle the shell first (inst is all it
// carries), then run the pass on this executor worker.
func (wk *flowFireWork) Run(ee *workerExEnv) {
	_ = ee
	inst := wk.inst
	wk.inst = nil
	flowFireWorkPool.Put(wk)
	inst.firingPass()
}

// Free is a no-op: the controller calls it right after Execute's successful
// handoff, possibly concurrently with Run, so it must not touch the shell
// (the funnelInstance precedent).
func (wk *flowFireWork) Free() {}

var flowFireWorkPool = omnipool.For[flowFireWork]()
