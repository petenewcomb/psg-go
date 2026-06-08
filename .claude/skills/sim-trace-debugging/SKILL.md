---
name: sim-trace-debugging
description: Diagnose an intermittent hang, livelock, deadlock, or "never-Done" failure in the psg-go scatter-gather framework's property-based concurrency simulation (TestBySimulation / internal/sim). Use when TestBySimulation hangs or times out (with or without -race), when a Pool/Wave/Funnel/Skimmer/Limiter deadlocks or spins, or when you need to capture and read an execution trace of the sim with the internal/sim + internal/cmd/fmttrace toolkit.
---

# Debugging psg-go simulation hangs with execution traces

`TestBySimulation` (psg root package, `simulation_test.go`) runs `rapid`-generated
plans through `internal/sim`, exercising Pools/Waves/Funnels/Skimmers/Limiters and
nested subjobs concurrently. Concurrency bugs show up as **intermittent** hangs.
This skill is the end-to-end method to root-cause them.

## 0. Mindset / what kind of bug is it?

Read the goroutine dump signature first (a plain `-timeout` panic dumps all goroutines):

- **`sync.Mutex` / `semacquire` waiters present** → a blocking deadlock; find the lock cycle.
- **Zero mutex waiters, goroutines `runnable`/spinning** → a **busy-spin livelock** (CPU-bound). Confirm via a counter that climbs in the trace (e.g. `nbcq.PushBack item=N`).
- **All workers exited, only `SkimAll` parked on `j.state.Done()`** → job never reached Done = a **leaked/stuck job reference** (work or funnel-instance barrier Init'd but never Closed/flushed).

Reproduce-then-trace beats staring at one dump: the dump is the *final state*; only the **trace** shows the *interleaving* that produced it.

## 1. Reproduce reliably (reduce variability — but know which knob matters)

`rapid` shrinking is **unreliable for non-deterministic concurrency bugs** (same plan
hangs one run, passes the next), so don't rely on seeds/shrinking. Instead reduce the
plan config in `simulation_test.go` to make the hang frequent, and loop `go test`.

Knobs (temporary edits in `TestBySimulation`, REVERT before finishing):
- `planConfig.<Launcher.Body|Funnel.Accumulate|Funnel.Flush|Skimmer.Handle>.SelfTime = sim.BiasedDurationConfig{}` — **zero delays**; biggest lever: makes cases fast AND tightens concurrency windows, surfacing races.
- `planConfig.Subjob.MaxDepth` — recursion; **many bugs require subjobs** (set 0 to test if recursion is needed).
- `planConfig.{Path,Funnel,Skimmer}.Count` — scale; some bugs need near-default scale, others reproduce small.

Beware **go-test result caching** — always use `-count=1` (or `-rapid.checks=N`).

Loop until a hang (each hang costs the `-timeout`):
```bash
for i in $(seq 1 300); do
  go test -run TestBySimulation -count=1 -rapid.checks=1 -timeout 20s . >/tmp/h_$i.log 2>&1
  grep -q "test timed out" /tmp/h_$i.log && { echo "HANG inv $i"; break; }
done
```
Useful discriminating experiments: make all limiters unlimited (`internal/sim/run.go`
`ensurePools`: `psg.NewSemaphore(-1)`) to test "is a limiter the gate?"; toggle
subjobs/funnels/delays to isolate the trigger.

## 2. Capture an execution trace

Enable internal tracing (`PSGTRACEINTERNALS` set, value is a prefix — empty is fine;
`=+` is equivalent, the leading `+` is stripped) AND `-trace`. A `-timeout` hang flushes
enough trace to use. Keep cases small (`-rapid.checks=1`) so the trace is one top-level
case (still large — hundreds of MB to GB; the tooling streams, that's OK):
```bash
for i in $(seq 1 300); do
  PSGTRACEINTERNALS= go test -run TestBySimulation -trace=/tmp/trace.out -rapid.checks=1 -timeout 25s . >/tmp/c_$i.log 2>&1
  grep -q "test timed out" /tmp/c_$i.log && { mv /tmp/trace.out /tmp/trace_hang.out; echo "trace inv $i"; break; }
done
```

## 3. Analyze with the toolkit

The formatter is `internal/cmd/fmttrace` (its own module; uses `golang.org/x/exp/trace`).
It prints events as text with goroutine IDs/timing/regions/log messages.

Full pipeline (creates plan/started/completed/incomplete in CWD):
```bash
mkdir -p /tmp/an && cd /tmp/an
<psg>/internal/sim/analyze-sim-trace.sh /tmp/trace_hang.out
```
`incomplete.txt` = ops that started (`step 1/M`) but never completed (`step M/M: done`) — the wedge.

Manual / when scripts misbehave (see gotchas):
```bash
SIM=<psg>/internal/sim
zstd -dc trace.out.zst | go run -C <psg>/internal/cmd/fmttrace ./... | zstd -T0 -3 > trace.txt.zst   # text (slow, ~minutes on GB)
zstd -dc trace.txt.zst | $SIM/extract-sim-started.sh   | sort -u > started.txt
zstd -dc trace.txt.zst | $SIM/extract-sim-completed.sh | sort -u > completed.txt
comm -23 started.txt completed.txt    # wedged ops (use plain sort for comm)
zstd -dc trace.txt.zst | tail -3000    # the tail is the interleaving right before the wedge
```

Drill into a goroutine (`<psg>/internal/cmd/fmttrace/`):
- `find-goroutines.sh '/<sed-pattern>/' < text` — goroutine IDs whose events match.
- `extract-goroutine.sh <gid> < text` — all events for one goroutine.

Read limiter/governor behavior directly: the existing `trace.Logf` points (Governor
downstream±/backpressure, `Pool.block`) plus any you add (e.g. limiter acquire/release
with the `lim=%p` pointer) reveal hold-and-wait. To prove a held resource: count
`acquire-OK` vs `release` for a `lim=%p` — `acquire-OK == release+1` means **held**;
`find-goroutines.sh` on the acquire vs the spin shows holder-vs-spinner.

## 4. Adding info to the trace

Use the **existing** facility (`internal/trace` → runtime/trace) — add `trace.Logf(ctx,
"<region>", "<fmt>", ...)` at the points of interest (gated by `trace.IsEnabled()` for
hot paths). Don't build a parallel stderr tracer; `fmttrace` already renders these. The
trace flushes enough on a `-timeout` hang to read the lead-up.

## Gotchas / known issues

- **Stale extract-script markers (combiner rename):** `extract-sim-trace.sh` greps
  `sim.Run: Test plan:` but the sim now logs the plan as `%v` (`Plan#N…`);
  `extract-sim-completed.sh` greps `step M/M: done` but the sim logs `… ends at`. Either
  fix the scripts or restore the markers in `internal/sim/run.go`. Until fixed, bypass
  with the manual pipeline above (with `-rapid.checks=1` there's one top-level case, so
  you don't need `extract-sim-trace.sh`'s "isolate the last plan" step).
- **rapid shrinking does not work** for these (non-deterministic). A per-case watchdog
  that cancels ctx to convert a hang into a `t.Fatal` for shrinking is also fragile —
  `sim.Run` may not unwind cleanly on cancel (`CancelAndWait` can itself block). Prefer
  loop-until-hang + trace.
- **Trace files are huge** (a single zero-delay case can be ~1 GB). That's expected; the
  tooling streams. `fmttrace` on 1 GB takes ~2 min. Don't delete a captured hang trace.
- Always `-count=1` to defeat go-test caching.

## Worked example (2026-06-07, the bug this skill came from)

Intermittent `TestBySimulation -race` hang → no mutex waiters → busy-spin in
`rdvq.Waiters.WaitFunc`/`block-and-help` (item counter ~920k). Trace showed a `limit=1`
limiter held (`acquire-OK == release+1`) while its holder was parked in a gather; same
-(sub)job work needing that permit spun. Confirmed by unlimited-limiter test (0/250 vs
~1/25). Root cause + fix (limiter suspend/resume around gathers) are written up at the
top of `WORKING_NOTES.md`.
