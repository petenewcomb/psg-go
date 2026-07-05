// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"fmt"
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
//   - +1 per ENCLOSING instance held by each inner (later-registered) instance
//     from registration until the inner's single fire completes (holds below) —
//     the peel that makes an outer follow-up wait for the whole nested subtree.
//
// The follow-up fires EXACTLY ONCE, when the count reaches zero (a single
// atomic transition — one winner). Its own rider is PEELED by construction — its
// binding lives on its own chain node and the fire carries node.next (enclosing
// below) — so its dispatches cannot re-reference it: nothing re-fires it, and
// re-extending the flow under its identity is an explicit re-stamp inside the
// body. The fire carries the ENCLOSING chain (enclosing), so its extensions hold
// the outer instances — which is why an outer cannot reach zero (cannot fire)
// until this instance's fire and everything it spawned have drained (LIFO nesting).
//
// Instances are currently GC-owned; pooling arrives with a later allocation
// pass (a firing is cold — once per flow end — so the alloc is off the hot path).
type flowInstance struct {
	// fn is the type-erased user follow-up (FlowKey/FlowTag.FollowUp wrap the
	// typed handler into this shape). It receives the bundle value (val below)
	// and returns the follow-up's error.
	fn func(ctx context.Context, value any) error
	// val is the bundle value passed to fn — the key's value, or nil for a tag
	// or a valueless key. Captured at registration (buildFlowRiders).
	val any
	// enclosing is the ENCLOSING rider chain the fire ctx carries: the node.next
	// below this instance's own node — the values and follow-up instances
	// registered before it (this instance PEELED by construction, its value
	// delivered as fn's argument instead). fn's dispatches inherit it, so they hold
	// the outer instances — the nested-lifetime coupling — while never
	// re-referencing this instance.
	enclosing *flowRiderNode
	// holds is the enclosing instances this (inner) instance references from
	// registration until its fire completes, released in fire(). Bridges the gap
	// before the fire's own fnRiders ref takes over, so an outer never reaches
	// zero out from under a not-yet-fired inner regardless of unref order.
	holds []*flowInstance
	count atomic.Int64
}

func (in *flowInstance) ref() {
	in.count.Add(1)
}

// unref releases one carrier; the release that reaches zero fires the follow-up
// EXACTLY ONCE. inline selects how fn runs:
//   - true (WithFlow scope exit only): directly on the caller's own frame; fn's
//     error is RETURNED, up to WithFlow's join. wave is nil.
//   - false (work-item or fire completion): a wave-rooted fire dispatched to the
//     executor — those paths run inside Free/release machinery where user code
//     must not run (an fn draining its wave would deadlock). wave is the finishing
//     item's wave; it is kept alive across the hop (IncrementReference, sound
//     because the triggering item's own work reference has not yet dropped) and
//     the fire's error routes to its errSink. Returns nil (the fire is async).
func (in *flowInstance) unref(inline bool, wave *Wave) error {
	if in.count.Add(-1) != 0 {
		return nil
	}
	if inline {
		m := bodyMetaPool.Get()
		m.ctxType = topLevelContext
		m.riders = in.enclosing
		flowRefRiders(m.riders)
		//nolint:contextcheck // scope-exit fire runs on the caller's own frame
		ctx := ctxpool.WithValue(context.Background(), m)
		return in.runFire(ctx, true, nil, nil)
	}
	wave.state.IncrementReference()
	wk := flowFireWorkPool.Get()
	wk.Init(workq.NewGroupID())
	wk.inst = in
	wk.wave = wave
	defaultPool.ForceFresh(wk)
	return nil
}

// runFire runs fn once under bodyCtx (which carries fnRiders — the enclosing set —
// so fn's dispatches hold the outer instances), delivers fn's error (returned when
// onErr is nil, else handed to onErr while bodyCtx is still live), then releases
// bodyCtx and finally this instance's holds on the enclosing instances (which may
// cascade to fire an outer). The holds release AFTER releaseBodyContext (defer
// ordering), so each outer's count is covered until this fire is fully done — no
// outer fires early regardless of unref order. inline/wave describe how a cascaded
// outer fires; an inline outer's error joins here (own error first). A panic in fn
// propagates, like every user body.
func (in *flowInstance) runFire(
	bodyCtx context.Context, inline bool, wave *Wave, onErr func(error),
) (err error) {
	// Deferred first → runs last: release the enclosing holds only after
	// releaseBodyContext.
	//nolint:contextcheck // a cascaded async fire roots at the scheduler ctx by design
	defer func() {
		holds := in.holds
		in.holds = nil
		for _, out := range holds {
			if e := out.unref(inline, wave); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	defer releaseBodyContext(bodyCtx)
	fnErr := in.fn(bodyCtx, in.val)
	if onErr != nil {
		if fnErr != nil {
			onErr(fnErr) // route to the wave errSink while bodyCtx is still live
		}
	} else {
		err = fnErr
	}
	return err
}

// flowRefRiders / flowUnrefRiders take and release one carrier reference on
// every follow-up instance in a rider chain (each instance appears on at most one
// node per chain, so this is one ref per instance). Paired by construction:
// borrowBodyContext and fire ref; releaseBodyContext unrefs. Derived metas
// (ensureCtxMeta) and the flush sever clone inherit chains WITHOUT refs and are
// released via paths that do not unref (releaseTopLevelContext) or carry nil
// riders — they are synchronous extents covered by their enclosing carrier's ref.
func flowRefRiders(r *flowRiderNode) {
	for n := r; n != nil; n = n.next {
		if n.inst != nil {
			n.inst.ref()
		}
	}
}

// flowUnrefRiders releases the carrier refs of r against wave — the wave of the
// context being released (releaseBodyContext reads it from the meta before
// teardown). A release that ends an instance's flow dispatches a wave-rooted
// fire; the unref return is always nil on this async path (the fire routes its own
// error to wave's errSink).
func flowUnrefRiders(r *flowRiderNode, wave *Wave) {
	for n := r; n != nil; n = n.next {
		if n.inst != nil {
			_ = n.inst.unref(false, wave)
		}
	}
}

// flowErrSink is the framework-owned, wave-agnostic error sink for follow-up
// firings (the funnelErrSink shape): its handler returns the error as-is so it
// surfaces via the target wave's SkimAll path. A single package-level sink serves
// every follow-up on every wave — the firing supplies the target wave.
var flowErrSink = newInternalSkimmer[struct{}](NewErrHandler(func(_ context.Context, err error) error {
	return err
}))

// flowFireWork carries a wave-rooted follow-up firing to the executor (the
// funnelInstance pattern): the fire runs user code, so it gets a pool worker and
// never runs inside completion/release machinery. It embeds a bare workq.WorkItem
// (not poolWork) — the wave is kept alive by the IncrementReference the dispatch
// took, dropped in Run — and rides borrowSrcCtx, the stable scheduler ctx stashed
// by Execute, so the fire body ctx roots at the wave/scheduler, not a recycled
// per-item ctx.
type flowFireWork struct {
	workq.WorkItem
	inst         *flowInstance
	wave         *Wave
	borrowSrcCtx context.Context //nolint:containedctx // borrow source for the fire body ctx
}

// Execute is the scheduler side: stash the borrow source for Run (before the
// publishing handoff, mirroring funnelInstance.Execute), then hand the fire to the
// executor. TryPushBack first; when the scheduler worker is prepared to park, a
// blocking PushBack.
func (wk *flowFireWork) Execute(ctx context.Context, ex workq.Execution) error {
	wk.borrowSrcCtx = ctx
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

// Run is the execpool.Task entry: recycle the shell first, then run the fire on
// this executor worker under a wave-rooted body ctx, routing fn's error to the
// wave's errSink, and finally drop the keep-alive reference the dispatch took.
//
//nolint:contextcheck // src is the borrow source for the fire body ctx, not a propagated arg
func (wk *flowFireWork) Run(ee *workerExEnv) {
	inst := wk.inst
	wave := wk.wave
	src := wk.borrowSrcCtx
	wk.inst = nil
	wk.wave = nil
	wk.borrowSrcCtx = nil
	flowFireWorkPool.Put(wk)

	// Wave-rooted fire body ctx: bound to the finishing wave, on this worker's
	// environment, carrying the instance's peeled enclosing rider set. Rooted at
	// the stable scheduler ctx (src), never a recycled per-item ctx.
	m := bodyMetaPool.Get()
	m.wave = wave
	m.ctxType = skimContext
	m.executionEnvironment = ee
	m.riders = inst.enclosing
	flowRefRiders(m.riders)
	bodyCtx := ctxpool.WithValue(src, m)

	// runFire returns nil here (onErr routes the error); the async fire owns it.
	_ = inst.runFire(bodyCtx, false, wave, func(fnErr error) {
		ctx2, meta := wave.ctxMeta(bodyCtx)
		if e := flowErrSink.submit(ctx2, meta, workq.NewGroupID(), struct{}{}, fnErr); e != nil &&
			ctx2.Err() == nil {
			panic(fmt.Sprintf("streampool: unexpected error routing follow-up error: %v", e))
		}
	})
	wave.state.DecrementReference()
}

// Free is a no-op: the controller calls it right after Execute's successful
// handoff, possibly concurrently with Run, so it must not touch the shell
// (the funnelInstance precedent).
func (wk *flowFireWork) Free() {}

var flowFireWorkPool = omnipool.For[flowFireWork]()
