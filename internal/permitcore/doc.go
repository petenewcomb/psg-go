// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package permitcore is the isolated sketch of the hierarchical permit cache
// specified in docs/permit-core.md. It exists to be model-checked under
// pgregory.net/rapid before any cutover — it is NOT yet wired into the live
// limiter (limiter.go's eager directRequest/suspendForEpisode/reclaimRequest
// remain load-bearing until the dispatch/execution split lands).
//
// # What this models, and what it does not
//
// This is a SEQUENTIAL reference model of the permit ALGORITHM — pools, the
// locality-ordered acquire, cache-don't-return release, and partial-idle
// stealing — and of the invariants those operations must preserve. It is
// single-threaded on purpose: the design's deadlock-freedom is a *structural*
// property ("a parked holder's permit is idle, hence borrowable", permit-core.md
// "Deadlock-freedom, with no cycle graph"), which a state machine that models
// park/resume/complete as explicit transitions can verify without real
// goroutines. The eventual concurrent implementation's lock correctness is a
// separate, later concern; the algorithm it implements is what is validated here.
//
// # The held/inUse accounting (made explicit)
//
// permit-core.md frames pools with two numbers — `held` (permits cached) and
// `inUse` (permits of held backing a running body) — but leaves the cross-pool
// update rules implicit. This package pins them down:
//
//   - Conservation. A permit checked out of the backing store L lives in EXACTLY
//     ONE pool's `held`. `Σ held` over all pools equals L's checked-out count; a
//     steal is a transfer (one pool held−−, another held++) that never changes the
//     sum; only a fresh check-out (step 3) raises it and only pool destruction
//     lowers it. (permit-core.md "Invariants": Conservation.)
//   - inUse is local to the pool that HOLDS the permit. A running body occupies
//     one permit and records its BACKING POOL — the pool whose `inUse` it bumped,
//     found by the locality search. The permit does not move to the body's own
//     pool unless the search had to take a delta (steps 3–4). So a child running
//     on a parked ancestor's idle permit bumps the ANCESTOR's inUse; the permit
//     stays in the ancestor's held.
//   - borrowable(P) = held − inUse is P's idle subset, nonzero only while P's unit
//     is parked (a running unit has inUse == held for its pool-of-one).
//
// # Operations
//
//   - Acquire(p): a body in pool p wants to run. The one locality-ordered
//     primitive (permit-core.md "Acquisition") searches own pool → ancestor chain
//     → free L → steal → wait, and returns the BACKING pool whose inUse it bumped
//     (or blocks). Steps 1–2 occupy an existing borrowable permit in p or a parked
//     ancestor (permit unmoved). Step 3 checks a fresh permit out of L into p.held.
//     Step 4 steals an idle permit from anywhere in the forest into p.held. Step 5
//     waits (modeled as a blocked result; the caller retries on the next release).
//   - Release(backing): the body completes or parks; backing.inUse−−. The permit
//     STAYS in backing.held (cache-don't-return), now borrowable. (permit-core.md
//     "Permits as a hierarchical cache".)
//   - Park / resume are Release / Acquire: parking to drive a sub-wave releases the
//     body's backing permit (inUse−−, now lendable to the sub-wave's bodies);
//     resuming to run a skim handler or return reacquires it (normally a step-1 hit
//     on the unit's own freshly-idled permit). (permit-core.md "Driving is an
//     alternation".)
//
// # Pool lifetime
//
// A pool is reference-counted over its unit plus every live sub-wave drawing on it
// (permit-core.md "Ownership and lifetime: pools outlive units"). It returns its
// `held` to L only when the refcount reaches zero (unit exited AND all sub-waves
// drained), never at unit completion alone.
//
// # The invariants the model-check targets (permit-core.md "Invariants")
//
//   - Per-pool: 0 ≤ inUse ≤ held.
//   - Conservation: Σ held == L.checkedOut ≤ capacity(L).
//   - Concurrency bound: Σ inUse ≤ capacity(L).
//   - Liveness: no reachable state has a blocked acquire while some permit is
//     borrowable (the steal step must find it) — the operational face of
//     deadlock-freedom.
package permitcore
