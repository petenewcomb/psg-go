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
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/stretchr/testify/assert"
)

// Run executes the given Plan against the current psg API via an
// adapter that translates the new vocabulary's static structure to
// today's Pool/TaskPool/CombinerPool/Gatherer/Combiner shapes. Real
// data routing uses Submit/TrySubmit; the current API's combiner-
// output-type slot is satisfied by a singleton dummy struct{}
// Gatherer.
//
// v1 supports only the linear-chain plans the minimal generator
// produces (one Submit per body, no multi-sink, no probabilistic
// Steps beyond the basic ReturnErrorProb support, no Subjob).
// Enrichment lands in follow-up commits.
func Run(ctx context.Context, t assert.TestingT, plan *Plan) error {
	traceRegion := "sim.Run"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", plan)

	pool := psg.New(ctx)
	defer pool.CancelAndWait()

	c := &controller{
		Plan:                      plan,
		Pool:                      pool,
		TaskLimiters:              make([]psg.Limiter, len(plan.TaskLimiters)),
		CombinerPool:              nil, // lazily constructed in ensurePools
		CombinerLimiters:          make([]psg.Limiter, len(plan.CombinerLimiters)),
		Gatherers:                 make([]*psg.Gatherer[*simValue], len(plan.Gatherers)),
		Combiners:                 make([]*psg.Combiner[*simValue], len(plan.Combiners)),
		concurrencyByTaskLimit:    make([]atomic.Int64, len(plan.TaskLimiters)),
		maxConcurrencyByTaskLimit: make([]atomicMaxInt64, len(plan.TaskLimiters)),
		concurrencyByCombLimit:    make([]atomic.Int64, len(plan.CombinerLimiters)),
		maxConcurrencyByCombLimit: make([]atomicMaxInt64, len(plan.CombinerLimiters)),
		gathererInvocations:       make([]atomic.Int64, len(plan.Gatherers)),
	}
	return c.Run(ctx, t)
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
	Plan             *Plan
	Pool             *psg.Pool
	TaskLimiters     []psg.Limiter
	CombinerPool     *psg.CombinerPool
	CombinerLimiters []psg.Limiter
	Gatherers        []*psg.Gatherer[*simValue]
	Combiners        []*psg.Combiner[*simValue]
	// TaskRunners holds one psg.TaskRunner0 per Plan TaskRunner. The
	// closure inside each runs the runner's Body Func, which Submits
	// directly to downstream Gatherers/Combiners.
	TaskRunners []psg.TaskRunner0

	limitersOnce sync.Once
	combPoolOnce sync.Once

	concurrencyByTaskLimit    []atomic.Int64
	maxConcurrencyByTaskLimit []atomicMaxInt64
	concurrencyByCombLimit    []atomic.Int64
	maxConcurrencyByCombLimit []atomicMaxInt64
	gathererInvocations       []atomic.Int64
	StartTime                 time.Time
}

func (c *controller) Run(ctx context.Context, t assert.TestingT) error {
	traceRegion := "sim.controller.Run"
	c.StartTime = time.Now()

	c.ensurePools()

	// Construct Gatherers and Combiners against the Pool. Order matters:
	// Combiners reference Gatherers (in body Submits), so Gatherers must
	// exist first.
	for i, g := range c.Plan.Gatherers {
		gp := g
		idx := i
		gatherer := psg.NewGatherer(c.newGathererHandler(t, gp, idx))
		c.Gatherers[i] = &gatherer
	}
	for i, cmb := range c.Plan.Combiners {
		cp := cmb
		idx := i
		var opts []psg.OpOption
		if len(cp.LimiterIndexes) > 0 {
			opts = append(opts, psg.WithLimits(c.CombinerLimiters[cp.LimiterIndexes[0]]))
		}
		combiner := psg.NewCombiner(c.CombinerPool, c.newCombinerFactory(t, cp, idx), opts...)
		c.Combiners[i] = &combiner
	}
	// Construct TaskRunners after Combiners/Gatherers so the bodies can
	// reference them via Submit. TaskRunner Bodies may StartTask other
	// runners, but only after the entire array is populated (a runner's
	// Body never runs during construction).
	c.TaskRunners = make([]psg.TaskRunner0, len(c.Plan.TaskRunners))
	for i, runner := range c.Plan.TaskRunners {
		c.TaskRunners[i] = c.newTaskRunner(t, runner)
	}

	// Execute top-level Steps.
	for i, step := range c.Plan.Steps {
		trace.Logf(ctx, traceRegion, "%v step %d/%d: %T", c.Plan, i+1, len(c.Plan.Steps)+1, step)
		c.executeStep(ctx, t, step)
	}

	// Drain.
	chk := assert.New(t)
	for {
		err := c.Pool.CloseAndGatherAll(ctx)
		if err == nil {
			break
		}
		var expectedErr ExpectedHandlerError
		if errors.As(err, &expectedErr) {
			// Gatherer.Handle returned an error as expected.
			continue
		}
		chk.NoError(err)
		break
	}

	// Per-Gatherer sink-invocation bounds.
	for i, want := range c.Plan.MinGathererInvocations {
		got := c.gathererInvocations[i].Load()
		chk.GreaterOrEqualf(got, int64(want),
			"Plan#%d Gatherer#%d min invocations (got %d, want >=%d)",
			c.Plan.ID, c.Plan.Gatherers[i].ID, got, want)
	}
	for i, want := range c.Plan.MaxGathererInvocations {
		got := c.gathererInvocations[i].Load()
		chk.LessOrEqualf(got, int64(want),
			"Plan#%d Gatherer#%d max invocations (got %d, want <=%d)",
			c.Plan.ID, c.Plan.Gatherers[i].ID, got, want)
	}
	// Per-Limiter aggregate concurrency: observed max must not exceed
	// configured permits.
	for i, lim := range c.Plan.TaskLimiters {
		observed := c.maxConcurrencyByTaskLimit[i].Load()
		chk.LessOrEqualf(observed, int64(lim.Permits),
			"TaskLimiter#%d observed concurrency %d > permits %d", lim.ID, observed, lim.Permits)
	}
	for i, lim := range c.Plan.CombinerLimiters {
		observed := c.maxConcurrencyByCombLimit[i].Load()
		chk.LessOrEqualf(observed, int64(lim.Permits),
			"CombinerLimiter#%d observed concurrency %d > permits %d", lim.ID, observed, lim.Permits)
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

// ensurePools lazily constructs the psg.Limiter and psg.CombinerPool
// instances backing the Plan's Limiters. One Limiter per
// Plan.TaskLimiters and Plan.CombinerLimiters entry. A single
// CombinerPool hosts all Combiners — per-Combiner concurrency is
// enforced via the CombinerLimiters bound to each Combiner via
// psg.WithLimits.
func (c *controller) ensurePools() {
	c.limitersOnce.Do(func() {
		for i, lim := range c.Plan.TaskLimiters {
			c.TaskLimiters[i] = psg.NewSemaphore(lim.Permits)
		}
		for i, lim := range c.Plan.CombinerLimiters {
			c.CombinerLimiters[i] = psg.NewSemaphore(lim.Permits)
		}
	})
	c.combPoolOnce.Do(func() {
		c.CombinerPool = psg.NewCombinerPool(c.Pool)
	})
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

// runSubjob executes a Subjob step by spinning up a fresh psg.Pool and
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
	err := Run(ctx, t, s.Plan)
	var expectedErr ExpectedHandlerError
	if err != nil && !errors.As(err, &expectedErr) {
		assert.New(t).NoError(err)
	}
}

// newTaskRunner constructs the psg.TaskRunner0 that backs a Plan
// TaskRunner. The task body walks the Plan's Body Func; Submits go
// directly to downstream sinks (Combiners/Gatherers) via Submit, and
// StartTask is skipped because the current API forbids dispatching new
// work from a task body.
func (c *controller) newTaskRunner(t assert.TestingT, runner *TaskRunner) psg.TaskRunner0 {
	// Concurrency tracking: bump TaskLimiter counter on entry to the
	// task body, decrement on exit. Used by the per-Limiter
	// max-concurrency assertion in Run.
	trackEntry := func() func() { return func() {} }
	var opts []psg.OpOption
	if len(runner.LimiterIndexes) > 0 {
		limIdx := runner.LimiterIndexes[0]
		opts = append(opts, psg.WithLimits(c.TaskLimiters[limIdx]))
		trackEntry = func() func() {
			cur := c.concurrencyByTaskLimit[limIdx].Add(1)
			c.maxConcurrencyByTaskLimit[limIdx].UpdateMax(cur)
			return func() { c.concurrencyByTaskLimit[limIdx].Add(-1) }
		}
	}
	body := psgfn.TaskFunc0(func(ctx context.Context) error {
		defer trackEntry()()
		v := &simValue{OriginRunnerID: runner.ID, DispatchTime: time.Now()}
		if err := c.executeFuncInTask(ctx, t, runner.Body, v); err != nil {
			return err
		}
		if c.shouldReturnError(runner.Body) {
			return ExpectedHandlerError{OpKind: "TaskRunner", OpID: runner.ID}
		}
		return nil
	})
	return psg.NewTaskRunner0(c.Pool, body, opts...)
}

// startTask dispatches a Plan TaskRunner. The TaskRunner was pre-built
// in Run(); Start can return an ExpectedHandlerError from internal
// backpressure-yielding (a previously-queued sink handler returned an
// injected error). In that case the work was Free()'d and NOT queued;
// retry until Start either succeeds or returns a non-injected error.
func (c *controller) startTask(ctx context.Context, t assert.TestingT, runnerIdx int) {
	chk := assert.New(t)
	runner := &c.TaskRunners[runnerIdx]
	for {
		err := runner.Start(ctx)
		if err == nil {
			return
		}
		var expectedErr ExpectedHandlerError
		if errors.As(err, &expectedErr) {
			continue
		}
		chk.NoError(err)
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
		case SinkCombiner:
			err = c.Combiners[idx].Submit(ctx, v, valErr)
		case SinkGatherer:
			err = c.Gatherers[idx].Submit(ctx, c.Pool, v, valErr)
		default:
			chk.Fail(fmt.Sprintf("unknown SinkKind %v", kind))
			return
		}
		if err == nil {
			return
		}
		var expectedErr ExpectedHandlerError
		if errors.As(err, &expectedErr) {
			continue
		}
		chk.NoError(err)
		return
	}
}

// newGathererHandler builds the handler function for a Plan Gatherer —
// walks its Handle Func, accounting invocations. Upstream errors
// (valErr) are NOT propagated back; the Handler returns either nil or
// its own injected ExpectedHandlerError. Matches old sim behavior:
// errors flow alongside values into the handler for it to act on, but
// the handler doesn't re-propagate them — that would short-circuit
// the framework's drain and cause subsequent queued work to be lost.
func (c *controller) newGathererHandler(t assert.TestingT, g *Gatherer, idx int) psgfn.Gather[*simValue] {
	return func(ctx context.Context, v *simValue, valErr error) error {
		_ = valErr
		_ = v
		c.gathererInvocations[idx].Add(1)
		if err := c.executeFunc(ctx, t, g.Handle, v); err != nil {
			return ExpectedHandlerError{OpKind: opNameGatherer, OpID: g.ID, Err: err}
		}
		if c.shouldReturnError(g.Handle) {
			return ExpectedHandlerError{OpKind: opNameGatherer, OpID: g.ID}
		}
		return nil
	}
}

// newCombinerFactory builds the combiner factory that the framework
// invokes per-instance. Accumulate and Flush bodies are walked from
// inside AccumulateFn/FlushFn; downstream Submits go through submitTo.
// FlushFn returns just error after Wave 2 — no output type.
func (c *controller) newCombinerFactory(
	t assert.TestingT, cmb *Combiner, idx int,
) psgfn.CombinerFactory[*simValue] {
	_ = idx
	// Concurrency tracking: bump CombinerLimiter counter on entry to
	// Accumulate or Flush, decrement on exit.
	trackEntry := func() func() { return func() {} }
	if len(cmb.LimiterIndexes) > 0 {
		limIdx := cmb.LimiterIndexes[0]
		trackEntry = func() func() {
			cur := c.concurrencyByCombLimit[limIdx].Add(1)
			c.maxConcurrencyByCombLimit[limIdx].UpdateMax(cur)
			return func() { c.concurrencyByCombLimit[limIdx].Add(-1) }
		}
	}
	return func() psgfn.Accumulator[*simValue] {
		return psgfn.FuncAccumulator[*simValue]{
			AccumulateFn: func(ctx context.Context, v *simValue, valErr error) (time.Time, error) {
				defer trackEntry()()
				err := c.executeFunc(ctx, t, cmb.Accumulate, v)
				if err == nil && c.shouldReturnError(cmb.Accumulate) {
					err = ExpectedHandlerError{OpKind: opNameCombiner, OpID: cmb.ID}
				}
				// No flush deadline in v1.
				_ = valErr
				return time.Time{}, err
			},
			FlushFn: func(ctx context.Context) error {
				defer trackEntry()()
				v := &simValue{DispatchTime: time.Now()}
				err := c.executeFunc(ctx, t, cmb.Flush, v)
				if err == nil && c.shouldReturnError(cmb.Flush) {
					err = ExpectedHandlerError{OpKind: opNameCombiner, OpID: cmb.ID}
				}
				return err
			},
		}
	}
}

// executeFunc walks a Func's Steps from a context where new tasks may
// be started (gather/combine handler bodies, top-level dispatch).
// SelfTime sleeps for the drawn duration; Submit routes to the target
// sink; StartTask dispatches a runner; Subjob spawns a nested Pool.
func (c *controller) executeFunc(ctx context.Context, t assert.TestingT, fn *Func, v *simValue) error {
	return c.executeFuncBody(ctx, t, fn, v, true)
}

// executeFuncInTask walks a Func's Steps from a task body. StartTask
// is skipped because the current psg API forbids dispatching new work
// from a task context (post-Wave-5 will relax this).
func (c *controller) executeFuncInTask(ctx context.Context, t assert.TestingT, fn *Func, v *simValue) error {
	return c.executeFuncBody(ctx, t, fn, v, false)
}

func (c *controller) executeFuncBody(
	ctx context.Context, t assert.TestingT, fn *Func, v *simValue, allowStartTask bool,
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
			c.runSubjob(ctx, t, s)
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
// Gatherer Handle, Combiner Accumulate, or Combiner Flush body —
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
