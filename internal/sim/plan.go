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
//   - Gatherers (terminal sinks) at depth 0
//   - Combiners at depths 1..MaxCombinerDepth, each Accumulate.Submit
//     wired to a shallower sink. Multiple paths may share a Combiner
//     (fan-in).
//   - Origin TaskRunners (one per path), Body.Submit wired to a sink
//     at depth = path-length - 1. StartTask entries for these go into
//     Plan.Steps.
//   - Fan-out TaskRunners dispatched from inside Gatherer.Handle and
//     Combiner.Accumulate bodies via StartTask steps
//     (scatter-from-gather/combine, the recursive-spawn pattern).
//     Each fan-out runner Submits to a Gatherer; cycle prevention is
//     enforced by ordering (Gatherer #i's StartTasks only target
//     Gatherers with index > i).
//
// Sink-invocation bounds and path durations are computed by walking
// the DAG forward from each top-level StartTask, memoized per op.
type Plan struct {
	ID               int
	PathCount        int
	Steps            []Step // top-level: StartTask, possibly Subjob
	MaxPathDuration  time.Duration
	TaskLimiters     []Limiter
	CombinerLimiters []Limiter
	TaskRunners      []*TaskRunner
	Combiners        []*Combiner
	Gatherers        []*Gatherer
	SubjobCount      int
	SubjobTaskCount  int
	// Sink-invocation bounds computed at plan time. In Deterministic
	// mode and the current v1 generator (all Probs = 1.0),
	// MinGathererInvocations[i] == MaxGathererInvocations[i]. When
	// probabilistic-mode generation lands, Min may drop to 0 for
	// non-unit probabilities.
	MinGathererInvocations []int
	MaxGathererInvocations []int
}

// NewPlan generates a new Plan for property-based testing.
func NewPlan(t *rapid.T, config *Config) *Plan {
	var nextIDs idCounters
	return newPlan(t, config, &nextIDs)
}

type idCounters struct {
	Plan        int
	TaskLimiter int
	CombLimiter int
	TaskRunner  int
	Combiner    int
	Gatherer    int
}

// The generator is dense by nature; refactoring into helpers obscures
// the path-construction flow.
//
//nolint:gocognit,funlen // see above
func newPlan(t *rapid.T, config *Config, nextIDs *idCounters) *Plan {
	planID := nextIDs.Plan
	nextIDs.Plan++
	planName := fmt.Sprintf("Plan#%d", planID)
	plan := &Plan{ID: planID}

	nextIDsOrigin := *nextIDs

	// === Limiters ===
	taskLimiterCount := config.TaskLimiter.Count.Draw(t, planName+".TaskLimiterCount")
	plan.TaskLimiters = make([]Limiter, taskLimiterCount)
	for i := range plan.TaskLimiters {
		id := nextIDs.TaskLimiter
		nextIDs.TaskLimiter++
		plan.TaskLimiters[i] = Limiter{
			ID:      id,
			Permits: config.TaskLimiter.Permits.Draw(t, fmt.Sprintf("TaskLimiter#%d.Permits", id)),
		}
	}
	combLimiterCount := config.CombinerLimiter.Count.Draw(t, planName+".CombinerLimiterCount")
	plan.CombinerLimiters = make([]Limiter, combLimiterCount)
	for i := range plan.CombinerLimiters {
		id := nextIDs.CombLimiter
		nextIDs.CombLimiter++
		plan.CombinerLimiters[i] = Limiter{
			ID:      id,
			Permits: config.CombinerLimiter.Permits.Draw(t, fmt.Sprintf("CombinerLimiter#%d.Permits", id)),
		}
	}

	// === Gatherers (depth 0, terminal sinks) ===
	gathererCount := config.Gatherer.Count.Draw(t, planName+".GathererCount")
	plan.Gatherers = make([]*Gatherer, gathererCount)
	for i := range plan.Gatherers {
		id := nextIDs.Gatherer
		nextIDs.Gatherer++
		plan.Gatherers[i] = &Gatherer{
			ID:     id,
			Depth:  0,
			Handle: newFunc(t, plan, config, &config.Gatherer.Handle, nextIDs, fmt.Sprintf("Gatherer#%d.Handle", id)),
		}
	}

	// === Combiners (depth 1..maxCombinerDepth, shared across paths for fan-in) ===
	maxCombinerDepth := config.Path.Length.Max - 1
	if maxCombinerDepth < 1 {
		maxCombinerDepth = 1
	}
	combinerCount := config.Combiner.Count.Draw(t, planName+".CombinerCount")
	plan.Combiners = make([]*Combiner, combinerCount)
	for i := range plan.Combiners {
		id := nextIDs.Combiner
		nextIDs.Combiner++
		depth := rapid.IntRange(1, maxCombinerDepth).Draw(t, fmt.Sprintf("Combiner#%d.Depth", id))
		plan.Combiners[i] = &Combiner{
			ID:    id,
			Depth: depth,
			Accumulate: newFunc(t, plan, config, &config.Combiner.Accumulate, nextIDs,
				fmt.Sprintf("Combiner#%d.Accumulate", id)),
			Flush: newFunc(t, plan, config, &config.Combiner.Flush, nextIDs,
				fmt.Sprintf("Combiner#%d.Flush", id)),
		}
		if combLimiterCount > 0 {
			limIdx := rapid.IntRange(0, combLimiterCount-1).Draw(t, fmt.Sprintf("Combiner#%d.LimiterIndex", id))
			plan.Combiners[i].LimiterIndexes = []int{limIdx}
		}
	}
	// Sort by depth ascending so earlier-indexed Combiners are shallower —
	// enables the wiring loop below to pick downstream candidates by index.
	sort.SliceStable(plan.Combiners, func(i, j int) bool {
		return plan.Combiners[i].Depth < plan.Combiners[j].Depth
	})

	// Wire each Combiner.Accumulate.Submit to a shallower sink
	// (Gatherer or Combiner with strictly smaller Depth).
	type sinkRef struct {
		kind SinkKind
		idx  int
	}
	for i, c := range plan.Combiners {
		var sinks []sinkRef
		for gi := range plan.Gatherers {
			sinks = append(sinks, sinkRef{SinkGatherer, gi})
		}
		for ci := 0; ci < i; ci++ {
			if plan.Combiners[ci].Depth < c.Depth {
				sinks = append(sinks, sinkRef{SinkCombiner, ci})
			}
		}
		pick := rapid.IntRange(0, len(sinks)-1).Draw(t,
			fmt.Sprintf("Combiner#%d.Accumulate.SubmitTarget", c.ID))
		c.Accumulate.Steps = append(c.Accumulate.Steps, Submit{
			Prob:      probValue(config, 1.0),
			SinkKind:  sinks[pick].kind,
			SinkIndex: sinks[pick].idx,
		})
	}

	// === Scatter-from-gather and scatter-from-combine: bodies dispatch
	// fan-out runners that all Submit to the "terminal" Gatherer
	// (Gatherers[len-1]). The terminal Gatherer has no StartTasks of its
	// own — this breaks the otherwise-multiplicative cascade that would
	// otherwise blow up when Gatherer chains compound. The terminal
	// can still be a path destination; it just doesn't propagate.
	terminalGathererIdx := len(plan.Gatherers) - 1
	addFanoutRunner := func(name string) int {
		id := nextIDs.TaskRunner
		nextIDs.TaskRunner++
		runner := &TaskRunner{
			ID:    id,
			Depth: 1,
			Body:  newFunc(t, plan, config, &config.TaskRunner.Body, nextIDs, name),
		}
		if taskLimiterCount > 0 {
			limIdx := rapid.IntRange(0, taskLimiterCount-1).Draw(t, name+".LimiterIndex")
			runner.LimiterIndexes = []int{limIdx}
		}
		runner.Body.Steps = append(runner.Body.Steps, Submit{
			Prob:      probValue(config, 1.0),
			SinkKind:  SinkGatherer,
			SinkIndex: terminalGathererIdx,
		})
		plan.TaskRunners = append(plan.TaskRunners, runner)
		return len(plan.TaskRunners) - 1
	}
	// Only non-terminal Gatherers get StartTasks. The terminal IS the
	// last Gatherer; for the degenerate case of only one Gatherer there
	// is nowhere safe to scatter to without re-introducing a cycle, so
	// skip scatter-from-gather entirely.
	const minGatherersForScatter = 2
	if len(plan.Gatherers) >= minGatherersForScatter {
		for gIdx, g := range plan.Gatherers {
			if gIdx == terminalGathererIdx {
				continue
			}
			sc := config.Gatherer.ScatterCount.Draw(t, fmt.Sprintf("Gatherer#%d.ScatterCount", g.ID))
			for s := 0; s < sc; s++ {
				runnerIdx := addFanoutRunner(fmt.Sprintf("FanoutRunner.from-Gatherer#%d[%d]", g.ID, s))
				g.Handle.Steps = append(g.Handle.Steps, StartTask{
					Prob:        probValue(config, 1.0),
					RunnerIndex: runnerIdx,
				})
			}
		}
	}
	for _, c := range plan.Combiners {
		if len(plan.Gatherers) == 0 {
			continue
		}
		sc := config.Combiner.ScatterCount.Draw(t, fmt.Sprintf("Combiner#%d.ScatterCount", c.ID))
		for s := 0; s < sc; s++ {
			runnerIdx := addFanoutRunner(fmt.Sprintf("FanoutRunner.from-Combiner#%d[%d]", c.ID, s))
			c.Accumulate.Steps = append(c.Accumulate.Steps, StartTask{
				Prob:        probValue(config, 1.0),
				RunnerIndex: runnerIdx,
			})
		}
	}

	// === Origin TaskRunners (one per path). Each path's Body.Submit
	// targets a sink at depth = pathLength - 1. Multiple paths may
	// share the same target — that's fan-in. ===
	plan.PathCount = config.Path.Count.Draw(t, planName+".PathCount")
	for i := 0; i < plan.PathCount; i++ {
		pathName := fmt.Sprintf("%s.Path[%d]", planName, i)
		length := config.Path.Length.Draw(t, pathName+".Length")

		var pickedSinkKind SinkKind
		var pickedSinkIdx int
		if length == 1 {
			pickedSinkKind = SinkGatherer
			pickedSinkIdx = rapid.IntRange(0, len(plan.Gatherers)-1).Draw(t, pathName+".TerminalGatherer")
		} else {
			// Try to find a Combiner exactly at depth length-1
			var candidates []int
			for ci, c := range plan.Combiners {
				if c.Depth == length-1 {
					candidates = append(candidates, ci)
				}
			}
			switch {
			case len(candidates) > 0:
				pickedSinkKind = SinkCombiner
				pickedSinkIdx = candidates[rapid.IntRange(0, len(candidates)-1).Draw(t, pathName+".SinkPick")]
			case len(plan.Combiners) > 0:
				// Fallback: any Combiner
				pickedSinkKind = SinkCombiner
				pickedSinkIdx = rapid.IntRange(0, len(plan.Combiners)-1).Draw(t, pathName+".SinkPick")
			default:
				// No Combiners — target a Gatherer
				pickedSinkKind = SinkGatherer
				pickedSinkIdx = rapid.IntRange(0, len(plan.Gatherers)-1).Draw(t, pathName+".SinkPick")
			}
		}

		id := nextIDs.TaskRunner
		nextIDs.TaskRunner++
		runner := &TaskRunner{
			ID:    id,
			Depth: length,
			Body:  newFunc(t, plan, config, &config.TaskRunner.Body, nextIDs, fmt.Sprintf("TaskRunner#%d.Body", id)),
		}
		if taskLimiterCount > 0 {
			limIdx := rapid.IntRange(0, taskLimiterCount-1).Draw(t, fmt.Sprintf("TaskRunner#%d.LimiterIndex", id))
			runner.LimiterIndexes = []int{limIdx}
		}
		runner.Body.Steps = append(runner.Body.Steps, Submit{
			Prob:      probValue(config, 1.0),
			SinkKind:  pickedSinkKind,
			SinkIndex: pickedSinkIdx,
		})
		plan.TaskRunners = append(plan.TaskRunners, runner)
		plan.Steps = append(plan.Steps, StartTask{
			Prob:        probValue(config, 1.0),
			RunnerIndex: len(plan.TaskRunners) - 1,
		})
	}
	plan.Steps = rapid.Permutation(plan.Steps).Draw(t, planName+".StepsPermutation")

	// === Compute sink-invocation bounds and path durations via DAG walk. ===
	plan.MinGathererInvocations = make([]int, len(plan.Gatherers))
	plan.MaxGathererInvocations = make([]int, len(plan.Gatherers))
	contribCache := map[any]map[int]int{}
	gathererIdx := map[*Gatherer]int{}
	for gi, g := range plan.Gatherers {
		gathererIdx[g] = gi
	}
	var contribOf func(op any) map[int]int
	contribOf = func(op any) map[int]int {
		if c, ok := contribCache[op]; ok {
			return c
		}
		contrib := map[int]int{}
		var body *Func
		switch o := op.(type) {
		case *TaskRunner:
			body = o.Body
		case *Combiner:
			body = o.Accumulate
		case *Gatherer:
			body = o.Handle
			contrib[gathererIdx[o]] = 1
		}
		if body != nil {
			for _, step := range body.Steps {
				switch s := step.(type) {
				case Submit:
					var target any
					switch s.SinkKind {
					case SinkGatherer:
						target = plan.Gatherers[s.SinkIndex]
					case SinkCombiner:
						target = plan.Combiners[s.SinkIndex]
					}
					for gi, cnt := range contribOf(target) {
						contrib[gi] += cnt
					}
				case StartTask:
					for gi, cnt := range contribOf(plan.TaskRunners[s.RunnerIndex]) {
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
		for gi, cnt := range contribOf(plan.TaskRunners[st.RunnerIndex]) {
			plan.MinGathererInvocations[gi] += cnt
			plan.MaxGathererInvocations[gi] += cnt
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
func newFunc(
	t *rapid.T, plan *Plan, config *Config, funcConfig *FuncConfig, nextIDs *idCounters, name string,
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

	if config.Subjob.MaxDepth > 0 && funcConfig.Subjob.Add.Draw(t, name+".Subjob.Add") {
		subConfig := *config
		subConfig.Subjob.MaxDepth--
		const subjobPathShrinkDivisor = 2
		subConfig.Path.Length.Med = max(subConfig.Path.Length.Min, subConfig.Path.Length.Med/subjobPathShrinkDivisor)
		subPlan := newPlan(t, &subConfig, nextIDs)
		plan.SubjobTaskCount += len(subPlan.TaskRunners) + subPlan.SubjobTaskCount
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
		case *TaskRunner:
			body = o.Body
		case *Combiner:
			body = o.Accumulate
		case *Gatherer:
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
					case SinkGatherer:
						target = plan.Gatherers[s.SinkIndex]
					case SinkCombiner:
						target = plan.Combiners[s.SinkIndex]
					}
					if d := durationFromOp(target); d > maxDownstream {
						maxDownstream = d
					}
				case StartTask:
					if d := durationFromOp(plan.TaskRunners[s.RunnerIndex]); d > maxDownstream {
						maxDownstream = d
					}
				}
			}
		}
		total := bodyDur + maxDownstream
		cache[op] = total
		// Memoize on the op struct's pathDuration field for Dump output.
		switch o := op.(type) {
		case *TaskRunner:
			o.pathDuration = total
		case *Combiner:
			o.pathDuration = total
		case *Gatherer:
			o.pathDuration = total
		}
		return total
	}

	var maxPath time.Duration
	for _, step := range plan.Steps {
		if st, ok := step.(StartTask); ok {
			if d := durationFromOp(plan.TaskRunners[st.RunnerIndex]); d > maxPath {
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
	for i := range p.CombinerLimiters {
		_, _ = fmt.Fprintf(fs, "\n%s   CombinerLimiters[%d]: %#v", indent, i, &p.CombinerLimiters[i])
	}
	for i, r := range p.TaskRunners {
		_, _ = fmt.Fprintf(fs, "\n%s   TaskRunners[%d]: ", indent, i)
		r.Dump(fs, indent+"     ")
	}
	for i, c := range p.Combiners {
		_, _ = fmt.Fprintf(fs, "\n%s   Combiners[%d]: ", indent, i)
		c.Dump(fs, indent+"     ")
	}
	for i, g := range p.Gatherers {
		_, _ = fmt.Fprintf(fs, "\n%s   Gatherers[%d]: ", indent, i)
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
