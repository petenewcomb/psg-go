// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"sort"
	"time"

	"pgregory.net/rapid"
)

// Plan is the static description of one Wave-equivalent unit of work
// in the destination streampool API. The runtime adapter executes a
// Plan against the current psg API; as reshape waves land, only the
// adapter changes.
//
// The generator produces a layered DAG:
//
//   - Skimmers (terminal sinks) at depth 0
//   - Funnels at depths 1..MaxFunnelDepth, each Accumulate.Submit
//     wired to a shallower sink. Multiple paths may share a Funnel
//     (fan-in).
//   - Origin Launchers (one per path), Body.Submit wired to a sink
//     at depth = path-length - 1. StartTask entries for these go into
//     Plan.Steps.
//   - Fan-out Launchers dispatched from inside Skimmer.Handle and
//     Funnel.Accumulate bodies via StartTask steps
//     (scatter-from-skim/funnel, the recursive-spawn pattern).
//     Each fan-out runner Submits to a Skimmer; cycle prevention is
//     enforced by ordering (Skimmer #i's StartTasks only target
//     Skimmers with index > i).
//
// Sink-invocation bounds and path durations are computed by walking
// the DAG forward from each top-level StartTask, memoized per op.
type Plan struct {
	ID              int
	PathCount       int
	Steps           []Step // top-level: StartTask, possibly Subjob
	MaxPathDuration time.Duration
	TaskLimiters    []Limiter
	FunnelLimiters  []Limiter
	Launchers       []*Launcher
	Funnels         []*Funnel
	Skimmers        []*Skimmer
	SubjobCount     int
	SubjobTaskCount int
	// Sink-invocation bounds computed at plan time. In Deterministic
	// mode and the current v1 generator (all Probs = 1.0),
	// MinSkimmerInvocations[i] == MaxSkimmerInvocations[i]. When
	// probabilistic-mode generation lands, Min may drop to 0 for
	// non-unit probabilities.
	MinSkimmerInvocations []int
	MaxSkimmerInvocations []int
	// CancelTriggerRunnerID, when >= 0, names the Launcher whose body
	// cancels this (sub)plan's wave when it runs — a plan-baked,
	// structural mid-flight cancellation. -1 means no cancellation. Only
	// set on subjob plans (see newFunc).
	CancelTriggerRunnerID int
}

// NewPlan generates a new Plan for property-based testing.
func NewPlan(t *rapid.T, config *Config) *Plan {
	var nextIDs idCounters
	return newPlan(t, config, &nextIDs, nil)
}

type idCounters struct {
	Plan        int
	TaskLimiter int
	CombLimiter int
	Launcher    int
	Funnel      int
	Skimmer     int
}

// The generator is dense by nature; refactoring into helpers obscures
// the path-construction flow.
//
// parentPlan is non-nil when generating a nested (subjob) Plan; it
// enables cross-subjob limiter inheritance (see Limiter.InheritFromParent).
//
// drawWeight picks a launcher's per-dispatch permit weight: a value in
// [1, permits] when bound to a weighted task limiter (exercising w>=2
// acquisition), else 1. The clamp to permits keeps the demand feasible — the
// sim exercises the weighted acquire/gather path, not the oversized-refuse one
// (that is covered by the weighted limiter's unit tests).
func drawWeight(t *rapid.T, lim Limiter, name string) int {
	if !lim.Weighted {
		return 1
	}
	return rapid.IntRange(1, lim.Permits).Draw(t, name+".Weight")
}

//nolint:gocognit,funlen // see above
func newPlan(t *rapid.T, config *Config, nextIDs *idCounters, parentPlan *Plan) *Plan {
	planID := nextIDs.Plan
	nextIDs.Plan++
	planName := fmt.Sprintf("Plan#%d", planID)
	plan := &Plan{ID: planID, CancelTriggerRunnerID: -1}

	nextIDsOrigin := *nextIDs

	// === Limiters ===
	// genLimiter draws one limiter, possibly aliasing a same-kind parent
	// limiter (subjob plans only).
	genLimiter := func(
		kind string, i int, cfg *LimiterConfig, parentLimiters []Limiter, nextID *int,
	) Limiter {
		if parentPlan != nil && len(parentLimiters) > 0 &&
			cfg.Inherit.Draw(t, fmt.Sprintf("%s.%sLimiters[%d].Inherit", planName, kind, i)) {
			k := rapid.IntRange(0, len(parentLimiters)-1).Draw(t,
				fmt.Sprintf("%s.%sLimiters[%d].InheritFrom", planName, kind, i))
			parent := parentLimiters[k]
			return Limiter{ID: parent.ID, Permits: parent.Permits, Weighted: parent.Weighted, InheritFromParent: k}
		}
		id := *nextID
		*nextID++
		permits := cfg.Permits.Draw(t, fmt.Sprintf("%sLimiter#%d.Permits", kind, id))
		// Only task limiters can be weighted, and only when permits >= 2 (a
		// weight-1-only ceiling is plain by another name).
		weighted := kind == "Task" && permits >= 2 &&
			cfg.Weighted.Draw(t, fmt.Sprintf("%sLimiter#%d.Weighted", kind, id))
		return Limiter{
			ID:                id,
			Permits:           permits,
			Weighted:          weighted,
			InheritFromParent: -1,
		}
	}
	var parentTaskLimiters, parentFunnelLimiters []Limiter
	if parentPlan != nil {
		parentTaskLimiters = parentPlan.TaskLimiters
		parentFunnelLimiters = parentPlan.FunnelLimiters
	}
	taskLimiterCount := config.TaskLimiter.Count.Draw(t, planName+".TaskLimiterCount")
	plan.TaskLimiters = make([]Limiter, taskLimiterCount)
	for i := range plan.TaskLimiters {
		plan.TaskLimiters[i] = genLimiter("Task", i, &config.TaskLimiter, parentTaskLimiters, &nextIDs.TaskLimiter)
	}
	combLimiterCount := config.FunnelLimiter.Count.Draw(t, planName+".FunnelLimiterCount")
	plan.FunnelLimiters = make([]Limiter, combLimiterCount)
	for i := range plan.FunnelLimiters {
		plan.FunnelLimiters[i] = genLimiter("Funnel", i, &config.FunnelLimiter, parentFunnelLimiters, &nextIDs.CombLimiter)
	}

	// === Skimmers, layered by depth across [0, MaxDepth]. Skimmers
	// at depth == MaxDepth are terminal (no StartTasks); shallower ones
	// can scatter to strictly-deeper Skimmers. Multiple Skimmers may
	// share a depth (including multiple terminals). ===
	maxSkimmerDepth := config.Skimmer.MaxDepth
	if maxSkimmerDepth < 0 {
		maxSkimmerDepth = 0
	}
	skimmerCount := config.Skimmer.Count.Draw(t, planName+".SkimmerCount")
	plan.Skimmers = make([]*Skimmer, skimmerCount)
	for i := range plan.Skimmers {
		id := nextIDs.Skimmer
		nextIDs.Skimmer++
		depth := 0
		if maxSkimmerDepth > 0 {
			depth = rapid.IntRange(0, maxSkimmerDepth).Draw(t, fmt.Sprintf("Skimmer#%d.Depth", id))
		}
		plan.Skimmers[i] = &Skimmer{
			ID:    id,
			Depth: depth,
			Handle: newFunc(t, plan, config, &config.Skimmer.Handle, nextIDs,
				fmt.Sprintf("Skimmer#%d.Handle", id), false), // skimmers can't drive subwaves
		}
	}
	// Sort Skimmers by depth ascending — needed so cascade-target
	// lookups can iterate forward when wiring StartTasks below.
	sort.SliceStable(plan.Skimmers, func(i, j int) bool {
		return plan.Skimmers[i].Depth < plan.Skimmers[j].Depth
	})
	// Ensure at least one Skimmer is at MaxDepth (terminal). If the
	// random draw didn't produce one, snap the last Skimmer to MaxDepth.
	if maxSkimmerDepth > 0 && len(plan.Skimmers) > 0 &&
		plan.Skimmers[len(plan.Skimmers)-1].Depth < maxSkimmerDepth {
		plan.Skimmers[len(plan.Skimmers)-1].Depth = maxSkimmerDepth
	}

	// === Funnels (depth 1..maxFunnelDepth, shared across paths for fan-in) ===
	maxFunnelDepth := config.Path.Length.Max - 1
	if maxFunnelDepth < 1 {
		maxFunnelDepth = 1
	}
	funnelCount := config.Funnel.Count.Draw(t, planName+".FunnelCount")
	plan.Funnels = make([]*Funnel, funnelCount)
	for i := range plan.Funnels {
		id := nextIDs.Funnel
		nextIDs.Funnel++
		depth := rapid.IntRange(1, maxFunnelDepth).Draw(t, fmt.Sprintf("Funnel#%d.Depth", id))
		plan.Funnels[i] = &Funnel{
			ID:    id,
			Depth: depth,
			Accumulate: newFunc(t, plan, config, &config.Funnel.Accumulate, nextIDs,
				fmt.Sprintf("Funnel#%d.Accumulate", id), true),
			Flush: newFunc(t, plan, config, &config.Funnel.Flush, nextIDs,
				fmt.Sprintf("Funnel#%d.Flush", id), true),
		}
		if combLimiterCount > 0 {
			limIdx := rapid.IntRange(0, combLimiterCount-1).Draw(t, fmt.Sprintf("Funnel#%d.LimiterIndex", id))
			plan.Funnels[i].LimiterIndexes = []int{limIdx}
		}
	}
	// Sort by depth ascending so earlier-indexed Funnels are shallower —
	// enables the wiring loop below to pick downstream candidates by index.
	sort.SliceStable(plan.Funnels, func(i, j int) bool {
		return plan.Funnels[i].Depth < plan.Funnels[j].Depth
	})

	// Wire each Funnel.Accumulate.Submit to a shallower sink
	// (Skimmer or Funnel with strictly smaller Depth).
	type sinkRef struct {
		kind SinkKind
		idx  int
	}
	for i, c := range plan.Funnels {
		var sinks []sinkRef
		for gi := range plan.Skimmers {
			sinks = append(sinks, sinkRef{SinkSkimmer, gi})
		}
		for ci := 0; ci < i; ci++ {
			if plan.Funnels[ci].Depth < c.Depth {
				sinks = append(sinks, sinkRef{SinkFunnel, ci})
			}
		}
		pick := rapid.IntRange(0, len(sinks)-1).Draw(t,
			fmt.Sprintf("Funnel#%d.Accumulate.SubmitTarget", c.ID))
		c.Accumulate.Steps = append(c.Accumulate.Steps, Submit{
			Prob:      probValue(config, 1.0),
			SinkKind:  sinks[pick].kind,
			SinkIndex: sinks[pick].idx,
		})
	}

	// === Scatter-from-skim and scatter-from-funnel. ===
	//
	// Non-terminal Skimmers (depth < MaxDepth) dispatch fan-out
	// runners targeting Skimmers at strictly greater depth — this
	// gives multi-hop Skimmer chains while keeping the cascade
	// bounded by MaxDepth. Funnels dispatch fan-out runners
	// targeting any Skimmer.
	// Fan-out runners have intentionally simple bodies: SelfTime
	// drawn from Launcher.Body config, plus the destination Submit.
	// No Subjob — Subjobs in fan-outs compound the cascade
	// catastrophically (each Skimmer-cascade level multiplies, and
	// adding Subjob recursion on top is too much). Origin Launchers
	// (paths) still get Subjob via newFunc.
	addFanoutRunner := func(targetSkimmerIdx int, name string) int {
		id := nextIDs.Launcher
		nextIDs.Launcher++
		body := &Func{}
		errCfg := BiasedBoolConfig{Probability: config.Launcher.Body.ReturnErrorProb}
		if errCfg.Draw(t, name+".ReturnError") {
			body.ReturnErrorProb = 1
		}
		dist := config.Launcher.Body.SelfTime
		if config.Deterministic {
			dist = BiasedDurationConfig{
				Min: dist.Med, Med: dist.Med, Max: dist.Med,
			}
		}
		body.Steps = append(body.Steps,
			SelfTime{Dist: dist},
			Submit{
				Prob:      probValue(config, 1.0),
				SinkKind:  SinkSkimmer,
				SinkIndex: targetSkimmerIdx,
			},
		)
		runner := &Launcher{
			ID:    id,
			Depth: 1,
			Body:  body,
		}
		if taskLimiterCount > 0 {
			limIdx := rapid.IntRange(0, taskLimiterCount-1).Draw(t, name+".LimiterIndex")
			runner.LimiterIndexes = []int{limIdx}
			runner.Weight = drawWeight(t, plan.TaskLimiters[limIdx], name)
		}
		plan.Launchers = append(plan.Launchers, runner)
		return len(plan.Launchers) - 1
	}
	for gIdx, g := range plan.Skimmers {
		if g.Depth >= maxSkimmerDepth {
			continue // terminal — no StartTasks
		}
		// Candidate targets: Skimmers at strictly greater depth.
		var candidates []int
		for tIdx, t := range plan.Skimmers {
			if t.Depth > g.Depth {
				candidates = append(candidates, tIdx)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		sc := config.Skimmer.ScatterCount.Draw(t, fmt.Sprintf("Skimmer#%d.ScatterCount", g.ID))
		for s := 0; s < sc; s++ {
			pick := rapid.IntRange(0, len(candidates)-1).Draw(t,
				fmt.Sprintf("Skimmer#%d.Scatter[%d].TargetSkimmer", g.ID, s))
			runnerIdx := addFanoutRunner(candidates[pick],
				fmt.Sprintf("FanoutRunner.from-Skimmer#%d[%d]", g.ID, s))
			g.Handle.Steps = append(g.Handle.Steps, StartTask{
				Prob:        probValue(config, 1.0),
				RunnerIndex: runnerIdx,
			})
		}
		_ = gIdx
	}
	for _, c := range plan.Funnels {
		if len(plan.Skimmers) == 0 {
			continue
		}
		sc := config.Funnel.ScatterCount.Draw(t, fmt.Sprintf("Funnel#%d.ScatterCount", c.ID))
		for s := 0; s < sc; s++ {
			targetIdx := rapid.IntRange(0, len(plan.Skimmers)-1).Draw(t,
				fmt.Sprintf("Funnel#%d.Scatter[%d].TargetSkimmer", c.ID, s))
			runnerIdx := addFanoutRunner(targetIdx,
				fmt.Sprintf("FanoutRunner.from-Funnel#%d[%d]", c.ID, s))
			c.Accumulate.Steps = append(c.Accumulate.Steps, StartTask{
				Prob:        probValue(config, 1.0),
				RunnerIndex: runnerIdx,
			})
		}
	}

	// === Origin Launchers (one per path). Each path's Body.Submit
	// targets a sink at depth = pathLength - 1. Multiple paths may
	// share the same target — that's fan-in. ===
	plan.PathCount = config.Path.Count.Draw(t, planName+".PathCount")
	for i := 0; i < plan.PathCount; i++ {
		pathName := fmt.Sprintf("%s.Path[%d]", planName, i)
		length := config.Path.Length.Draw(t, pathName+".Length")

		var pickedSinkKind SinkKind
		var pickedSinkIdx int
		if length == 1 {
			pickedSinkKind = SinkSkimmer
			pickedSinkIdx = rapid.IntRange(0, len(plan.Skimmers)-1).Draw(t, pathName+".TerminalSkimmer")
		} else {
			// Try to find a Funnel exactly at depth length-1
			var candidates []int
			for ci, c := range plan.Funnels {
				if c.Depth == length-1 {
					candidates = append(candidates, ci)
				}
			}
			switch {
			case len(candidates) > 0:
				pickedSinkKind = SinkFunnel
				pickedSinkIdx = candidates[rapid.IntRange(0, len(candidates)-1).Draw(t, pathName+".SinkPick")]
			case len(plan.Funnels) > 0:
				// Fallback: any Funnel
				pickedSinkKind = SinkFunnel
				pickedSinkIdx = rapid.IntRange(0, len(plan.Funnels)-1).Draw(t, pathName+".SinkPick")
			default:
				// No Funnels — target a Skimmer
				pickedSinkKind = SinkSkimmer
				pickedSinkIdx = rapid.IntRange(0, len(plan.Skimmers)-1).Draw(t, pathName+".SinkPick")
			}
		}

		id := nextIDs.Launcher
		nextIDs.Launcher++
		runner := &Launcher{
			ID:    id,
			Depth: length,
			Body: newFunc(t, plan, config, &config.Launcher.Body, nextIDs,
				fmt.Sprintf("Launcher#%d.Body", id), true),
		}
		if taskLimiterCount > 0 {
			limIdx := rapid.IntRange(0, taskLimiterCount-1).Draw(t, fmt.Sprintf("Launcher#%d.LimiterIndex", id))
			runner.LimiterIndexes = []int{limIdx}
			runner.Weight = drawWeight(t, plan.TaskLimiters[limIdx], fmt.Sprintf("Launcher#%d", id))
		}
		runner.Body.Steps = append(runner.Body.Steps, Submit{
			Prob:      probValue(config, 1.0),
			SinkKind:  pickedSinkKind,
			SinkIndex: pickedSinkIdx,
		})
		plan.Launchers = append(plan.Launchers, runner)
		plan.Steps = append(plan.Steps, StartTask{
			Prob:        probValue(config, 1.0),
			RunnerIndex: len(plan.Launchers) - 1,
		})
	}
	plan.Steps = rapid.Permutation(plan.Steps).Draw(t, planName+".StepsPermutation")

	// === Compute sink-invocation bounds and path durations via DAG walk. ===
	plan.MinSkimmerInvocations = make([]int, len(plan.Skimmers))
	plan.MaxSkimmerInvocations = make([]int, len(plan.Skimmers))
	contribCache := map[any]map[int]int{}
	skimmerIdx := map[*Skimmer]int{}
	for gi, g := range plan.Skimmers {
		skimmerIdx[g] = gi
	}
	var contribOf func(op any) map[int]int
	contribOf = func(op any) map[int]int {
		if c, ok := contribCache[op]; ok {
			return c
		}
		contrib := map[int]int{}
		var body *Func
		switch o := op.(type) {
		case *Launcher:
			body = o.Body
		case *Funnel:
			body = o.Accumulate
		case *Skimmer:
			body = o.Handle
			contrib[skimmerIdx[o]] = 1
		}
		if body != nil {
			for _, step := range body.Steps {
				switch s := step.(type) {
				case Submit:
					var target any
					switch s.SinkKind {
					case SinkSkimmer:
						target = plan.Skimmers[s.SinkIndex]
					case SinkFunnel:
						target = plan.Funnels[s.SinkIndex]
					}
					for gi, cnt := range contribOf(target) {
						contrib[gi] += cnt
					}
				case StartTask:
					for gi, cnt := range contribOf(plan.Launchers[s.RunnerIndex]) {
						contrib[gi] += cnt
					}
				}
			}
		}
		contribCache[op] = contrib
		return contrib
	}
	for _, step := range plan.Steps {
		st, ok := step.(StartTask)
		if !ok {
			continue
		}
		for gi, cnt := range contribOf(plan.Launchers[st.RunnerIndex]) {
			plan.MinSkimmerInvocations[gi] += cnt
			plan.MaxSkimmerInvocations[gi] += cnt
		}
	}

	plan.MaxPathDuration = computeMaxPathDuration(plan)
	plan.SubjobCount = nextIDs.Plan - nextIDsOrigin.Plan

	return plan
}

// probValue returns 1.0 in Deterministic mode, else the given prob.
// v1 generator emits only Prob=1.0; this hook is here so a future
// probabilistic generator can flow Prob values through here.
func probValue(config *Config, p float64) float64 {
	if config.Deterministic {
		return 1.0
	}
	return p
}

// newFunc constructs a Func with a SelfTime step (always present) and
// optional Subjob step (probabilistically). ReturnErrorProb is resolved
// to a deterministic 0 or 1 at plan-construction time via a Bernoulli
// draw against funcConfig.ReturnErrorProb — matches the old sim's
// ReturnError bool semantics. Submits and StartTasks are appended by
// the caller based on path-construction context.
// allowSubjob is false for skimmer Handle bodies: a skim handler cannot
// drive a subwave (it would monopolize the sole serial skim driver and
// deadlock — see docs/limiter-suspend-resume.md; the framework panics on it).
// Subwork from a skim handler goes through a funnel or a launched task,
// so the generator simply never nests a subjob directly under a skimmer.
func newFunc(
	t *rapid.T, plan *Plan, config *Config, funcConfig *FuncConfig, nextIDs *idCounters, name string,
	allowSubjob bool,
) *Func {
	fn := &Func{}
	errCfg := BiasedBoolConfig{Probability: funcConfig.ReturnErrorProb}
	if errCfg.Draw(t, name+".ReturnError") {
		fn.ReturnErrorProb = 1
	} else {
		fn.ReturnErrorProb = 0
	}
	dist := funcConfig.SelfTime
	if config.Deterministic {
		dist = BiasedDurationConfig{Min: funcConfig.SelfTime.Med, Med: funcConfig.SelfTime.Med, Max: funcConfig.SelfTime.Med}
	}
	fn.Steps = append(fn.Steps, SelfTime{Dist: dist})

	if allowSubjob && config.Subjob.MaxDepth > 0 && funcConfig.Subjob.Add.Draw(t, name+".Subjob.Add") {
		subConfig := *config
		subConfig.Subjob.MaxDepth--
		const subjobPathShrinkDivisor = 2
		subConfig.Path.Length.Med = max(subConfig.Path.Length.Min, subConfig.Path.Length.Med/subjobPathShrinkDivisor)
		subPlan := newPlan(t, &subConfig, nextIDs, plan)
		// Bake a mid-flight cancellation into some subjobs: a designated
		// launcher's body cancels the subwave when it runs (structural,
		// plan-baked trigger; only the interleaving that decides what is
		// blocked at cancel time is nondeterministic). Exercises the
		// cancellation/teardown error paths. The disrupted ops may not reach
		// their sinks, so the subplan's skimmer lower bounds drop to zero.
		if len(subPlan.Launchers) > 0 &&
			(BiasedBoolConfig{Probability: config.Subjob.CancelProb}).Draw(t, name+".Subjob.Cancel") {
			k := rapid.IntRange(0, len(subPlan.Launchers)-1).Draw(t, name+".Subjob.CancelTrigger")
			subPlan.CancelTriggerRunnerID = subPlan.Launchers[k].ID
			for i := range subPlan.MinSkimmerInvocations {
				subPlan.MinSkimmerInvocations[i] = 0
			}
		}
		plan.SubjobTaskCount += len(subPlan.Launchers) + subPlan.SubjobTaskCount
		fn.Steps = append(fn.Steps, Subjob{Prob: probValue(config, 1.0), Plan: subPlan})
	}
	return fn
}

// computeMaxPathDuration returns the longest causal-time chain through
// the plan's DAG starting from any top-level StartTask. Bodies execute
// sequentially within their own scope; downstream sinks process in
// parallel (so max across downstream chains, not sum).
func computeMaxPathDuration(plan *Plan) time.Duration {
	cache := map[any]time.Duration{}
	var durationFromOp func(op any) time.Duration
	durationFromOp = func(op any) time.Duration {
		if d, ok := cache[op]; ok {
			return d
		}
		var body *Func
		switch o := op.(type) {
		case *Launcher:
			body = o.Body
		case *Funnel:
			body = o.Accumulate
		case *Skimmer:
			body = o.Handle
		}
		var bodyDur time.Duration
		var maxDownstream time.Duration
		if body != nil {
			for _, step := range body.Steps {
				bodyDur += step.Duration()
				switch s := step.(type) {
				case Submit:
					var target any
					switch s.SinkKind {
					case SinkSkimmer:
						target = plan.Skimmers[s.SinkIndex]
					case SinkFunnel:
						target = plan.Funnels[s.SinkIndex]
					}
					if d := durationFromOp(target); d > maxDownstream {
						maxDownstream = d
					}
				case StartTask:
					if d := durationFromOp(plan.Launchers[s.RunnerIndex]); d > maxDownstream {
						maxDownstream = d
					}
				}
			}
		}
		total := bodyDur + maxDownstream
		cache[op] = total
		// Memoize on the op struct's pathDuration field for Dump output.
		switch o := op.(type) {
		case *Launcher:
			o.pathDuration = total
		case *Funnel:
			o.pathDuration = total
		case *Skimmer:
			o.pathDuration = total
		}
		return total
	}

	var maxPath time.Duration
	for _, step := range plan.Steps {
		if st, ok := step.(StartTask); ok {
			if d := durationFromOp(plan.Launchers[st.RunnerIndex]); d > maxPath {
				maxPath = d
			}
		}
	}
	return maxPath
}

// Format implements fmt.Formatter for pretty-printing a plan.
func (p *Plan) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		p.Dump(f, "")
	} else {
		_, _ = fmt.Fprintf(f, "Plan#%d", p.ID)
	}
}

func (p *Plan) Dump(fs fmt.State, indent string) {
	name := fmt.Sprint(p)
	_, _ = fmt.Fprintf(fs, "%s: pathCount=%d maxPathDuration=%v",
		name, p.PathCount, p.MaxPathDuration)
	for i := range p.TaskLimiters {
		_, _ = fmt.Fprintf(fs, "\n%s   TaskLimiters[%d]: %#v", indent, i, &p.TaskLimiters[i])
	}
	for i := range p.FunnelLimiters {
		_, _ = fmt.Fprintf(fs, "\n%s   FunnelLimiters[%d]: %#v", indent, i, &p.FunnelLimiters[i])
	}
	for i, r := range p.Launchers {
		_, _ = fmt.Fprintf(fs, "\n%s   Launchers[%d]: ", indent, i)
		r.Dump(fs, indent+"     ")
	}
	for i, c := range p.Funnels {
		_, _ = fmt.Fprintf(fs, "\n%s   Funnels[%d]: ", indent, i)
		c.Dump(fs, indent+"     ")
	}
	for i, g := range p.Skimmers {
		_, _ = fmt.Fprintf(fs, "\n%s   Skimmers[%d]: ", indent, i)
		g.Dump(fs, indent+"     ")
	}
	var t time.Duration
	for i, s := range p.Steps {
		_, _ = fmt.Fprintf(fs, "\n%s%s step %d/%d (+%v): ", indent, name, i+1, len(p.Steps)+1, t)
		s.Dump(fs, indent)
		t += s.Duration()
	}
	_, _ = fmt.Fprintf(fs, "\n%s%s step %d/%d (+%v): ends at %v",
		indent, name, len(p.Steps)+1, len(p.Steps)+1, t, p.MaxPathDuration)
}
