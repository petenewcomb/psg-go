// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/stretchr/testify/assert"
)

// Run executes the given Plan against the current psg API via an
// adapter that translates the new vocabulary's static structure to
// today's Pool/TaskPool/FunnelPool/Skimmer/Funnel shapes. Real
// data routing uses Submit/TrySubmit; the current API's funnel-
// output-type slot is satisfied by a singleton dummy struct{}
// Skimmer.
//
// v1 supports only the linear-chain plans the minimal generator
// produces (one Submit per body, no multi-sink, no probabilistic
// Steps beyond the basic ReturnErrorProb support, no Subjob).
// Enrichment lands in follow-up commits.
func Run(ctx context.Context, t assert.TestingT, plan *Plan) error {
	return run(ctx, t, plan, nil)
}

// run executes a Plan; parent is the enclosing controller when plan is a
// Subjob's nested Plan (enables cross-subjob limiter inheritance), nil at
// top level.
func run(ctx context.Context, t assert.TestingT, plan *Plan, parent *controller) error {
	traceRegion := "sim.Run"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", plan)

	ctx, wave := psg.NewWave(ctx)
	defer wave.CancelAndWait()

	c := newController(plan, wave, parent)
	if plan.CancelTriggerRunnerID >= 0 {
		// Plan-baked mid-flight cancellation: the designated launcher's body
		// (see newLauncher) calls c.cancel while holding its permit, with
		// siblings likely blocked acquiring the shared limiter.
		ctx, c.cancel = context.WithCancel(ctx)
	}
	return c.Run(ctx, t)
}

// newController builds the per-Plan runtime adapter state. parent is the
// enclosing controller for a Subjob's nested Plan, nil at top level.
func newController(plan *Plan, wave *psg.Wave, parent *controller) *controller {
	return &controller{
		Plan:                  plan,
		Wave:                  wave,
		parent:                parent,
		TaskLimiters:          make([]psg.Limiter, len(plan.TaskLimiters)),
		FunnelLimiters:        make([]psg.Limiter, len(plan.FunnelLimiters)),
		Skimmers:              make([]*psg.Skimmer[*simValue], len(plan.Skimmers)),
		Funnels:               make([]*psg.Funnel[*simValue], len(plan.Funnels)),
		taskLimiterTrackers:   make([]*limiterTracker, len(plan.TaskLimiters)),
		funnelLimiterTrackers: make([]*limiterTracker, len(plan.FunnelLimiters)),
		skimmerInvocations:    make([]atomic.Int64, len(plan.Skimmers)),
	}
}

// limiterTracker measures observed *active* concurrency for one Plan
// limiter: bodies enter() at start and exit() at end, and additionally
// exit()/enter() around driving a subwave — the span over which the
// framework suspends the body's permit (docs/limiter-suspend-resume.md,
// "Measuring concurrency under suspension"). Shared by pointer with
// subjob controllers when the underlying limiter is inherited, so the
// observed-max assertion covers the joint topology (split counters would
// each check a subset and could miss a joint violation).
type limiterTracker struct {
	cur atomic.Int64
	max atomicMaxInt64
}

func (lt *limiterTracker) enter() {
	lt.max.UpdateMax(lt.cur.Add(1))
}

func (lt *limiterTracker) exit() {
	lt.cur.Add(-1)
}

// simValue is the uniform value type that flows through all sim ops.
// Carries minimal metadata for assertion-checking.
type simValue struct {
	OriginRunnerID int
	DispatchTime   time.Time
}

// controller is the per-Plan runtime adapter state. Owns the psg API
// objects backing the Plan's static vocabulary.
type controller struct {
	Plan           *Plan
	Wave           *psg.Wave
	TaskLimiters   []psg.Limiter
	FunnelLimiters []psg.Limiter
	Skimmers       []*psg.Skimmer[*simValue]
	Funnels        []*psg.Funnel[*simValue]
	// Launchers holds one psg.TaskLauncher per Plan Launcher. The
	// closure inside each runs the runner's Body Func, which Submits
	// directly to downstream Skimmers/Funnels.
	Launchers []psg.TaskLauncher

	limitersOnce sync.Once

	// parent is the enclosing controller when this Plan runs as a
	// Subjob; nil at top level. Read-only after construction; used by
	// ensurePools to alias inherited limiters and trackers.
	parent *controller

	// cancel is non-nil only when this Plan is baked for mid-flight
	// cancellation (Plan.CancelTriggerRunnerID >= 0); the trigger
	// launcher's body invokes it. Idempotent (context.CancelFunc).
	cancel context.CancelFunc

	taskLimiterTrackers   []*limiterTracker
	funnelLimiterTrackers []*limiterTracker
	skimmerInvocations    []atomic.Int64
	StartTime             time.Time
}

func (c *controller) Run(ctx context.Context, t assert.TestingT) error {
	traceRegion := "sim.controller.Run"
	c.StartTime = time.Now()

	c.ensurePools()

	// Construct Skimmers and Funnels against the Pool. Order matters:
	// Funnels reference Skimmers (in body Submits), so Skimmers must
	// exist first.
	// Alternate explicit-wave (even idx) vs nil-wave (odd idx) to
	// exercise both the bound-wave path and the nil-sentinel
	// ctx-resolution path. Nil-wave ops resolve the dispatching wave
	// from the ctx at Submit time (top-level ctx, or the worker-
	// stamped ctx inside task / accumulate bodies).
	for i, g := range c.Plan.Skimmers {
		gp := g
		idx := i
		var w *psg.Wave
		if i%2 == 0 {
			w = c.Wave
		}
		skimmer := psg.NewSkimmer(w, c.newSkimmerHandler(t, gp, idx))
		c.Skimmers[i] = &skimmer
	}
	for i, cmb := range c.Plan.Funnels {
		cp := cmb
		idx := i
		var opts []psg.OpOption
		if len(cp.LimiterIndexes) > 0 {
			opts = append(opts, psg.WithLimits(c.FunnelLimiters[cp.LimiterIndexes[0]]))
		}
		funnel := psg.NewFunnel(c.Wave, c.newFunnelFactory(t, cp, idx), opts...)
		c.Funnels[i] = &funnel
	}
	// Construct Launchers after Funnels/Skimmers so the bodies can
	// reference them via Submit. Launcher Bodies may StartTask other
	// runners, but only after the entire array is populated (a runner's
	// Body never runs during construction).
	// Alternate explicit-wave (even idx) vs nil-wave (odd idx) for
	// Launchers too — same rationale as the Skimmer construction
	// above.
	c.Launchers = make([]psg.TaskLauncher, len(c.Plan.Launchers))
	for i, runner := range c.Plan.Launchers {
		c.Launchers[i] = c.newLauncher(t, runner, i%2 == 0)
	}

	// Execute top-level Steps.
	for i, step := range c.Plan.Steps {
		trace.Logf(ctx, traceRegion, "%v step %d/%d: %T", c.Plan, i+1, len(c.Plan.Steps)+1, step)
		c.executeStep(ctx, t, step)
	}

	// Drain.
	chk := assert.New(t)
	for {
		err := c.Wave.CloseAndSkimAll(ctx)
		if d := classify(err); d == dispRetry {
			// Skimmer.Handle returned an error as expected.
			continue
		} else if d == dispFail {
			chk.NoError(err)
		}
		break
	}

	// Per-Skimmer sink-invocation bounds.
	for i, want := range c.Plan.MinSkimmerInvocations {
		got := c.skimmerInvocations[i].Load()
		chk.GreaterOrEqualf(got, int64(want),
			"Plan#%d Skimmer#%d min invocations (got %d, want >=%d)",
			c.Plan.ID, c.Plan.Skimmers[i].ID, got, want)
	}
	for i, want := range c.Plan.MaxSkimmerInvocations {
		got := c.skimmerInvocations[i].Load()
		chk.LessOrEqualf(got, int64(want),
			"Plan#%d Skimmer#%d max invocations (got %d, want <=%d)",
			c.Plan.ID, c.Plan.Skimmers[i].ID, got, want)
	}
	// Per-Limiter aggregate concurrency: observed max must not exceed
	// configured permits. Inherited limiters are skipped — the tracker
	// is shared with (and asserted by) the owning ancestor plan, whose
	// run encloses this one.
	for i, lim := range c.Plan.TaskLimiters {
		if lim.InheritFromParent >= 0 {
			continue
		}
		observed := c.taskLimiterTrackers[i].max.Load()
		chk.LessOrEqualf(observed, int64(lim.Permits),
			"TaskLimiter#%d observed concurrency %d > permits %d", lim.ID, observed, lim.Permits)
	}
	for i, lim := range c.Plan.FunnelLimiters {
		if lim.InheritFromParent >= 0 {
			continue
		}
		observed := c.funnelLimiterTrackers[i].max.Load()
		chk.LessOrEqualf(observed, int64(lim.Permits),
			"FunnelLimiter#%d observed concurrency %d > permits %d", lim.ID, observed, lim.Permits)
	}
	// Path-duration lower bound: total elapsed wall-clock must be at
	// least MaxPathDuration. Only enforced in Deterministic mode where
	// SelfTime distributions are collapsed to their Med values; in
	// probabilistic mode the per-invocation draws can come in below
	// Med.
	if c.Plan != nil && (c.Plan.MaxPathDuration > 0) {
		elapsed := time.Since(c.StartTime)
		chk.GreaterOrEqualf(elapsed, c.Plan.MaxPathDuration,
			"elapsed %v < MaxPathDuration %v", elapsed, c.Plan.MaxPathDuration)
	}

	return nil
}

// ensurePools lazily constructs the psg.Limiter and psg.FunnelPool
// instances backing the Plan's Limiters. One Limiter per
// Plan.TaskLimiters and Plan.FunnelLimiters entry. A single
// FunnelPool hosts all Funnels — per-Funnel concurrency is
// enforced via the FunnelLimiters bound to each Funnel via
// psg.WithLimits.
func (c *controller) ensurePools() {
	c.limitersOnce.Do(func() {
		for i, lim := range c.Plan.TaskLimiters {
			if c.parent != nil && lim.InheritFromParent >= 0 {
				// Shared limiter across the subjob boundary: alias the
				// parent's psg.Limiter AND its tracker so permits and the
				// observed-max assertion both cover the joint topology.
				c.TaskLimiters[i] = c.parent.TaskLimiters[lim.InheritFromParent]
				c.taskLimiterTrackers[i] = c.parent.taskLimiterTrackers[lim.InheritFromParent]
				continue
			}
			c.TaskLimiters[i] = psg.NewSemaphore(nil, lim.Permits)
			c.taskLimiterTrackers[i] = &limiterTracker{}
		}
		for i, lim := range c.Plan.FunnelLimiters {
			if c.parent != nil && lim.InheritFromParent >= 0 {
				c.FunnelLimiters[i] = c.parent.FunnelLimiters[lim.InheritFromParent]
				c.funnelLimiterTrackers[i] = c.parent.funnelLimiterTrackers[lim.InheritFromParent]
				continue
			}
			c.FunnelLimiters[i] = psg.NewSemaphore(nil, lim.Permits)
			c.funnelLimiterTrackers[i] = &limiterTracker{}
		}
	})
	// The funnel engine is now an internal per-job detail behind NewFunnel(wave);
	// no FunnelPool to construct here anymore.
}

// executeStep dispatches a Step. Currently handles StartTask, Submit
// (synthesized at top level — uncommon), and Subjob (deferred to v2).
func (c *controller) executeStep(ctx context.Context, t assert.TestingT, step Step) {
	chk := assert.New(t)
	switch s := step.(type) {
	case StartTask:
		c.startTask(ctx, t, s.RunnerIndex)
	case Submit:
		c.submitFresh(ctx, t, s)
	case SelfTime:
		// Top-level SelfTime is unusual but allowed; just sleep.
		timer := timerp.Get()
		defer timerp.Put(timer)
		timerp.Reset(timer, c.drawDuration(s.Dist))
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	case Subjob:
		c.runSubjob(ctx, t, s)
	default:
		chk.Fail(fmt.Sprintf("unknown Step type %T", step))
	}
}

// runSubjob executes a Subjob step by spinning up a fresh psg.Wave and
// recursing into Run with the nested Plan. This exercises cross-Pool
// boundary code (a key race-coverage objective) and matches old sim
// semantics where Subjobs ran on their own Pool.
func (c *controller) runSubjob(ctx context.Context, t assert.TestingT, s Subjob) {
	if s.Plan == nil {
		return
	}
	if !c.rollProb(s.Prob) {
		return
	}
	err := run(ctx, t, s.Plan, c)
	if classify(err) == dispFail {
		assert.New(t).NoError(err)
	}
}

// newLauncher constructs the psg.TaskLauncher that backs a Plan
// Launcher. The task body walks the Plan's Body Func; Submits go
// directly to downstream sinks (Funnels/Skimmers) via Submit, and
// StartTask is skipped because the current API forbids dispatching new
// work from a task body.
func (c *controller) newLauncher(t assert.TestingT, runner *Launcher, bindWave bool) psg.TaskLauncher {
	// Concurrency tracking: bump the TaskLimiter tracker on entry to the
	// task body, decrement on exit; the tracker also rides down the Func
	// walk so Subjob steps can drop the contribution while the body
	// drives the subwave. Used by the per-Limiter max-concurrency
	// assertion in Run.
	var tracker *limiterTracker
	var opts []psg.OpOption
	if len(runner.LimiterIndexes) > 0 {
		limIdx := runner.LimiterIndexes[0]
		opts = append(opts, psg.WithLimits(c.TaskLimiters[limIdx]))
		tracker = c.taskLimiterTrackers[limIdx]
	}
	body := psg.NewTask(func(ctx context.Context) error {
		if tracker != nil {
			tracker.enter()
			defer tracker.exit()
		}
		// Plan-baked structural cancellation trigger: this designated
		// launcher's body cancels the subwave while holding its permit,
		// with siblings likely blocked acquiring the shared limiter. The
		// leak surfaces only when scheduling lands the cancel in a blocked
		// acquire's grant window.
		if c.cancel != nil && runner.ID == c.Plan.CancelTriggerRunnerID {
			c.cancel()
		}
		v := &simValue{OriginRunnerID: runner.ID, DispatchTime: time.Now()}
		if err := c.executeFuncInTask(ctx, t, runner.Body, v, tracker); err != nil {
			return err
		}
		if c.shouldReturnError(runner.Body) {
			return ExpectedHandlerError{OpKind: "Launcher", OpID: runner.ID}
		}
		return nil
	})
	var w *psg.Wave
	if bindWave {
		w = c.Wave
	}
	return psg.NewLauncher(w, body, opts...)
}

// disposition tells an op driver how to react to an error returned by a psg
// operation.
type disposition int

const (
	dispDone    disposition = iota // completed (err == nil)
	dispRetry                      // injected handler error: work was Free'd, re-drive
	dispAbandon                    // cancellation / wave teardown: stop without completing
	dispFail                       // unexpected: fail the test
)

// classify maps an error from a psg operation to a driver disposition. It is
// the single point that decides which errors the sim tolerates as expected
// disruptions: injected handler errors (re-drive — the work was Free'd, not
// queued) and cancellation / wave-done (abandon — the op legitimately did not
// complete). Anything else is a real failure. dispAbandon is dormant until a
// disruption (e.g. mid-run cancellation) is injected; absent that, these
// errors never surface here.
func classify(err error) disposition {
	var expected ExpectedHandlerError
	switch {
	case err == nil:
		return dispDone
	case errors.As(err, &expected):
		return dispRetry
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, psg.ErrWaveDone):
		return dispAbandon
	default:
		return dispFail
	}
}

// startTask dispatches a Plan Launcher. The Launcher was pre-built
// in Run(); Start can return an ExpectedHandlerError from internal
// backpressure-yielding (a previously-queued sink handler returned an
// injected error). In that case the work was Free()'d and NOT queued;
// retry until Start either succeeds or returns a non-injected error.
func (c *controller) startTask(ctx context.Context, t assert.TestingT, runnerIdx int) {
	chk := assert.New(t)
	runner := &c.Launchers[runnerIdx]
	for {
		err := runner.Start(ctx)
		switch classify(err) {
		case dispRetry:
			continue
		case dispFail:
			chk.NoError(err)
		}
		return
	}
}

// submitFresh handles a top-level Submit by constructing a fresh
// simValue (no upstream context) and Submit-ing it to the target sink.
func (c *controller) submitFresh(ctx context.Context, t assert.TestingT, s Submit) {
	v := &simValue{DispatchTime: time.Now()}
	c.submitTo(ctx, t, s.SinkKind, s.SinkIndex, v, nil)
}

// submitTo routes a value into a Plan-level sink. With Wave 3's task-
// context-safe Submit, this is a thin wrapper around the op's Submit.
// Retry on ExpectedHandlerError covers the case where Submit yields
// for backpressure and a previously-queued sink handler returns an
// injected error.
func (c *controller) submitTo(
	ctx context.Context, t assert.TestingT, kind SinkKind, idx int, v *simValue, valErr error,
) {
	chk := assert.New(t)
	for {
		var err error
		switch kind {
		case SinkFunnel:
			err = c.Funnels[idx].SubmitResult(ctx, v, valErr)
		case SinkSkimmer:
			err = c.Skimmers[idx].SubmitResult(ctx, v, valErr)
		default:
			chk.Fail(fmt.Sprintf("unknown SinkKind %v", kind))
			return
		}
		switch classify(err) {
		case dispRetry:
			continue
		case dispFail:
			chk.NoError(err)
		}
		return
	}
}

// newSkimmerHandler builds the handler function for a Plan Skimmer —
// walks its Handle Func, accounting invocations. Upstream errors
// (valErr) are NOT propagated back; the psg.Handler returns either nil or
// its own injected ExpectedHandlerError. Matches old sim behavior:
// errors flow alongside values into the handler for it to act on, but
// the handler doesn't re-propagate them — that would short-circuit
// the framework's drain and cause subsequent queued work to be lost.
func (c *controller) newSkimmerHandler(t assert.TestingT, g *Skimmer, idx int) psg.HandlerFunc[*simValue] {
	return func(ctx context.Context, v *simValue, valErr error) error {
		_ = valErr
		_ = v
		c.skimmerInvocations[idx].Add(1)
		// Skimmers are deliberately limiter-free (drain must stay
		// permit-free — see docs/limiter-suspend-resume.md), so no
		// tracker rides this walk.
		if err := c.executeFunc(ctx, t, g.Handle, v, nil); err != nil {
			return ExpectedHandlerError{OpKind: opNameSkimmer, OpID: g.ID, Err: err}
		}
		if c.shouldReturnError(g.Handle) {
			return ExpectedHandlerError{OpKind: opNameSkimmer, OpID: g.ID}
		}
		return nil
	}
}

// newFunnelFactory builds the funnel factory that the framework
// invokes per-instance. Accumulate and Flush bodies are walked from
// inside AccumulateFn/FlushFn; downstream Submits go through submitTo.
// FlushFn returns just error after Wave 2 — no output type.
func (c *controller) newFunnelFactory(
	t assert.TestingT, cmb *Funnel, idx int,
) psg.AccumulatorFactory[*simValue] {
	_ = idx
	// Concurrency tracking: bump the FunnelLimiter tracker on entry to
	// Accumulate or Flush, decrement on exit; the tracker also rides
	// down the Func walk so Subjob steps can drop the contribution while
	// the body drives the subwave.
	var tracker *limiterTracker
	if len(cmb.LimiterIndexes) > 0 {
		tracker = c.funnelLimiterTrackers[cmb.LimiterIndexes[0]]
	}
	return psg.NewAccumulatorFactory(func() psg.Accumulator[*simValue] {
		return psg.FuncAccumulator[*simValue]{
			AccumulateFn: func(ctx context.Context, v *simValue, valErr error) (time.Time, error) {
				if tracker != nil {
					tracker.enter()
					defer tracker.exit()
				}
				err := c.executeFunc(ctx, t, cmb.Accumulate, v, tracker)
				if err == nil && c.shouldReturnError(cmb.Accumulate) {
					err = ExpectedHandlerError{OpKind: opNameFunnel, OpID: cmb.ID}
				}
				// No flush deadline in v1.
				_ = valErr
				return time.Time{}, err
			},
			FlushFn: func(ctx context.Context) error {
				// Flush is deliberately NOT counted against the limiter
				// tracker: the funnel limiter gates funnelWork (the
				// Accumulate dispatch) only; funnelInstance.flush never
				// acquires the permit. Counting Flush would assert more
				// than the limiter gates — and under cross-subjob sharing
				// a parent op's end-of-work Flush legitimately overlaps a
				// subjob op's Accumulate on the shared tracker. Pass nil
				// so Subjob steps in a Flush body don't drop a
				// contribution that was never added.
				v := &simValue{DispatchTime: time.Now()}
				err := c.executeFunc(ctx, t, cmb.Flush, v, nil)
				if err == nil && c.shouldReturnError(cmb.Flush) {
					err = ExpectedHandlerError{OpKind: opNameFunnel, OpID: cmb.ID}
				}
				return err
			},
		}
	}, nil)
}

// executeFunc walks a Func's Steps from a context where new tasks may
// be started (skim/funnel handler bodies, top-level dispatch).
// SelfTime sleeps for the drawn duration; Submit routes to the target
// sink; StartTask dispatches a runner; Subjob spawns a nested Pool.
// active is the enclosing body's concurrency tracker (nil when the body
// is not limiter-bound), threaded down so Subjob steps can drop the
// body's contribution while it drives the subwave.
func (c *controller) executeFunc(
	ctx context.Context, t assert.TestingT, fn *Func, v *simValue, active *limiterTracker,
) error {
	return c.executeFuncBody(ctx, t, fn, v, true, active)
}

// executeFuncInTask walks a Func's Steps from a task body. StartTask
// is skipped because the current psg API forbids dispatching new work
// from a task context (post-Wave-5 will relax this).
func (c *controller) executeFuncInTask(
	ctx context.Context, t assert.TestingT, fn *Func, v *simValue, active *limiterTracker,
) error {
	return c.executeFuncBody(ctx, t, fn, v, false, active)
}

func (c *controller) executeFuncBody(
	ctx context.Context, t assert.TestingT, fn *Func, v *simValue, allowStartTask bool,
	active *limiterTracker,
) error {
	chk := assert.New(t)
	timer := timerp.Get()
	defer timerp.Put(timer)
	for _, step := range fn.Steps {
		switch s := step.(type) {
		case SelfTime:
			d := c.drawDuration(s.Dist)
			if d > 0 {
				timerp.Reset(timer, d)
				select {
				case <-timer.C:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case Submit:
			if !c.rollProb(s.Prob) {
				continue
			}
			c.submitTo(ctx, t, s.SinkKind, s.SinkIndex, v, nil)
		case StartTask:
			if !allowStartTask {
				continue
			}
			if !c.rollProb(s.Prob) {
				continue
			}
			c.startTask(ctx, t, s.RunnerIndex)
		case Subjob:
			// Active-concurrency measurement: drop this body's
			// contribution while it drives the subwave — the span over
			// which the framework suspends the body's permit. Dropping
			// before the actual suspend and restoring after the reclaim
			// completes means both edges skew toward under-counting,
			// keeping the `observed ≤ permits` assertion sound (and
			// making this change safe to land before the suspend
			// brackets do).
			if active != nil {
				active.exit()
			}
			c.runSubjob(ctx, t, s)
			if active != nil {
				active.enter()
			}
		default:
			chk.Fail(fmt.Sprintf("unknown Step type %T", step))
		}
	}
	return nil
}

// drawDuration picks a duration from a SelfTime distribution at
// runtime. In v1 we always use the Med value for simplicity; richer
// per-invocation drawing lands with the probabilistic-mode work.
func (c *controller) drawDuration(d BiasedDurationConfig) time.Duration {
	return d.Med
}

// rollProb returns true with probability p. v1 generator only emits
// Prob=1.0 so this short-circuits; probabilistic-mode work will
// replace with a real RNG.
func (c *controller) rollProb(p float64) bool {
	return p >= 1.0
}

// shouldReturnError reports whether this Func invocation should
// surface an error. v1 generator forces ReturnErrorProb to 0 or 1
// (Deterministic-style) so this short-circuits; probabilistic-mode
// work will replace with a real RNG.
func (c *controller) shouldReturnError(fn *Func) bool {
	return fn.ReturnErrorProb >= 1.0
}

// ExpectedHandlerError marks a deliberately-returned error from a
// Skimmer Handle, Funnel Accumulate, or Funnel Flush body —
// distinguished from infrastructure errors so the drain loop can
// continue past them.
type ExpectedHandlerError struct {
	OpKind string
	OpID   int
	Err    error
}

func (e ExpectedHandlerError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("expected %s#%d error: %v", e.OpKind, e.OpID, e.Err)
	}
	return fmt.Sprintf("expected %s#%d error", e.OpKind, e.OpID)
}

func (e ExpectedHandlerError) Unwrap() error {
	return e.Err
}

// atomicMaxInt64 tracks a monotonically non-decreasing observed maximum.
type atomicMaxInt64 struct {
	value atomic.Int64
}

func (mm *atomicMaxInt64) Load() int64 {
	return mm.value.Load()
}

func (mm *atomicMaxInt64) UpdateMax(x int64) {
	for {
		old := mm.value.Load()
		if x <= old {
			return
		}
		if mm.value.CompareAndSwap(old, x) {
			return
		}
	}
}
