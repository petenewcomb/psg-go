---
name: sim-trace-debugging
description: Diagnose an intermittent hang, livelock, deadlock, or "never-Done" failure in the psg-go scatter-gather framework's property-based concurrency simulation (TestBySimulation / internal/sim). Use when TestBySimulation hangs or times out (with or without -race), when a Pool/Wave/Funnel/Skimmer/Limiter deadlocks or spins, or when you need to capture and read an execution trace of the sim with the internal/sim + internal/cmd/fmttrace toolkit.
---

# Debugging psg-go simulation hangs with execution traces

`TestBySimulation` (root package, `simulation_test.go`) runs `rapid`-generated plans
through `internal/sim`, exercising Pools/Waves/Funnels/Skimmers/Limiters and nested
subjobs concurrently. Concurrency bugs surface as **intermittent** hangs/livelocks —
Heisenbugs.

The method, in order. Do the cheap, high-leverage steps *first*:

1. **Classify** the failure from the goroutine dump (picks the diagnosis technique).
2. **Bias and bisect the sim config to a minimal repro** — *before* capturing any trace.
   This is the highest-leverage step (see §2): biasing the sim makes a rare Heisenbug
   *frequent*, and bisecting the plan makes it *small* — which makes the eventual trace
   short and the analysis tractable.
3. **Capture a trace** of that minimized repro.
4. **Post-process the trace**; if it doesn't yet reveal the bug, **add trace logging** and
   re-capture. Iterate.

Favor post-processing data you collect in the trace over building diagnostic machinery
(watchdogs, analyzers, parallel stderr tracers) in the code. The one exception is
**panics that assert invalid state** — those are cheap, they convert a silent wedge into
a loud, located failure, and they should generally be left in the code permanently.

## Toolset

- **`internal/sim` + `TestBySimulation`** — the simulation. `planConfig` (a
  `sim.Config`) parameterizes plan generation: op counts, path length/count, subjob
  depth, per-op `SelfTime` delays, error/scatter probabilities. These knobs are how you
  bias and shrink (see §2). `internal/sim/run.go` (`ensurePools`) wires the limiters.
- **`rapid`** — the property-based generator driving plan generation. Note: rapid
  **shrinking does not work** for non-deterministic concurrency bugs (a seed that hangs
  one run passes the next), so don't rely on seeds/shrinking — you bisect `planConfig`
  by hand instead. Always `-count=1` (and `-rapid.checks=N`) to defeat go-test caching.
- **Tracing** — `internal/trace` wraps `runtime/trace`. Enable internal trace logging
  with env `PSGTRACEINTERNALS` set (value is a prefix filter; empty matches all, `=+`
  strips the leading `+`); capture the runtime trace with `go test -trace=…`. A
  `-timeout` hang flushes enough trace to read the lead-up. Existing `trace.Logf` points
  stamp lifecycle events with object pointers (`funnelEngine=%p`, `worker=%p`,
  `WaveState=%p`, channel pointers, limiter `lim=%p`) — these are what make a trace
  *correlatable* (§4).
- **`internal/cmd/fmttrace`** — its own module (uses `golang.org/x/exp/trace`); renders a
  raw trace to text with goroutine IDs, timing, regions, and log messages. Helpers in
  that dir: `find-goroutines.sh '/<sed-pattern>/' < text` (gids whose events match) and
  `extract-goroutine.sh <gid> < text` (one goroutine's events).
  - Caveat: the higher-level `analyze-sim-trace.sh` / `extract-sim-*.sh` op-diff scripts
    grep markers that drifted in the rename (`sim.Run: Test plan:`, `step M/M: done`) and
    may not run as-is. The manual pipeline below and the §4 techniques don't need them.

## 1. Classify from the goroutine dump

A plain `-timeout` panic dumps all goroutines. Read the signature first — it selects the
technique:

- **`sync.Mutex` / `semacquire` waiters present** → a blocking **deadlock**; find the
  lock/permit cycle (who holds what while waiting for what).
- **Zero mutex waiters, goroutines `runnable`/spinning** → a **busy-spin livelock**
  (CPU-bound); confirm with a counter that climbs in the trace, and look for a
  hold-and-wait on a resource (a permit held by a parked holder while another spins).
- **All workers exited, only `SkimAll` parked on `j.state.Done()`** → the job never
  reached Done = a **leaked/stuck reference** (a work or funnel-instance barrier was
  taken but never released — either the release logic never ran, or a driver goroutine
  that should run it is parked having missed its wake signal).

The dump is the *final state*. Only the **trace** shows the *interleaving* that produced
it — but get to a small, frequent repro before you capture one.

## 2. Bias and bisect the sim to a minimal repro (do this BEFORE tracing)

Since rapid shrinking is useless here, you reduce variability by hand — and you get two
wins at once: **bias** the config so the bug reproduces *frequently*, and **bisect** it so
the reproducing plan is *small*. Heisenbugs often become dramatically more frequent and
much simpler under the right bias, and a simple plan yields a short, readable trace.

Work in `TestBySimulation` (temporary edits — **REVERT before finishing**), looping
`go test` to *measure the hit rate* so you can tell whether a change helped:

```bash
for i in $(seq 1 300); do
  go test -run TestBySimulation -count=1 -rapid.checks=1 -timeout 20s . >/tmp/h_$i.log 2>&1
  grep -q "test timed out" /tmp/h_$i.log && echo "HANG inv $i"
done
```

Quote the rate (e.g. "≈3/300" vs "0/300") to compare configs quantitatively.

The knobs, and what each one buys you:

- `planConfig.<Launcher.Body|Funnel.Accumulate|Funnel.Flush|Skimmer.Handle>.SelfTime =
  sim.BiasedDurationConfig{}` — **zero delays**. The biggest lever: cases run fast *and*
  concurrency windows tighten, so races fire far more often.
- `planConfig.Subjob.MaxDepth` — recursion depth. Many bugs need subjobs (set 0 to test
  whether recursion is required).
- `planConfig.{Path,Funnel,Skimmer}.Count`, `Path.Length`, `ScatterCount` — scale and
  shape. Shrink toward the smallest counts that still reproduce.
- Unlimited limiters (`internal/sim/run.go` `ensurePools`: `NewSemaphore(-1)`) — tests
  "is a limiter the gate?".
- Error/scatter probabilities → 0 — remove orthogonal variation.

Bisect like delta-debugging: starting from a config that reproduces, turn off or shrink
**one dimension at a time** and re-measure.

- If the bug **persists**, keep that dimension off/smaller — the repro just got simpler.
- If the bug **vanishes**, that dimension is part of the trigger — turn it back on and
  record it. This doubles as a **discriminator**: "needs funnels", "needs subjobs", "not
  the limiter" each narrow the cause *before* you read a single trace event.

Converge on the minimal set of enabled features and smallest counts that still hangs
frequently. That config is the thing you trace.

## 3. Capture a trace (of the minimized repro)

```bash
for i in $(seq 1 300); do
  PSGTRACEINTERNALS= go test -run TestBySimulation -trace=/tmp/trace.out -rapid.checks=1 -timeout 25s . >/tmp/c_$i.log 2>&1
  grep -q "test timed out" /tmp/c_$i.log && { mv /tmp/trace.out /tmp/trace_hang.out; echo "trace inv $i"; break; }
done
```

A minimized repro keeps this trace small (un-minimized zero-delay cases can run to ~1 GB;
fmttrace streams them, but a small one is the difference between seconds and minutes of
analysis). `-rapid.checks=1` keeps it to a single top-level case. Don't fear the trace
overhead masking the bug — capture and see; if a particular bug only reproduces untraced,
that itself is a clue, but usually tracing reproduces fine.

Render to text:

```bash
go run -C <psg>/internal/cmd/fmttrace ./... < /tmp/trace_hang.out > /tmp/trace.txt
# huge traces: stream through zstd, e.g.
#   zstd -dc trace.out.zst | go run -C <psg>/internal/cmd/fmttrace ./... | zstd -T0 -3 > trace.txt.zst
```

## 4. Post-process the trace

General techniques, roughly in order of leverage. They lean on the pointer-stamped
lifecycle logs already in the code — prefer extracting from the trace over adding code:

- **Op started-vs-completed diff** — find *which op* wedged: collect op-start lines and
  op-done lines, `comm`/`sort -u` the difference. (The `extract-sim-*.sh` scripts mean to
  do this but have stale markers; a manual grep of the start/done log strings works.)
- **Single-goroutine-to-wedge** — a parked goroutine's **last event is the wedge**.
  `extract-goroutine.sh <gid> | tail`. If its final line is a `select` ("entering
  select: … =0x…"), it died parked there, and the printed channel pointers say exactly
  what it was (and wasn't) waiting on. Get the gid by grepping a per-object lifecycle log
  line and reading its `G=NNN`.
- **Pointer correlation** — `%p` stamps tie objects to goroutines to channels. Follow an
  object (a wave, a funnel engine, a limiter) across goroutines by its pointer to
  reconstruct who touched it.
- **Pair-tally** — count two paired lifecycle events globally (`grep -c`). An off-by-one
  between a "take" and its matching "release"/"signal" pinpoints that *exactly one*
  instance leaked, ruling out broad accounting bugs and pointing at a single race.
- **Held-resource proof** — for hold-and-wait, count `acquire-OK` vs `release` for a
  `lim=%p`: `acquire == release+1` means **held**; `find-goroutines.sh` on the acquire
  vs. the spin separates holder from spinner.
- **Timestamp-ordering** — every event carries an absolute ns stamp. Comparing the
  stamps of a racing pair (e.g. a state transition vs. the moment another goroutine
  subscribed/read shared state) can directly **prove a happens-before violation**.

## Adding instrumentation when the trace isn't enough

Add `trace.Logf(ctx, "<region>", "<fmt>", …)` at the points of interest (gate hot paths
on `trace.IsEnabled()`), re-capture, and iterate until the bug reveals itself. `fmttrace`
already renders these — don't build a parallel stderr tracer, an in-code watchdog, or a
bespoke analyzer. The exception, again: an **invalid-state `panic`** is a fine and durable
addition — it turns a silent wedge into a located failure and earns its keep in the code.

## Gotchas

- **rapid shrinking does not work** here (non-deterministic). A per-case watchdog that
  cancels ctx to force a shrinkable `t.Fatal` is also fragile — `sim.Run` may not unwind
  cleanly on cancel (`CancelAndWait` can itself block), and a per-case timer left running
  on the (mostly passing) cases leaks many sleeping goroutines that pollute the dump.
  Bisect the config (§2) + loop-until-hang + trace instead.
- **Stale op-diff script markers** (rename drift): `analyze-sim-trace.sh` /
  `extract-sim-*.sh` grep strings the sim no longer logs verbatim. Fix the scripts/markers
  or use the manual §4 techniques (which don't depend on them).
- **Traces are huge** unless you minimize first (§2) — another reason §2 precedes §3.
- Always **`-count=1`** to defeat go-test result caching.
- **Don't delete a captured hang trace** until the bug is fixed and verified.
