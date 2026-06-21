# Positioning Research

Research into what Go developers actually complain about when reaching for
worker-pool / concurrency abstractions, to guide the repositioning and
(possibly) renaming of this library.

## Methodology

Performed 2026-05-23 by a research subagent.

**Sources covered well:**
- GitHub issues on the top Go worker-pool / concurrency libraries: `sourcegraph/conc`, `panjf2000/ants`, `alitto/pond`, `gammazero/workerpool`.
- `golang/go` issue tracker for errgroup proposals (#27837, #53757).
- Hacker News threads on conc and structured concurrency (item IDs 34344514, 16922150, 24359650).
- golang-nuts thread `dN-0SlXtaZA` on recursive worker-pool deadlocks.
- Sourcegraph blog post introducing conc.

**Sources NOT accessible (real gap):**
- Reddit `/r/golang` — host blocked from the research environment.
- Stack Overflow — site blocked; SO-attributed phrasings below come from third-party search summaries and should be treated as weaker evidence.

Treat frequency rankings as directional, not census-grade.

---

## Summary

### Headline finding

**Streaming results is the dominant user pain.** Across every pool library
sampled and across multiple errgroup proposals, the single most repeated
frustration is "how do I get return values out of my tasks?" Users complain
that they must wire their own channels, shared maps, or wrapper structs to
get results back — which defeats the point of using a pool. The vocabulary
that surfaces this pain: *"results"*, *"collect results"*, *"return values
from goroutines"*, *"errgroup with results"*.

### Strongest positioning hooks (ranked)

1. **Streaming results from a worker pool** — highest user-vocabulary
   frequency, highest plausible search volume. This is the lead hook.
2. **Workers that can submit more work without deadlocking** — psg's most
   defensible technical differentiator. Vivid pain when it hits, but a
   smaller audience. Position as the *why-we're-different* beat behind hook
   #1.
3. **CPU vs I/O pool separation** — NOT a user-voiced pain. The research
   found no evidence developers complain in these terms. Position as a
   *consequence* of the streaming + recursive model, not as a headline.

### Vocabulary that lands

| Category | Use these | Frequency |
|---|---|---|
| Container nouns | pool, worker pool, goroutine pool | very high |
| Unit nouns | task | universal |
| Verb | submit | universal |
| Output nouns | results, stream, return values | high |
| Failure modes | deadlock, goroutine leak, "crash the whole process" | high |

### Vocabulary to avoid (it's ours, not theirs)

- **scatter-gather** — appears literally nowhere in user complaints.
- **fan-in / fan-out** — blog-and-tutorial vocabulary, not felt-pain vocabulary.
- **backpressure** — essentially absent; users say "blocks when busy" or "queue full."
- **structured concurrency** — library-author vocabulary; conc's marketing
  taught the term but users still describe their *problems* as "goroutine
  leaks" and "deadlocks."
- **task runner / concurrent executor** — barely appears; people say "pool."

### Strategic signals

- **conc is in maintenance limbo.** Issue
  [sourcegraph/conc#148](https://github.com/sourcegraph/conc/issues/148)
  explicitly notes 1+ year of silence. Users are actively shopping for a
  successor. This is a real opening.
- **Users can't tell ants/pond/conc apart.** Issue
  [alitto/pond#123](https://github.com/alitto/pond/issues/123) ("what is the
  difference with conc and ants?") is open and unanswered. Any new library
  must be unmistakably distinct in the first ten seconds of README reading.

### Recommended positioning

A tagline focused on PP1 (streaming results) with PP4 (recursive tasks) as
the second beat. Draft directions, to react against rather than adopt:

- "Like errgroup, but tasks return values."
- "A Go worker pool whose tasks stream results — and can spawn more tasks
  without deadlocking."
- "errgroup for tasks that return values and call themselves."

The current README leads with "Pipelined Scatter-Gather in Go" and
"concurrency pattern" — neither phrase appears in user vocabulary. The first
five lines of the README should be rewritten using *pool / task / submit /
results / recursive*. The phrase "pipelined scatter-gather" can keep a
section heading further down, framed as "what we call this pattern."

---

## Addendum: ultrapool — pure-speed competitor + reusable cross-library benchmark suite (2026-06-12)

Single-repo review of [maurice2k/ultrapool](https://github.com/maurice2k/ultrapool)
(README, repo metadata, benchmark source), added 2026-06-12. Not part of the
original 2026-05-23 corpus sweep, and a different kind of evidence: this is
a competitor analysis, not user-complaint mining. All performance numbers
below are the vendor's own claims, not independently verified.

### What it is

A generic, sharded, adaptive worker pool; single-file, dependency-free,
Go 1.20+, MIT. The README leads with "the fastest worker pool in Go" and
sells almost entirely via benchmark tables. API shape is handler-per-pool,
like ants' `PoolWithFunc` but generic: `NewWorkerPool(func(T))`, then
`AddTask(T)` / `AddTaskWithBlocking(T)`. Architecture: ~GOMAXPROCS/2 shards
each owning a task channel and worker set; lock-free CAS worker spawning
against per-shard and global caps; idle workers retire after 1s above a
floor. Claims 120–406 ns/op dispatch and 2–12× throughput vs `ants`,
`pond`, `gammazero/workerpool`, and fasthttp's workerpool — and beating
*raw goroutines* — across 8 workloads on 4 architectures (M2 Max,
Graviton4 96c, EPYC 48c, Xeon 96c).

Adoption signal: created 2020-01, recent v2 redesign, last push 2026-05-12 —
actively developed, negligibly adopted (11 stars, 0 forks, 0 open issues).

### Positioning relevance

1. **The pure-speed axis is occupied and doesn't appear to sell.** A
   library whose entire identity is "fastest worker pool" sits at 11 stars
   after six years. Directional only — that could reflect zero marketing
   rather than a market verdict — but it corroborates the recommendation
   to lead with PP1/PP4 rather than dispatch ns/op. Speed is table stakes
   to claim, not a headline to win on.
2. **Reinforces the headline gap.** Even this 2026-era, generics-native
   entrant takes `func(T)` with no return value — strictly fire-and-forget,
   no results story at all (PP1 untouched). No recursive-submission story
   either; a blocking `AddTaskWithBlocking` from inside a saturated worker
   presumably has the same PP4 deadlock shape as every bounded pool
   (unverified — its own benchmark harness would be a convenient place to
   test that). The gaps psg targets remain open in the newest competition.
3. **Vocabulary check passes.** "Worker pool", "task", `AddTask` —
   consistent with the Appendix B inventory. Nothing here updates the
   vocabulary recommendations.
4. **Now covered in ARCHITECTURE_COMPARISON.md** (section 6, added
   2026-06-12, source-level read of the whole 498-line library). Sharded
   dispatch + CAS spawn-on-demand + idle retirement overlaps psg's
   demand-driven spawning and idle-exit; rated Low-Medium contention,
   near-zero framework allocations.

### The benchmark suite (the actionable part)

`benchmark/` in the ultrapool repo: `workerpool_test.go` (~23 kB),
`crossbench*.sh` drivers, raw results. Workloads: Sleep 1µs, Sleep 50ms,
SHA256 1kB, AES-CBC 1kB/8kB, CRC32 64B, MemScan 4kB, mixed bimodal
(80% CRC32 / 20% AES), mutex contention. Harness: steady-state
`b.RunParallel` at parallelism 1/10/50/100 measuring dispatch throughput,
plus burst benchmarks (submit 50/500/5000 tasks, wait, let workers idle
out, repeat) isolating cold-start spawn behavior, with a 1 ms sampler
tracking peak goroutine/worker counts. Adapters already exist for ants,
pond, gammazero, fasthttp, tunny, and raw goroutines.

Why adapt psg to it:

- ARCHITECTURE_COMPARISON.md says its ratings "should be backed by an
  actual cross-library benchmark suite" before the README comparison table
  is published. This is a ready-made one; writing a psg adapter (no-result
  Launcher tasks) is cheap relative to building a suite from scratch.
- The fire-and-forget workloads measure exactly the overhead psg pays for
  machinery these tasks don't use (results plumbing, waves, limiters).
  That is the configuration where psg should look *worst*, which makes it
  the honest adversarial check on any "comparable speed" claim.
- The burst benchmarks and peak-worker sampling speak directly to psg's
  demand-driven spawning and scale-down-to-zero behavior.
- Known gap: the suite measures throughput only — no latency percentiles.
  Our priority order is P99/max latency first, throughput second, so psg
  runs would extend the harness with latency measurement rather than adopt
  its metrics wholesale.

Caveats: vendor-authored benchmarks in the vendor's repo; competing
libraries were adapted by the vendor and may not be optimally configured;
the "beats raw goroutines" claim is extraordinary and should be reproduced
locally before being repeated anywhere.

---

## Competitive landscape sweep (2026-06-12)

Finding ultrapool by accident prompted the question: what else did the
2026-05-23 research miss? This section is a systematic competitor sweep,
distinct in kind from the original research: that was user-complaint
mining; this is a library census.

### Methodology

Three parallel research agents, each searching a different way:
curated lists + GitHub topic/keyword search (sorted by stars and by
recency, including `created:>2023`); community discussions (HN via
Algolia, blog/news coverage, search-engine snippets of Reddit — Reddit
itself and some APIs remained blocked, the same gap as the May pass);
and package indexes + stdlib/x-repo proposals (pkg.go.dev importer
counts, golang/go issue tracker). Every reported library was verified by
fetching its repo and README — nothing reported from model memory alone.
Star counts and activity as observed 2026-06-12; treat rankings as
directional.

### Headline corrections to the earlier research

1. **destel/rill was the big miss.** ~1.8k stars, actively maintained,
   strong HN momentum (Show HN Nov 2024). "Go toolkit for clean,
   composable, channel-based concurrency" — functions take channels in
   and return channels out, every stage takes a concurrency parameter,
   backpressure is a headline feature, and results stream by
   construction. It owns the "streamed results + backpressure" narrative
   this document recommended psg lead with. Its gaps are exactly psg's
   territory: topology is fixed at pipeline-build time (no dynamic or
   recursive task submission), no rate limiting, and HN commenters
   flagged weak context integration and early-termination races. Note
   the adoption asymmetry: 1.8k stars but only ~16 pkg.go.dev importers —
   mindshare, not yet production share. **"Why not just use rill?" will
   be the reflexive question; the README needs a ready answer.**
2. **"Pools are fire-and-forget" is no longer a safe claim.** pond v2
   (2.2k stars, v2.7.1 Apr 2026, the most actively shipping pool) has
   typed `NewResultPool[T]` with `Submit(...).Wait() (T, error)` and
   ordered result slices for task groups. goptics/varmq (187 stars,
   active) delivers results via per-job handles. PP1 is being addressed
   by the market — as *futures/handles per job*. psg's claim must
   sharpen from "tasks return values" to **results streamed to handlers
   as they arrive** (plus recursion-safety and limiting), which remains
   unclaimed.
3. **The stdlib is absorbing the low end.** Go 1.25 shipped
   `sync.WaitGroup.Go(f)` (spawn-and-wait needs no library now);
   proposal golang/go#57534 to promote errgroup into stdlib `sync` is
   open (citing ~10k importers); errgroup's panic-propagation change was
   added (Apr 2025) and then **reverted** (v0.16.0, Jun 2025) — the Go
   team is iterating cautiously and errgroup's surface is effectively
   frozen at error-only, no values, no recursion story. Don't compete on
   spawn ergonomics; the stdlib won that.
4. **Go 1.23 iterators are the new result-streaming substrate.** Two
   agents independently converged on this: rill added range-over-func
   integration, samber/lo grew a lazy-iterator subpackage, and the most
   modern small entrant (firetiger-oss/concurrent) returns results as
   `iter.Seq`/`iter.Seq2`. **psg's result-handler API should at minimum
   have an `iter.Seq` interop story** — this is the most likely direction
   from which psg gets leapfrogged.

### Competitors that matter, ranked by relevance

**Direct positioning competitors (result-returning / streaming):**

- **destel/rill** (~1.8k★, active) — see above. The one to study first.
- **earthboundkid/flowmatic** (~400★, dormant since 2023; author Carl
  Johnson) — "Structured concurrency made easy." Its `ManageTasks` is
  the only published API found that *markets* the "tasks that spawn more
  tasks" pattern: a serial manager examines each task's output and may
  return new work. Prior art for psg's recursive-submission story —
  expect "how is this different from flowmatic?" The differences: psg
  has no serial-manager bottleneck, streams results, and has per-op
  limiting; flowmatic is also effectively abandoned.
- **creachadair/taskgroup** (~36★, small but actively maintained) —
  `Gatherer` delivers values to a serialized callback: conceptually the
  nearest neighbor to psg's result-handler model. Its `Limit` returns a
  start-func that blocks the submitter at the limit — **exactly the
  recursive-submission deadlock psg avoids; crisp demo material.**
- **alitto/pond v2** (2.2k★, very active) — typed result pools; see
  correction #2.
- **samber/lo `lop.Map`** (21.3k★ for lo) — by adoption, probably what
  most developers actually use for "parallel map." One goroutine per
  element, no limits, no errors, no streaming — a ready-made foil.
- **chebyrash/promise** (~413★, active) — generic futures with
  `.Await(ctx)`; per-stage pool selection (conc/ants integrations) is a
  crude cousin of per-op limiting.
- **reugn/go-streams** (~2.2k★, maintained) and **vladimirvivien/automi**
  (848★, revived 2025) — fixed-topology stream-processing DSLs with
  connectors (Kafka etc.); compete for "transform a stream concurrently"
  but pull toward ETL, not in-process dynamic work.
- **samber/ro** (new, Oct 2025) — RxJS-style observables; skeptical HN
  reception ("heaps of reflection and panics"), but samber's
  distribution reach (lo) means it could spread. The community's allergy
  to the Rx paradigm is an opening: "streamed results without the
  paradigm tax."
- **go-pkgz/pool** (25★, active; umputun) — feature-for-feature the
  closest *pool* to psg: typed results, streaming `Iter()` consumption,
  bounded-buffer backpressure, cross-pool pipelines. Tiny adoption; a
  design comparison point, not a market threat.

**Adjacent-category competitors:**

- **failsafe-go** (2.1k★, very active) — "Adaptive Limiter, Bulkhead,
  Rate Limiter…" — increasingly *the* answer when a Go developer wants
  per-operation limiting. psg's limiter story competes against
  "errgroup + failsafe-go composition," not just against pools.
- **noneback/go-taskflow** (632★, active) — taskflow-cpp-style DAGs with
  visualization; competes for dependency-graph workloads, not streaming;
  tasks communicate by side effect.
- **hibiken/asynq** (13.4k★) — distributed Redis-backed task queue;
  different category, but absorbs some "concurrent task processing"
  searches. psg is the in-process, value-returning counterpoint.
- **fatih/semgroup** (321★) — errgroup + semaphore, all-errors
  accumulation; represents the "errgroup + semaphore is all you need"
  school. Same recursive-submit deadlock as taskgroup's Limit.
- **negrel/conc** (53★, new, unrelated to sourcegraph/conc) —
  nursery-style structured concurrency where nested spawning is explicit
  (pass the nursery down); overlaps the recursion story, error-only.

**Watchlist (small, but show where the space is heading):**
firetiger-oss/concurrent (9★ — `iter.Seq` results, **concurrency limits
carried in the context that propagate down the call tree, only ever
decreasing** — the only interesting alternative to per-op limiting
found); goptics/varmq (187★ — result workers + pluggable persistence);
Yiming1997/go-agile-pool (166★, active ants-style pool, fire-and-forget);
kolosys/ion (1★ — bundles pool + multi-tier rate limiting + circuit
breakers, claims outrun maturity).

**Dead but instructive:**

- **go-playground/pool** (724★, archived, last release 2016) — prior art
  for psg's *exact* model: work units return `(interface{}, error)` and
  `batch.Results()` **streams completed units over a channel**. Worth
  citing as the pre-generics ancestor; it died with the type system,
  not the idea.
- **ReactiveX/RxGo** (5.1k★, dormant since 2021, pre-generics) — *the*
  result-streaming library of its era; its collapse left the gap rill
  now targets. Lesson (with go-playground/pool): streaming-results
  libraries die in Go when they fight the type system — lead with type
  safety.
- devchat-ai/gopool (214★, dormant 2023 — result callbacks),
  aaronjan/hunch, vardius/gollback, autom8ter/machine, workanator/go-floc
  — the long tail of "errgroup with results" attempts, all abandoned.
  The repeated abandonment is itself evidence the need is real and
  unmet.

### Updated strategic signals (supplements the 2026-05-23 list)

- **conc's limbo confirmed deeper**: still at v0.3.0 since January 2023,
  never 1.0, no successor found. The opening is still open.
- **errgroup's footguns are getting publicity** (HN Aug 2025, "A subtle
  bug with Go's errgroup," with bcmills defending the design). People
  keep using it while increasingly calling it trap-laden.
- **Community taste check**: the rill and ro HN threads split on clever
  channel/stream abstractions; revealed preference is for *boring,
  context-aware, type-safe* APIs. psg's context-first, handler-based
  surface aligns; any README cleverness does not.
- **Net competitive assessment** (all three agents converged): **no
  actively maintained library combines psg's four differentiators** —
  typed results streamed to handlers, deadlock-safe recursive
  submission, backpressure, per-op concurrency/rate limiting. Each is
  individually claimed (rill: streaming+backpressure; flowmatic/negrel:
  recursion; pond v2/varmq: typed results; failsafe-go: limiting); the
  conjunction is not.

### Action items emerging

1. Write the "why not rill?" comparison before the README rewrite —
   it's the first question any informed reader will ask.
2. Sharpen the PP1 claim to *streamed handlers*, not *result-returning*
   (pond v2 closed the naive version).
3. Build the recursive-deadlock demo against taskgroup `Limit` /
   semgroup / a bounded pool — concrete, reproducible, vivid.
4. Evaluate an `iter.Seq`/`iter.Seq2` interop surface for skim/result
   consumption (also filed in TODO.md).
5. Periodically re-check the watchlist + golang/go#57534.

---

## Appendix A: Pain points with quotes

### PP1. "How do I get the results out?"

Worker pools take `func()` instead of `func() (T, error)`; users open issues
asking how to return values, wait for completion, or collect outputs.

> "when i use `_ = p.Invoke(task)`, I want to do something while the task is being completed, how do it?"
> — iGen1us, [panjf2000/ants#230](https://github.com/panjf2000/ants/issues/230)

> "Why not provide a task wrapper for waiting and retrieving task results"
> — zundaren, [panjf2000/ants#280](https://github.com/panjf2000/ants/issues/280) (translated title)

> "I want to run multiple requests and get one response (struct or something) that all done."
> — JennyMet, [gammazero/workerpool#34](https://github.com/gammazero/workerpool/issues/34)

> "ResultPool is great for running tasks concurrently and collecting the
> results, but it doesn't necessarily maintain the order of the functions
> calls. Stream entity allows for processing an ordered stream of tasks in
> parallel but does not collect the results."
> — nolotz, [sourcegraph/conc#115](https://github.com/sourcegraph/conc/issues/115)

**Targets**: ants, gammazero, errgroup acutely; conc partially addresses but
gaps remain. **Type**: missing feature / API rough edge.

### PP2. errgroup forces shared variables with mutexes

errgroup-specific, structurally identical to PP1, but errgroup's stdlib
status amplifies it.

> "This is usually why I end up using https://godoc.org/golang.org/x/sync/errgroup instead of straight go statements... it addresses some of the cancellation and error propogation issues"
> — lclarkmichalek, [HN 16922150](https://news.ycombinator.com/item?id=16922150)

> "ErrGroup is nice, but it was created before contexts existed, and doesn't have support for cancellation"
> — atombender, same thread.

Repeated phrasing from third-party SO summaries: *"Since errgroup requires
goroutines to return only `error`, if you need to return values from
goroutines, you must use shared variables (with proper synchronization)."*

**Target**: errgroup specifically. **Type**: API limitation.

### PP3. Panics in goroutines crash the whole process

Every conc commentary references this; ants and pond also tout panic
recovery as a headline feature.

> "A frequent problem with goroutines in long-running applications is handling panics... A goroutine spawned without a panic handler will crash the whole process."
> — camdencheek (conc author), [HN 34344514](https://news.ycombinator.com/item?id=34344514)

> "A goroutine created inside an http request handler which then panics, by default will crash the whole server... You can't prevent it from panicking."
> — alexeldeib, same thread.

> "errgroup doesn't recover from panics to prevent them from killing the process"
> — erik_seaberg, [HN 16922150](https://news.ycombinator.com/item?id=16922150)

> "it is difficult to write concurrent code that operates reasonably in the face of panics"
> — Sourcegraph blog, https://sourcegraph.com/blog/building-conc-better-structured-concurrency-for-go

> "Once that condition is met, if any of the goroutines in the group terminated with an unrecovered `panic`, `Wait` should panic with a value wrapping the first panic-value recovered from a goroutine in the group."
> — bcmills, [golang/go#53757](https://github.com/golang/go/issues/53757)

**Targets**: errgroup, raw `go`, gammazero. ants/pond/conc each market
themselves as the fix. **Type**: surprising behavior + missing feature.

### PP4. Deadlocks when tasks submit more tasks (recursive spawn)

Most psg-relevant. Appears less often per repo than PP1 but where it
appears, the description is unusually crisp. Small audience, high pain
intensity.

> "Imagine a recursively implemented solution where by walking a tree, you discover that a leaf could spawn N subtrees (=goroutines). This example deadlocks, however, since the work pool crunches on incoming functions in order (range) using a buffered channel which blocks after receiving 2 tasks, but the tasks themselves are blocking since they wait for a result that will be made available much further down the recursion queue."
> — Artjom Simon, [golang-nuts thread](https://groups.google.com/g/golang-nuts/c/dN-0SlXtaZA)

> "none of them receive anything until the n - 1 and n - 2 workers return, so the worker pool has to be able to create the entire Fibonacci recursive tree at once."
> — Emily Maier, same thread.

> "the tasks are submitted to a group in bulk (using default queue size) and the tasks have not been processed when the context is cancelled... actually happened in production for us with v2.3.4."
> — Vyom-Yadav, [alitto/pond#138](https://github.com/alitto/pond/issues/138)

Errgroup-specific pattern from search summaries: *"A deadlock can occur when
a goroutine calls Go() to submit work while another goroutine is
simultaneously calling Wait() on the same errgroup."* (referencing
neilotoole/errgroup#6)

**Targets**: all pool libraries — every bounded pool has this failure mode.
**Type**: surprising behavior under recursion.

### PP5. Goroutine leaks

Distinct from PP3; users run goleak and discover the pool itself leaks.

> "I use this package in my application and I observe that the memory consumption is increased over time."
> — lidortal, [gammazero/workerpool#50](https://github.com/gammazero/workerpool/issues/50)

> "making it harder to leak goroutines"
> — camdencheek (one of conc's three stated goals), [HN 34344514](https://news.ycombinator.com/item?id=34344514)

> "I need graceful shutdown of my app with ants.PoolWithFunc so i need firstly forbid new tasks, and second wait for completion of already running. How can i do that with ants?"
> — vtolstov, [panjf2000/ants#73](https://github.com/panjf2000/ants/issues/73)

**Targets**: all of them; conc explicitly markets against it. **Type**:
surprising behavior.

### PP6. Type-unsafe APIs (legacy `interface{}`)

> "pf only have an ingerface{}, but i want to pass at least two args, how to solve tihes?"
> "context is not suggested to put into a struct, how to pass ctx + args as one task to queue?"
> — kungf, [panjf2000/ants#81](https://github.com/panjf2000/ants/issues/81)

**Target**: ants and pre-generics code generally. **Type**: confusing API.
Lower frequency post-generics; do not anchor on it.

### PP7. Confusing defaults and surprising behavior

> "If we do not set any concurrency, Stream will do its work sequentially."
> — OP, [sourcegraph/conc#153](https://github.com/sourcegraph/conc/issues/153)

> "After upgrading from v0.3.0 to `5f936ab`, we have observed performance degradation (significant increase in CPU)."
> — adriantam, [sourcegraph/conc#154](https://github.com/sourcegraph/conc/issues/154)

> "ctrl+C is not exiting the program"
> — serbrech, [sourcegraph/conc#78](https://github.com/sourcegraph/conc/issues/78) (context cancellation doesn't propagate if all tasks done)

> "When the Result Pool is configured WithErrors results after fir error are clipped."
> — OP, [sourcegraph/conc#156](https://github.com/sourcegraph/conc/issues/156)

**Target**: conc specifically. **Type**: surprising behavior.

---

## Appendix B: Vocabulary inventory (verbatim user phrases)

### Naming the abstraction
- **"goroutine pool"** — dominant in ants README, neilotoole/errgroup, HN.
- **"worker pool"** — gammazero, pond, Go-by-Example. Roughly equal frequency overall.
- **"pool"** (bare) — conc, pond, errgroup. The conversational form.
- "task runner" / "concurrent executor" — **rare** (expected, didn't appear).
- "structured concurrency" — library-author vocabulary; appears in conc marketing more than user complaints.

### Naming the units of work
- **"task"** — universal.
- "job" — common in tutorials (Go-by-Example uses `jobs`/`results` channels), less common in libraries.
- "work" / "unit of work" — appears in blog posts.
- **"submit"** — universal verb.
- "function call" — used by nolotz, conc#115.

### Naming results / outputs
- **"results"** — most common (`NewWithResults`, `results channel`, `collect the results`, `get the results of my tasks`).
- "collect results" — recurring in blog summaries.
- "return value" — Go forum threads and ants issues.
- "stream" — strongly tied to `conc.stream`; "ordered parallel processing without collecting all results in memory."
- "fan-in" — used in blog posts about combining channels; **rarely** in user issues. More theoretical than felt.

### Naming the trouble of recursive/child work
- **"recursively"** / **"recursive tree"** / **"subtrees"** (Artjom Simon, golang-nuts) — exact match for psg's territory.
- "nested goroutines" — present in search queries, rarer in issues.
- "submit while running" / "submit from inside a task" — errgroup deadlock discussions.
- "child tasks" — **rare**. Canonical phrasing is "tasks that spawn more tasks" or "recursive."

### Naming the bounded-concurrency goal
- **"limit the number of goroutines"** — universal (ants README, errgroup SetLimit proposal, blog posts).
- "bounded concurrency" — gitconnected article title; less common in user complaints.
- "rate limit" / "rate-limited APIs" — neilotoole/errgroup, pond README.
- **"backpressure"** — virtually absent. Surprising null.
- "cap concurrency" — gitconnected.

### Naming the failure modes
- **"deadlock"** — universal.
- **"goroutine leak"** — universal.
- "crash the whole process" / "crash the whole server" (alexeldeib) — dominant phrasing for panic problems.
- "panic recovery" / "handle panics gracefully" — library-side framing.

### CPU vs I/O split — null finding

**Important.** No user-voiced complaints framed as "different work types
need different pools." Vocabulary talks about "limit goroutines" and
"rate-limited APIs" but never names the dual-pool need explicitly. This
vocabulary appears to be the library author's, not the audience's.

---

## Appendix C: Top search queries / question titles

These are the actual question titles and recurring phrasings — the things a
tagline and name must rank against:

1. "How to get the results of my tasks" — ants#230 title
2. "How to wait for completion" — ants#73 OP
3. "How to pass context and other args in pool_func" — ants#81
4. "Get response from all worker pulls [sic]" — gammazero#34
5. "go errgroup return values" / "errgroup with results" — canonical SO search
6. "limit the number of goroutines running" — SetLimit proposal phrasing, repeated everywhere
7. "go worker pool" — Go-by-Example is the #1 tutorial result
8. "go fan-out fan-in" — heavy blog presence, light user-complaint presence
9. "goroutine leak" + "worker pool"
10. "limiting count of goroutines (work pool) when goroutines are created recursively" — golang-nuts thread title; direct psg territory
11. "send on closed channel" + "pool" — conc#103
12. "context cancel propagation" + "pool" — conc#78

---

## Appendix D: Surprising findings

1. **"Fan-out/fan-in" is academic vocabulary, not user vocabulary.** Tons of
   blog posts use it; almost no frustrated user in an issue uses it to
   describe their problem.
2. **"Backpressure" is essentially absent** from the user-facing corpus.
3. **"Structured concurrency" is library-author vocabulary**, not user
   vocabulary.
4. **CPU vs I/O pool separation is not a complaint people voice.**
5. **The recursive-spawn deadlock is more vivid than frequent.** Small
   audience, high pain intensity. Good for positioning, bad for raw search
   volume.
6. **Type-unsafe APIs are a legacy complaint** post-generics; don't anchor
   on it.
7. **conc maintenance is itself a complaint** (issue #148).
8. **Users cannot tell ants/pond/conc apart** (pond#123, open and
   unanswered). Positioning must be unmistakable in the first ten seconds
   of README reading.

---

## Appendix E: Sources consulted

**GitHub issues:**
- sourcegraph/conc: #29, #36, #78, #86, #103, #115, #141, #145, #148, #153, #154, #156
- panjf2000/ants: #73, #76, #81, #168, #230, #280, #363
- alitto/pond: #116, #123, #138
- gammazero/workerpool: #34, #50
- golang/go: #27837, #53757
- neilotoole/errgroup: README

**Hacker News:** items 34344514, 16922150, 24359650

**Other:**
- golang-nuts thread `dN-0SlXtaZA`
- Sourcegraph blog: building-conc-better-structured-concurrency-for-go
- levelup.gitconnected: bounded-concurrency-in-go article (could not fetch full body — Medium auth redirect)

**Not accessible:**
- Reddit `/r/golang`
- Stack Overflow (direct access)
