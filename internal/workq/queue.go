// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

// ─────────────────────────────────────────────────────────────────────────────
// Queue is the combined work engine of the consolidation (see WORKING_NOTES
// "Worker pool + workq consolidation"). It bundles the producer→worker handoff
// (incoming) with the fresh/postponed priority engine + scheduled work
// (accepted), presenting one slim surface: Post (producer) and — driven by a
// Worker — driveOne (consumer). The deep priority-engine internals are still
// delegated to Accepted; a later pass may absorb them.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Queue is the combined work engine. It hides the two things the rest of the
// system used to juggle separately:
//
//   - the INCOMING handoff (producers Post work here; Workers pull it), and
//   - the fresh/postponed PRIORITY engine + scheduled/timed work.
//
// One Queue per stage: the task/funnel engine (driven by worker.Pool goroutines)
// and the skim engine (driven by user Skim goroutines). Producers call Post;
// consumers drive it through a Worker. The Queue is wave-agnostic — work items
// are tagged with their wave and carry their own per-wave cancellation.
type Queue struct {
	// incoming is the producer→worker handoff. Post pushes here; a Worker's
	// pull drains from here into the priority engine.
	incoming Pending

	// accepted is the fresh/postponed priority engine plus scheduled/timed
	// work. FIRST CUT: composed (hidden) to reuse its controller while
	// presenting the slim API; a later pass folds its fields in and replaces
	// ExecuteOne with a native driveOne.
	accepted Accepted

	// unmetDemandFn is the CONDITION signal "there is fresh work and possibly
	// no taker." It is reaction-agnostic: worker.Pool wires it to spawnWorker;
	// the skim Queue wires it to nil (user goroutines drive — no spawn). It is
	// fired from three sites — the Post handoff bufferedFn, the Post about-to-
	// wait point, and the batch-drain inside accepted — but it is one hook.
	unmetDemandFn RenotifyFunc
}

// Init wires the queue. unmetDemandFn is the condition signal (see the field);
// nil for queues whose driver population doesn't react to demand.
func (q *Queue) Init(unmetDemandFn RenotifyFunc) {
	q.incoming.Init()
	q.accepted.Init(unmetDemandFn)
	q.unmetDemandFn = unmetDemandFn
}

// ── Producer side ────────────────────────────────────────────────────────────

// Post is the HANDOFF, and nothing more: it delivers w to a worker. The op's
// LIMITER (post-admission, at the worker) and the wave's GOVERNOR (at top-level
// submit) are satisfied outside Post — so the queue has no admission or limiter
// logic and the op type is irrelevant here.
//
// It is the single producer path that replaces the three near-identical
// hand-rolled loops (taskPostWork / skimPostWork / funnelPostWork): try a
// non-blocking push; if that fails and the work can wait, either LISTEN (nested
// work: subscribe to the sender's drain and return to be re-driven) or BLOCK
// (top-level: park on the sender's outbox until a worker takes the item).
//
// shouldBlock is the caller's meta.ShouldBlock() — block (top-level) vs listen
// (nested). onWait, if non-nil, fires just before the producer waits (used to
// register downstream backpressure on the wave governor); it matches the legacy
// waiting() hook. Demand (unmetDemandFn) is fired whenever the item buffers or
// the producer is about to wait, so a worker is always requested before we park.
func (q *Queue) Post(
	ctx context.Context,
	ex Execution,
	sender *rdvq.Sender,
	shouldBlock bool,
	w Work,
	onWait func(),
) (posted bool, err error) {
	bufferedFn := rdvq.BufferedFunc(q.unmetDemandFn)
	tryPost := func() bool { return q.incoming.TryPushBack(sender, w, bufferedFn) }

	for {
		if tryPost() {
			posted = true
			break
		}

		if !ex.ShouldBlockOrPostpone() {
			// The work cannot wait (no listener wiring). It will be retried by
			// whoever re-drives it; nothing more to do here.
			return false, nil
		}

		// About to wait: ensure a worker is demanded. This replaces the legacy
		// per-producer spawn triggers (task registerDemand-on-fail, funnel
		// maybeSpawn) with the uniform demand signal; for worker.Pool it
		// requests a goroutine.
		q.fireDemand()

		if !shouldBlock {
			// LISTEN: subscribe to the sender's drain notification, retry once
			// (closing the race between the failed push and the subscription),
			// then return to be re-driven when the outbox drains.
			ex.AddToListeners(q.incoming.ListenersFor(sender))
			if tryPost() {
				posted = true
				break
			}
			if onWait != nil {
				onWait()
			}
			return false, nil
		}

		// BLOCK: park on the sender's outbox until a worker takes the item.
		posted = true
		q.incoming.PushBackFunc(sender, w, bufferedFn, func(outboxCh chan<- Work) bool {
			posted = false
			ex.Blocking()
			if onWait != nil {
				onWait()
			}
			var sent bool
			sent, err = rdvq.BasicPushSelect[Work](ctx, outboxCh, w)
			posted = sent
			return sent
		})
		if posted || err != nil {
			break
		}
	}

	if posted {
		ex.Starting()
	}
	return posted, err
}

// fireDemand fires the demand signal if one is wired (nil for queues whose
// drivers don't react to demand, e.g. the skim Queue).
func (q *Queue) fireDemand() {
	if q.unmetDemandFn != nil {
		q.unmetDemandFn()
	}
}

// ── Consumer side (driven by a Worker) ───────────────────────────────────────

// driveOne processes at most one item under the priority discipline
// (fresh → postponed → new via addWorkFn) — the single consumer primitive behind
// Worker.DriveOne. FIRST CUT delegates to the legacy controller; the native
// version inlines drainScheduled → fresh → postponed → addWorkFn and merges the
// old ExecuteOne/TryExecuteOne. Worker[E] (same package) calls this directly and
// supplies the addWorkFn that pulls from q.incoming.
func (q *Queue) driveOne(ctx context.Context, addWorkFn AddWorkFunc) error {
	return q.accepted.ExecuteOne(ctx, addWorkFn)
}

// ── Scheduled / timed work (flush deadlines) ─────────────────────────────────
//
// Schedule a ScheduledWork to become ready at a deadline; due items drain into
// fresh during driveOne. Reschedule/ClaimForFlush stay; Remove/Expedite become
// private (no external callers).

func (q *Queue) Schedule(w ScheduledWork, at time.Time)        { q.accepted.Schedule(w, at) }
func (q *Queue) Reschedule(w ScheduledWork, at time.Time) bool { return q.accepted.Reschedule(w, at) }
func (q *Queue) ClaimForFlush(w ScheduledWork) bool            { return q.accepted.ClaimForFlush(w) }
func (q *Queue) DrainAllScheduled(dst []ScheduledWork) []ScheduledWork {
	return q.accepted.DrainAllScheduled(dst)
}
