// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package permits is the isolated, model-checked sketch of the hierarchical permit
// cache specified in docs/permit-core.md. It exists to be model-checked under
// pgregory.net/rapid before any cutover — it is NOT yet wired into the live limiter
// (limiter.go's eager directRequest/suspendForEpisode/reclaimRequest remain
// load-bearing until the dispatch/execution split lands).
//
// # The three types, and why they split
//
//   - [Resource] is the pluggable accounting object permits are drawn from — the
//     open extension point (semaphore, memory, rate, weighted; each a small
//     accounting object). It is the only thing that knows capacity.
//   - [Pool] is the Resource boundary and the root of a forest of caches. It is the
//     ONLY place permits cross in or out of the Resource: a cache checks a permit
//     OUT of the Resource (step 3 of acquire), and a destroyed cache drains its
//     held permits back THROUGH the Pool to the Resource. The Pool itself never
//     caches.
//   - [Cache] is a per-unit node in the forest — one per wave/sub-wave. Caches
//     cache-don't-return: a finished body's permit stays in the cache's held,
//     borrowable, until the cache is destroyed or another cache steals it. The
//     unit→sub-wave nesting is the cache tree.
//
// This Pool/Cache split is not just naming: a Pool returns permits to the Resource
// while a Cache caches them. That genuine behavioral difference is the type
// boundary (permit-core.md "Permits as a hierarchical cache").
//
// # What this models, and what it does not
//
// A SEQUENTIAL reference model of the permit ALGORITHM. The design's deadlock-
// freedom is a structural property ("a parked holder's permit is idle, hence
// borrowable") that a state machine modeling park/resume/complete as explicit
// transitions verifies without real goroutines. The eventual concurrent impl's
// locking is a separate, later concern; the algorithm is what is validated here.
//
// # The held/inUse accounting (made explicit)
//
// permit-core.md frames each cache with two numbers — held (permits cached) and
// inUse (permits of held backing a running body) — but leaves the cross-cache
// update rules implicit. This package pins them down:
//
//   - Conservation. A permit checked out of the Resource lives in EXACTLY ONE
//     cache's held; Σheld over all caches equals the Pool's checked-out count. A
//     steal is a transfer (one cache held−−, another held++) that never changes the
//     sum; only a fresh check-out raises it and only cache destruction lowers it.
//   - inUse is local to the cache that HOLDS the permit. A running body occupies one
//     permit via a [Permit] handle recording its BACKING cache — the cache whose
//     inUse the locality search bumped. A child running on a parked ancestor's idle
//     permit bumps the ANCESTOR's inUse; the permit stays in the ancestor's held.
//   - borrowable(c) = held − inUse is c's idle subset, nonzero only while c's unit
//     is parked (a running unit has inUse == held for its cache-of-one).
//
// # Operations
//
//   - [Cache.Acquire] is the single locality-ordered primitive (permit-core.md
//     "Acquisition"): own cache → ancestor chain → free Resource → steal → wait. It
//     takes the caller-held [Demand] identity and a weight, returning a [Permit]
//     (recording the backing cache and weight), a zero Permit to wait, or an
//     [OverdraftResource] refusal error (the unit's distinct failure). Steps 1–2
//     occupy existing borrowable permits; steps 3–4 (delegated to the Pool) check
//     capacity out of the Resource or steal it into the acquiring cache's held —
//     assembling a weight no single source covers by a multi-source gather whose
//     partial hoard stays borrowable throughout (weighted-acquisition.md
//     Decision 1); a registered head whose gather exhausts a provably infeasible
//     forest runs the overdraft evaluation (weighted-acquisition.md §Overdraft).
//   - [Permit.Release] ends a run segment (body completed or parked): backing
//     inUse−−. The permit STAYS cached in held (cache-don't-return), now borrowable.
//   - Park / resume are Release / Acquire: parking releases the body's permit (now
//     lendable to the sub-wave's bodies); resuming reacquires it (normally a step-1
//     own-cache hit). (permit-core.md "Driving is an alternation".)
//   - [Cache.SuspendDriver] / [Cache.ResumeDriver] bracket a holder's park-for-drive
//     episode on the drive-target wave's cache — the attribution the overdraft
//     stranger check reads (§Overdraft resolution (c)).
//
// # Lifetime
//
// A Cache is reference-counted over its unit plus every live sub-wave drawing on it
// (pools outlive units). It returns its held to the Resource only when the refcount
// reaches zero (unit exited AND all sub-waves drained), never at unit completion.
//
// # Invariants the model-check targets (permit-core.md "Invariants")
//
//   - Per-cache: 0 ≤ inUse ≤ held — except inside a standing overdraft episode's
//     exempt subtree, where inUse may exceed held by exactly what was claimed from
//     the episode allowance.
//   - Conservation: Σheld == Pool.checkedOut ≤ capacity — untouched by overdraft
//     (the grant enters neither held nor checkedOut).
//   - Concurrency bound: Σinuse ≤ capacity + episodeTotal (== capacity outside an
//     episode).
//   - Episode: Σ max(inUse−held, 0) + allowance == episodeTotal, zero outside an
//     episode; the allowance is necessarily fully home at episode end.
//   - Liveness: no reachable state has a blocked Acquire of weight w while the
//     gatherable capacity — borrowable permits anywhere plus free Resource capacity —
//     covers w (the gather must assemble it) — the operational face of
//     deadlock-freedom, generalized from the weight-1 "some permit is borrowable".
package permits
