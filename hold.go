// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/ctxpool"
)

// HoldFlow takes a GC-owned hold on ctx's flow and returns a cancelable
// context carrying it. It is the safe retention tier — a first-class surface,
// not sugar over [PinFlow]: where a pin is the pooled in-place primitive with
// the framework's usual extent rules (undefined behavior past its window), a
// held context is an ordinary garbage-collected Go object with NO undefined
// behavior, before or after release.
//
// While held, the flow stays open (the hold is a carrier: follow-ups wait for
// the release and for any work dispatched through held), values and tag
// presence read normally, and dispatch through held works as an ordinary
// top-level submission into an explicitly named wave (op.In(&wave) — a hold
// carries no ambient wave), under the ordinary multi-goroutine caveats.
//
// cancel is named for what it visibly does — it cancels held — and canceling
// is how the hold is released, in three phases: the hold's follow-up
// references are released in chain order — a flow ending here fires its
// follow-ups inline, errors collected — then held severs from the flow, then
// held is canceled with the passed cause MERGED with those errors (a nil
// cause defaults to [context.Canceled] first, so a follow-up error never
// becomes the primary cause). Every holder observes both through
// [context.Cause].
//
// After cancellation, held remains fully usable, exactly as Go's contract says a
// canceled context is: Err() and Done() report the cancellation, and reads
// return the flow's riders AS OF THE HOLD — held is a snapshot handle. The
// flow's liveness is signaled by Err(), never by read availability; the
// snapshot is what lets the goroutine woken by Done() still read the request
// ID for its cancellation log line. Post-cancel dispatch through held is
// defined but ordinary-canceled: the work carries the snapshot's values, no
// follow-up lifetimes, and a canceled ancestry.
//
// Reads through held are race-free against the cancel: they only ever touch
// GC-owned snapshot state, never pooled framework memory. Dispatch is the one
// asymmetry — dispatch racing the cancel may misattribute or panic, the same
// hazard class as dispatching from any ending extent; the owner sequencing
// dispatches against its own cancel is the natural contract.
//
// cancel is idempotent (later calls are no-ops, per the CancelCauseFunc
// convention). A hold whose cancel is never called keeps its flow open
// forever — treat cancel like any resource closer. HoldFlow must be called
// inside the extent where ctx is valid; holding a bare, flow-less ctx yields
// a hold of the empty flow.
func HoldFlow(ctx context.Context) (held context.Context, cancel context.CancelCauseFunc) {
	srcMeta, _ := metaFromContext(ctx)
	if srcMeta != nil {
		srcMeta.vetNotExpiredPin()
	}

	// Snapshot the rider chain into GC-owned copies — never pooled, so
	// nothing reachable from held can ever be recycled or re-stamped. The
	// LIVE chain carries the instance pointers (dispatch through held extends
	// the real follow-up lifetimes); the SEVERED chain is value-only (same
	// values and tag presence, no lifetime coupling). The permanent +1 ref
	// bias keeps a body borrow's nodeRef/nodeUnref cycle from ever pushing a
	// GC node into the pool. The hold's carrier refs on the real instances
	// are taken here, inside the caller's extent, where cover is provable.
	var live, severed *flowRiderNode
	if srcMeta != nil {
		live, severed = snapshotRiders(srcMeta.riders)
	}

	liveMeta := newHoldMeta(live)
	sevMeta := newHoldMeta(severed)

	cancelCtx, cancelInner := context.WithCancelCause(context.Background())
	h := &heldFlowCtx{}
	h.cur.Store(&heldState{valueCtx: ctxpool.WithValue(cancelCtx, liveMeta)})
	// Both value children share cancelCtx ancestry, so Done/Err/Deadline and
	// context.Cause resolve identically across the sever. Neither child is
	// ever Freed: a ctxpool child over a canceled parent falls out of the
	// pool by design and is reclaimed by GC with the hold.
	sevCtx := ctxpool.WithValue(cancelCtx, sevMeta)
	liveMeta.selfCtx = h
	sevMeta.selfCtx = h

	var once sync.Once
	cancel = func(cause error) {
		once.Do(func() {
			if cause == nil {
				cause = context.Canceled
			}
			// Fires first (errors must exist before the cancel that carries
			// them): release the carrier refs in chain order — the walk-cover
			// discipline of every release site, with the live hold meta as
			// the fires' last carrier. The chain is GC-owned, so a concurrent
			// reader mid-walk is safe throughout.
			var err error
			for n := live; n != nil; n = n.next {
				if n.inst != nil {
					//nolint:contextcheck // an inline fire roots its own ctx (the scope-exit shape)
					if e := n.inst.unref(true, nil, liveMeta); e != nil {
						err = errors.Join(err, e)
					}
				}
			}
			// Sever: dispatch after this point sees the value-only snapshot.
			h.cur.Store(&heldState{valueCtx: sevCtx})
			// Announce: cancellation is the liveness signal, its cause the
			// error delivery path.
			cancelInner(errors.Join(cause, err))
		})
	}
	return h, cancel
}

// snapshotRiders builds the hold's two GC-owned copies of chain: the live
// copy (instance pointers carried) and the severed, value-only copy. Both are
// permanently ref-biased so they can never enter the node pool. One carrier
// ref is taken per instance-bearing node, mirroring flowRefRiders.
func snapshotRiders(chain *flowRiderNode) (live, severed *flowRiderNode) {
	var buildLive func(n *flowRiderNode) *flowRiderNode
	buildLive = func(n *flowRiderNode) *flowRiderNode {
		if n == nil {
			return nil
		}
		c := &flowRiderNode{id: n.id, val: n.val, hasVal: n.hasVal, inst: n.inst, next: buildLive(n.next)}
		c.refs.Store(1) // permanent bias: GC-owned, never pooled
		if c.inst != nil {
			c.inst.ref() // the hold's carrier ref
		}
		return c
	}
	var buildSevered func(n *flowRiderNode) *flowRiderNode
	buildSevered = func(n *flowRiderNode) *flowRiderNode {
		if n == nil {
			return nil
		}
		c := &flowRiderNode{id: n.id, val: n.val, hasVal: n.hasVal, next: buildSevered(n.next)}
		c.refs.Store(1) // permanent bias: GC-owned, never pooled
		return c
	}
	return buildLive(chain), buildSevered(chain)
}

// newHoldMeta builds a meta for the hold: drawn from bodyMetaPool with its owner reference
// armed (Get sets refs=1) but NEVER released — that permanent owner ref is the bias a derived
// dispatch meta's parent unref cascade can never drop to zero, so the meta is never recycled
// and is reclaimed by GC with the hold. Wave-less and permit-rootless like a pin: a hold
// carries the flow, never the source extent.
func newHoldMeta(riders *flowRiderNode) *ctxMeta {
	m := bodyMetaPool.Get() // owner ref armed and held for the hold's life; never Released
	m.ctxType = topLevelContext
	m.permitRoot = true
	m.riders = riders
	return m
}

// heldFlowCtx is the held context: an ordinary GC-owned object delegating to
// the current inner value ctx (live before the cancel, severed after), both of
// which share the hold's cancelable ancestry. The swap is atomic; every
// method on a context is safe concurrently with the cancel.
type heldFlowCtx struct {
	cur atomic.Pointer[heldState]
}

type heldState struct {
	valueCtx context.Context //nolint:containedctx // the delegation target IS the state
}

func (h *heldFlowCtx) Deadline() (time.Time, bool) { return h.cur.Load().valueCtx.Deadline() }
func (h *heldFlowCtx) Done() <-chan struct{}       { return h.cur.Load().valueCtx.Done() }
func (h *heldFlowCtx) Err() error                  { return h.cur.Load().valueCtx.Err() }
func (h *heldFlowCtx) Value(key any) any           { return h.cur.Load().valueCtx.Value(key) }
