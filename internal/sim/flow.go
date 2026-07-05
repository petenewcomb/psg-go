// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/assert"
)

// FlowConfig controls flow-scope generation: with ScopeProb, a (sub)plan's
// whole run — steps AND drain — is wrapped in a streampool.WithFlow scope
// carrying one value key and one tag follow-up (docs/decisions/flow-design.md).
type FlowConfig struct {
	ScopeProb float64
}

// flowExpect is one enclosing scope's observable contract, threaded to every
// body the plan runs. Expectations inherit dynamically at subjob entry by
// PROBING the ctx (flowExpectsForCtx) rather than statically: a subjob
// descending through a funnel FLUSH legitimately sees the ancestor's value
// severed while its tag presence survives the fan-in, and only the ctx knows
// which path was taken.
type flowExpect struct {
	key streampool.FlowKey[int]
	val int
	tag streampool.FlowTag
}

// flowState is the per-controller flow oracle state. Nil when the plan is
// unscoped and no ancestor expectations survive at entry.
type flowState struct {
	expects []flowExpect // enclosing contracts: own scope (if any) LAST
	own     bool         // this plan minted a scope of its own
	fires   atomic.Int32
	drained atomic.Bool
}

// flowExpectsForCtx filters ancestor expectations by probing ctx at subjob
// entry: an expectation is inherited only if its key still reads present
// there (a flush-descended subjob sees it severed). A present key must carry
// the right value — anything else is misdelivery. The tag assertion rides
// with the kept value expectation; tags additionally survive fan-ins, so a
// severed value with a still-present tag is legal and simply drops the pair
// from value assertions (the union already has dedicated coverage).
func flowExpectsForCtx(ctx context.Context, t assert.TestingT, parent *controller) []flowExpect {
	if parent == nil || parent.flow == nil {
		return nil
	}
	chk := assert.New(t)
	var kept []flowExpect
	for _, e := range parent.flow.expects {
		v, ok := e.key.From(ctx)
		if !ok {
			continue // absent (e.g. a NewFlow root) — legal; a present key must match
		}
		chk.Equalf(e.val, v, "flow key misdelivery at subjob entry: got %d want %d", v, e.val)
		chk.Truef(e.tag.InFlow(ctx), "flow tag absent while its bundle value is present")
		kept = append(kept, e)
	}
	return kept
}

// assertFlowInBody asserts every enclosing scope's contract inside a
// launcher body, accumulate body, or skim handler: value present and
// correct, tag present. These all run on unbroken dispatch/drive chains
// under the scope (the drain is inside the scope, so skim handlers see the
// driver's riders).
func (c *controller) assertFlowInBody(ctx context.Context, t assert.TestingT, where string) {
	if c.flow == nil {
		return
	}
	chk := assert.New(t)
	for _, e := range c.flow.expects {
		v, ok := e.key.From(ctx)
		chk.Truef(ok, "flow value absent in %s (Plan#%d)", where, c.Plan.ID)
		if ok {
			chk.Equalf(e.val, v, "flow value mismatch in %s (Plan#%d)", where, c.Plan.ID)
		}
		chk.Truef(e.tag.InFlow(ctx), "flow tag absent in %s (Plan#%d)", where, c.Plan.ID)
	}
}

// assertFlowInFlush asserts the fan-in contract inside a funnel Flush body
// (CP-F8): every enclosing value CROSSES intact — the scope wraps the whole run,
// so it is the driver flow enclosing the funnel's wave, structural context above
// the fan-in (only per-item riders added within the funnel's wave sever, and the
// sim adds none) — and every enclosing tag's presence survives via the union.
func (c *controller) assertFlowInFlush(ctx context.Context, t assert.TestingT) {
	if c.flow == nil {
		return
	}
	chk := assert.New(t)
	for _, e := range c.flow.expects {
		v, ok := e.key.From(ctx)
		chk.Truef(ok, "enclosing flow value severed at the flush (Plan#%d)", c.Plan.ID)
		if ok {
			chk.Equalf(e.val, v, "flow value mismatch at the flush (Plan#%d)", c.Plan.ID)
		}
		chk.Truef(e.tag.InFlow(ctx), "flow tag lost at the accumulate→flush fan-in (Plan#%d)", c.Plan.ID)
	}
}

// runWithFlowScope wraps body per the plan's flow spec. For a scoped plan it
// mints the key/tag, registers the follow-up oracle, and asserts the
// nominal-end contract after the scope exits: the follow-up fires EXACTLY
// once (the oracle fn extends nothing, so its first firing is the true end),
// only after the drain completed, and within a bounded wait — the wave
// barrier guarantees carriers release by drain return, except the flush
// ctx's adopted refs, which release just after the barrier drops (hence
// Eventually, not inline-deterministic).
func (c *controller) runWithFlowScope(
	ctx context.Context, t assert.TestingT, body func(context.Context) error,
) error {
	fs := c.flow
	if fs == nil || !fs.own {
		return body(ctx)
	}
	chk := assert.New(t)
	own := &fs.expects[len(fs.expects)-1]
	err := streampool.WithFlow(ctx, body,
		own.key.Value(own.val),
		own.tag.FollowUpFn(func(context.Context) error {
			chk.Truef(fs.drained.Load(),
				"flow follow-up fired before the scoped plan drained (Plan#%d)", c.Plan.ID)
			fs.fires.Add(1)
			return nil
		}))
	// Generous bound: the executor-path firing normally lands in microseconds;
	// the wait only bites when the contract is violated.
	const fireWait = 10 * time.Second
	chk.Eventuallyf(func() bool { return fs.fires.Load() == 1 },
		fireWait, time.Millisecond,
		"flow follow-up fired %d times; want exactly 1 (Plan#%d)", fs.fires.Load(), c.Plan.ID)
	return err
}
