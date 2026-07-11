// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"errors"

	"github.com/petenewcomb/streampool/internal/ctxpool"
)

// PinFlow makes ctx's flow valid beyond its extent: it returns a pinned
// context — an ordinary, shareable, retainable Go context — that carries the
// flow's riders (values, tag presence, follow-up lifetimes) until [UnpinFlow]
// releases it. An explicit pin is the purchase of Go's normal context
// contract: framework contexts are pooled and call-scoped (that is what keeps
// per-dispatch cost down), and the pin is the paid-for opt-out
// (docs/decisions/context-pinning-and-origin-access.md).
//
// PinFlow must be called inside the extent where ctx is valid — a body, a
// scope, or another pin's window; that is when the flow's state is provably
// alive to reference. The returned ctx is the token: pass it around, pin it
// again for an independent pin (pins compose freely; to hand off, overlap —
// p2 := PinFlow(p1); UnpinFlow(p1)), and eventually pass exactly it to
// UnpinFlow.
//
// A pinned ctx CARRIES its flows: follow-ups wait for the last unpin (and for
// any work dispatched from the pin), so a leaked pin holds its follow-ups
// open forever — treat it like any unclosed resource. What it deliberately
// does NOT carry is the source extent: no ambient wave (dispatch from a pin
// requires op.In(&wave) and behaves as an ordinary top-level submission,
// under the ordinary caveats about submitting to and skimming a wave from
// multiple goroutines), no limiter permit, no executor state, and no
// cancellation or deadline — the pinned ctx roots at [context.Background].
// Inside a body, dispatch through the body ctx; the pin is for later.
//
// The standard pattern for cancelable retention composes the pin with
// ordinary context machinery:
//
//	pinned := psg.PinFlow(ctx)
//	retained, cancel := context.WithCancel(pinned)
//	context.AfterFunc(retained, func() {
//		if err := psg.UnpinFlow(pinned); err != nil {
//			// A flow ended at this release and a follow-up returned err.
//			// This is its only delivery — deliver it somewhere you own.
//		}
//	})
//	// share retained; cancel() releases everything, idempotently
//
// Beyond convenience, this makes the pin's invalidation OBSERVABLE: a raw
// unpinned ctx goes invalid silently, while a canceled retained ctx announces
// it through ctx.Err() — restoring the full ordinary-Go discipline for every
// holder. The release is a fire site (see [UnpinFlow]), so the AfterFunc owns
// delivering any follow-up error: nothing in the framework re-delivers it.
//
// PinFlow of a bare, flow-less ctx returns a pin of the empty flow.
func PinFlow(ctx context.Context) context.Context {
	srcMeta, _ := metaFromContext(ctx)
	if srcMeta != nil {
		srcMeta.vetNotExpiredPin()
	}

	// The pin is minted, never blessed in place: a fresh meta whose owner ref
	// is the pin itself, carrying the source's rider chain under the pin's
	// own refs — takeable here because the caller's extent covers them — and
	// none of the source's extent state (held, exEnv, wave, parentWaves).
	// permitRoot stops every synchronous-extent walk at the pin, so a later
	// dispatch can never reach the source extent's recycled permit handle.
	m := newCtxMeta()
	m.ctxType = topLevelContext
	m.permitRoot = true
	m.pin.Store(pinLive)
	if srcMeta != nil {
		m.parent = srcMeta // position; the ref keeps the origin chain walkable
		refMeta(srcMeta)
		m.riders = srcMeta.riders
		flowRefRiders(m.riders) // the pin's carrier refs: the flow stays open
		nodeRef(m.riders)
	}
	// Rooted at Background, not ctx: a framework source is itself a pooled
	// child that recycles after its extent, and a retained ctx must not read
	// through it. The flow's values arrive via the rider chain; ancestry
	// (cancellation, deadline, ctx values) is deliberately not carried.
	//nolint:contextcheck // the Background root is the retention shield, by design
	pinned := ctxpool.WithValue(context.Background(), m)
	m.selfCtx = pinned
	return pinned
}

// UnpinFlow releases a pin taken by [PinFlow], ending the pinned ctx's
// validity: past the unpin, the ctx — and every context derived from it — is
// invalid, like any framework ctx past its extent. Work dispatched from the
// pin before the unpin is unaffected (it holds its own references); the flow
// ends when its last carrier — pin, dispatch, or scope — releases.
//
// UnpinFlow requires the exact ctx PinFlow returned and panics otherwise: on
// a derivative, a non-pin, or a second unpin of the same token. If this
// release ends a flow, its follow-ups run inline here — UnpinFlow is a
// synchronous user call site, the same shape as a WithFlow scope exit — and
// their errors are joined into the return value.
func UnpinFlow(ctx context.Context) error {
	m, ok := metaFromContext(ctx)
	if !ok || m.selfCtx != ctx || m.pin.Load() == pinNone {
		panic("streampool: UnpinFlow requires the exact context PinFlow returned")
	}
	if !m.pin.CompareAndSwap(pinLive, pinExpired) {
		panic("streampool: UnpinFlow of an already-unpinned context")
	}

	// Release the pin's carrier refs in chain order — the same walk-cover
	// discipline as every release site: at a count→0 the walker's refs on the
	// suffix are still held, so a fire's chain build is sound, with the pin
	// meta (owner ref still held here) as the fire's last carrier.
	riders := m.riders
	var err error
	for n := riders; n != nil; n = n.next {
		if n.inst != nil {
			//nolint:contextcheck // an inline fire roots its own ctx (the scope-exit shape)
			if e := n.inst.unref(true, nil, m); e != nil {
				err = errors.Join(err, e)
			}
		}
	}
	nodeUnref(riders)
	unrefMeta(m) // the pin's owner ref; children (dispatched work) may outlive it
	return err
}
