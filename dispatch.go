// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

//go:build ignore

// NOTE: build-excluded design sketch. This is the step-4 dispatch wiring — the
// uniform `submit` shape; it references Wave.governor()/blockBehaviorFor which land
// when submit is wired. Kept in-tree for shape review (see WORKING_NOTES "Worker
// pool + workq consolidation" → Dispatch). Drop the build tag when wiring submit.
//
// The permit ACQUIRE model is NOT sketched here — see docs/permit-core.md (pools,
// locality-ordered acquire, idle-steal) and docs/dispatch-execution-split.md
// ("Permits: managed off the executor") for where and how permits are acquired.

package psg

// ─────────────────────────────────────────────────────────────────────────────
// DRAFT SKETCH — the uniform dispatch. NOT wired in; references conceptual
// accessors. Shows the collapse of taskPostWork + funnelPostWork + skimPostWork
// into ONE submit, and the minimal-WIP admission model. See WORKING_NOTES
// "Worker pool + workq consolidation" → "Backpressure & admission model".
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/workq"
)

// submit is the single dispatch path for every op. There is no task / funnel /
// skim distinction — they all submit identically. The ONLY axis that matters is
// whether this submit is TOP-LEVEL (a user goroutine, holding no permit) or
// NON-TOP-LEVEL (a body running on a worker, holding its op's permit):
//
//   - NON-TOP-LEVEL submits are admitted UNCONDITIONALLY. Gating a permit-holding
//     body could hold-and-wait deadlock, and it isn't needed — downstream
//     backpressure reaches the top via that body's own scheduler saturation.
//   - TOP-LEVEL submits pass the wave's admission gate: wait while any source the
//     wave feeds has its buffer-1 on-deck slot full (minimal WIP — input paced by
//     the narrowest bottleneck).
//
// The op's LIMITER is NOT acquired here — submit only hands off. The permit is
// acquired at ADMISSION by a manager (non-blocking: acquire-or-postpone, holding
// the work un-admitted on a miss) and reacquired mid-body by the executor when it
// resumes from a drive (see docs/dispatch-execution-split.md, "Permits: managed off
// the executor"). A postponed candidate registers on the wave governor — exactly as
// a saturated skimmer does.
//
// q is the TARGET stage Queue, chosen by the caller per op type (the only place
// the op kind matters — and only to route, not to branch the logic):
//   - launcher / funnel work → the POOL's shared work queue (driven by
//     worker.Pool goroutines);
//   - skim work → this WAVE's skim queue (driven by user Skim goroutines).
//
// Skim work is always produced by a worker body, so it is always non-top-level →
// the unconditional handoff path; the admission gate only ever applies to
// top-level launcher/funnel submits.
func submit(
	ctx context.Context,
	meta *ctxMeta, // the dispatching ctx's metadata; embeds the exEnv, so it
	// answers IsTopLevel() directly. Passed (not re-derived from ctx)
	// per the codebase's (ctx, meta) idiom — and kept distinct from ex, which is
	// workq's per-work admission Execution, a different layer.
	ex workq.Execution,
	q *workq.Queue, // target stage queue (pool work queue, or this wave's skim queue)
	wave *Wave, // for the per-wave admission governor (top-level only)
	w workq.Work,
	deadline time.Time,
) (posted bool, err error) {
	// The HANDOFF — deliver w to a worker (the limiter is the worker's problem).
	handoff := func(ctx context.Context, ex workq.Execution) error {
		posted, err = q.Post(ctx, ex, deadline, w)
		return err
	}

	// Non-top-level: unconditional. Deadlock-freedom — the body must be able to
	// finish its submits, exit, and release.
	if !meta.IsTopLevel() {
		return posted, handoff(ctx, ex)
	}

	// Top-level admission. wave.governor aggregates the wave's saturation
	// sources — its skimmers' on-deck slots and its ops' schedulers' on-deck
	// slots; gov.Execute IS ExecuteOrWait on it: "wait while any fed source is
	// on-deck-full, then run the handoff." Wave-scoping is the user's declared
	// granularity (a wave moves as a unit), not an approximation.
	err = wave.governor().Execute(ctx, ex, deadline, blockBehaviorFor(ex), handoff)
	return posted, err
}

// ── how q is chosen (caller side, by op type) ────────────────────────────────
//
//	launcher / funnel op → pool.workQueue   (shared task/funnel engine)
//	skim op              → wave.skimQueue    (per-wave skim engine)
//
// ── conceptual helpers (supplied during hardening) ───────────────────────────
//
//	Wave.governor()        — the per-wave governor (aggregates this wave's
//	                         skimmers' + ops' schedulers' on-deck signals)
//	blockBehaviorFor(ex)   — the pool BlockBehavior; canBlock (top-level) lives here
//
// (ctxMeta already provides IsTopLevel() — it embeds the exEnv.)
