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

// FlowConfig controls flow-scope generation: with ScopeProb, a (sub)plan is
// wrapped in a streampool.WithFlow scope carrying one value key and one tag
// follow-up (docs/decisions/flow-design.md). A scoped plan is steps-only with
// StepsOnlyProb: the scope wraps just the plan's Steps — exiting while
// dispatched work is still outstanding, so the follow-up fires ASYNC from the
// drain — instead of the whole run (steps and drain, follow-up fires inline at
// scope exit).
type FlowConfig struct {
	ScopeProb     float64
	StepsOnlyProb float64
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
	expects   []flowExpect // enclosing contracts: own scope (if any) LAST
	own       bool         // this plan minted a scope of its own
	stepsOnly bool         // own scope wraps only the Steps (follow-up fires async from the drain)
	fires     atomic.Int32
	drained   atomic.Bool
	// carriers is the conservation oracle's model-unit count for the plan's OWN
	// scope: units the sim knows still carry the scope's riders — a dispatched
	// task until its body completes (startTask → launcher body exit), a
	// submitted skim item until its handler completes (submitTo → handler
	// exit), and a submitted funnel item until its instance's flush body
	// completes (submitTo → FlushFn exit, attributed per instance). Every sim
	// decrement happens-before the framework releases the corresponding rider
	// carrier, so at fire time the count MUST be zero — a nonzero read is a
	// premature fire (a carrier ref was dropped while its unit was still
	// outstanding). Asserted only when carrierAssert (no plan-baked
	// cancellation: teardown abandons units without running them, stranding
	// the sim-side count — the framework's own refs still release, unasserted).
	carriers      atomic.Int64
	carrierAssert bool
}

// carrierAdd adjusts the own-scope carrier count; a no-op when the plan is
// unscoped (nothing fires, nothing to guard).
func (c *controller) carrierAdd(delta int64) {
	if c.flow != nil && c.flow.own {
		c.flow.carriers.Add(delta)
	}
}

// flowStepsAsyncFires counts steps-only scopes whose follow-up had NOT yet
// fired when the scope exited — i.e. the fire went through the asynchronous
// executor path rather than inline at scope exit. Test observability only
// (TestFlowStepsOnlyScopeEndToEnd asserts the async path is actually
// exercised); an async fire completing between scope exit and the read only
// undercounts, so the assertion stays sound.
var flowStepsAsyncFires atomic.Int64

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
// correct, tag present. Launcher/accumulate bodies capture riders at
// dispatch, which always happens under the scope. Skim handlers are covered
// by either route: under a whole-run scope the drain runs inside it (the
// drive ctx carries the riders); under a steps-only scope the drain runs
// AFTER scope exit and the handler sees the ITEM's riders instead (a skim
// result is a flow continuation, CP-F7) — every sim item is submitted under
// the scope, so the contract is the same.
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
// (CP-F8): every enclosing value CROSSES intact — the scope encloses the
// funnel's wave (its dispatches all happen inside the scope, and the fan-in
// boundary is captured at dispatch, so a steps-only scope's exit before the
// flush changes nothing), structural context above the fan-in (only per-item
// riders added within the funnel's wave sever, and the sim adds none) — and
// every enclosing tag's presence survives via the union.
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

// flowFollowUpFn builds the oracle follow-up for the plan's own scope. Fired
// exactly once per scope (a single non-shared tag instance); the conservation
// assertion is that no sim-side model unit still carries the scope when it
// fires — every sim decrement happens-before the framework releases the
// corresponding rider carrier, so a nonzero count means a premature fire. The
// whole-run form additionally pins the fire after the drain returned (its
// scope exit IS post-drain); the steps-only form cannot — a fire mid-drain is
// exactly the async behavior it exists to exercise.
func (c *controller) flowFollowUpFn(t assert.TestingT) func(context.Context) error {
	fs := c.flow
	chk := assert.New(t)
	return func(context.Context) error {
		if fs.carrierAssert {
			chk.Zerof(fs.carriers.Load(),
				"flow follow-up fired with %d model carrier(s) outstanding (Plan#%d)",
				fs.carriers.Load(), c.Plan.ID)
		}
		if !fs.stepsOnly {
			chk.Truef(fs.drained.Load(),
				"flow follow-up fired before the scoped plan drained (Plan#%d)", c.Plan.ID)
		}
		fs.fires.Add(1)
		return nil
	}
}

// assertFlowFired asserts the nominal-end contract once the fire is due: the
// follow-up fires EXACTLY once (the oracle fn extends nothing, so its first
// firing is the true end) within a bounded wait. Called after the whole-run
// scope exits, or after the DRAIN for a steps-only scope (whose fire is
// asynchronous — dispatched to the executor when the last carrier releases;
// the wave keep-alive makes the drain wait for it, except the flush ctx's
// adopted refs, which release just after the barrier drops — hence
// Eventually, not inline-deterministic).
func (c *controller) assertFlowFired(t assert.TestingT) {
	fs := c.flow
	chk := assert.New(t)
	// Generous bound: the executor-path firing normally lands in microseconds;
	// the wait only bites when the contract is violated.
	const fireWait = 10 * time.Second
	chk.Eventuallyf(func() bool { return fs.fires.Load() == 1 },
		fireWait, time.Millisecond,
		"flow follow-up fired %d times; want exactly 1 (Plan#%d)", fs.fires.Load(), c.Plan.ID)
}

// runWithFlowScope wraps a WHOLE-RUN scoped plan's body (steps and drain):
// it mints nothing (identities were minted in Run), registers the follow-up
// oracle, and asserts the nominal-end contract after the scope exits. A
// steps-only scope is wrapped inside runInner instead (the scope must exit
// before the drain).
func (c *controller) runWithFlowScope(
	ctx context.Context, t assert.TestingT, body func(context.Context) error,
) error {
	fs := c.flow
	if fs == nil || !fs.own || fs.stepsOnly {
		return body(ctx)
	}
	own := &fs.expects[len(fs.expects)-1]
	err := streampool.WithFlow(ctx, body,
		own.key.Value(own.val),
		own.tag.FollowUpFn(c.flowFollowUpFn(t)))
	c.assertFlowFired(t)
	return err
}
