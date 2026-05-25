// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"

	"pgregory.net/rapid"
)

// Plan is the static description of one Wave-equivalent unit of work
// in the destination streampool API. The runtime adapter executes a
// Plan against the current psg API; as reshape waves land, only the
// adapter changes.
//
// Plan is intentionally minimal in v1: each path is a linear chain
// TaskRunner → (Combiner →)* Gatherer with one Submit per body. Fan-in,
// fan-out, multi-Submit, multi-StartTask, conditional routing, and
// cross-Subjob Submit are vocabulary-supported and will enrich the
// generator in follow-up work; the static types and runtime adapter
// are sized for the full expressive range.
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
	// mode, MinSinkInvocations[i] == MaxSinkInvocations[i] for each
	// Gatherer i; in probabilistic mode the bounds may differ.
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

	// Limiters
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

	// Gatherers (terminal sinks) — generate up front; paths will Submit into them.
	gathererCount := config.Gatherer.Count.Draw(t, planName+".GathererCount")
	plan.Gatherers = make([]*Gatherer, gathererCount)
	for i := range plan.Gatherers {
		id := nextIDs.Gatherer
		nextIDs.Gatherer++
		plan.Gatherers[i] = &Gatherer{
			ID:     id,
			Depth:  0, // gatherers are terminal (depth 0 = deepest); placeholder, set during path build
			Handle: newFunc(t, plan, config, &config.Gatherer.Handle, nextIDs, fmt.Sprintf("Gatherer#%d.Handle", id)),
		}
	}
	plan.MinGathererInvocations = make([]int, gathererCount)
	plan.MaxGathererInvocations = make([]int, gathererCount)

	// PathCount paths, each a linear chain TaskRunner → (Combiner →)* Gatherer.
	plan.PathCount = config.Path.Count.Draw(t, planName+".PathCount")

	for i := range plan.PathCount {
		pathName := fmt.Sprintf("%s.Path[%d]", planName, i)
		length := config.Path.Length.Draw(t, pathName+".Length")
		// Build the chain. Pick a terminal Gatherer.
		gathererIdx := rapid.IntRange(0, gathererCount-1).Draw(t, pathName+".TerminalGatherer")
		// Current Submit target as we build backward from terminal.
		currentSinkKind := SinkGatherer
		currentSinkIndex := gathererIdx
		// Intermediate Combiners: length - 1 of them (length 1 = direct task→gatherer).
		// length == 0 isn't allowed (config Min is at least 1).
		for j := length - 1; j > 0; j-- {
			id := nextIDs.Combiner
			nextIDs.Combiner++
			combiner := &Combiner{
				ID:    id,
				Depth: j, // deeper = closer to terminal
				Accumulate: newFunc(t, plan, config, &config.Combiner.Accumulate, nextIDs,
					fmt.Sprintf("Combiner#%d.Accumulate", id)),
				Flush: newFunc(t, plan, config, &config.Combiner.Flush, nextIDs,
					fmt.Sprintf("Combiner#%d.Flush", id)),
			}
			if combLimiterCount > 0 {
				limIdx := rapid.IntRange(0, combLimiterCount-1).Draw(t, fmt.Sprintf("Combiner#%d.LimiterIndex", id))
				combiner.LimiterIndexes = []int{limIdx}
			}
			// Accumulate submits to current sink.
			combiner.Accumulate.Steps = append(combiner.Accumulate.Steps, Submit{
				Prob: probValue(config, 1.0), SinkKind: currentSinkKind, SinkIndex: currentSinkIndex,
			})
			plan.Combiners = append(plan.Combiners, combiner)
			currentSinkKind = SinkCombiner
			currentSinkIndex = len(plan.Combiners) - 1
		}
		// Origin TaskRunner: body submits to current sink.
		id := nextIDs.TaskRunner
		nextIDs.TaskRunner++
		runner := &TaskRunner{
			ID:    id,
			Depth: length, // origin is at depth = path length
			Body:  newFunc(t, plan, config, &config.TaskRunner.Body, nextIDs, fmt.Sprintf("TaskRunner#%d.Body", id)),
		}
		if taskLimiterCount > 0 {
			limIdx := rapid.IntRange(0, taskLimiterCount-1).Draw(t, fmt.Sprintf("TaskRunner#%d.LimiterIndex", id))
			runner.LimiterIndexes = []int{limIdx}
		}
		runner.Body.Steps = append(runner.Body.Steps, Submit{
			Prob: probValue(config, 1.0), SinkKind: currentSinkKind, SinkIndex: currentSinkIndex,
		})
		plan.TaskRunners = append(plan.TaskRunners, runner)

		// Top-level StartTask for this path's origin.
		plan.Steps = append(plan.Steps, StartTask{
			Prob: probValue(config, 1.0), RunnerIndex: len(plan.TaskRunners) - 1,
		})

		// Sink-invocation accounting for the terminal Gatherer.
		// In Deterministic mode, exactly one invocation per path that ends here.
		// In probabilistic mode, multiplied by cumulative path probabilities (all 1 in v1).
		plan.MinGathererInvocations[gathererIdx]++
		plan.MaxGathererInvocations[gathererIdx]++
	}

	plan.Steps = rapid.Permutation(plan.Steps).Draw(t, planName+".StepsPermutation")

	plan.MaxPathDuration = computeMaxPathDuration(plan)
	plan.SubjobCount = nextIDs.Plan - nextIDsOrigin.Plan
	// SubjobTaskCount accounting omitted in minimal v1 (no Subjobs generated by this minimal generator).

	return plan
}

// probValue returns 1.0 in Deterministic mode, else the given prob.
// (v1 generator emits only Prob=1.0 steps; this hook is here so the
// future probabilistic generator can flow Prob values through here.)
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
	// ReturnErrorProb: roll at plan time to a definite 0 or 1.
	errCfg := BiasedBoolConfig{Probability: funcConfig.ReturnErrorProb}
	if errCfg.Draw(t, name+".ReturnError") {
		fn.ReturnErrorProb = 1
	} else {
		fn.ReturnErrorProb = 0
	}
	// SelfTime: in Deterministic mode collapse to a fixed Med; otherwise
	// pass the full distribution through for per-invocation draws.
	dist := funcConfig.SelfTime
	if config.Deterministic {
		dist = BiasedDurationConfig{Min: funcConfig.SelfTime.Med, Med: funcConfig.SelfTime.Med, Max: funcConfig.SelfTime.Med}
	}
	fn.Steps = append(fn.Steps, SelfTime{Dist: dist})

	// Subjob step — probabilistic add, gated by config.Subjob.MaxDepth
	// to prevent unbounded recursion.
	if config.Subjob.MaxDepth > 0 && funcConfig.Subjob.Add.Draw(t, name+".Subjob.Add") {
		subConfig := *config
		subConfig.Subjob.MaxDepth--
		// Shrink path lengths in the subjob to keep total cost manageable.
		const subjobPathShrinkDivisor = 2
		subConfig.Path.Length.Med = max(subConfig.Path.Length.Min, subConfig.Path.Length.Med/subjobPathShrinkDivisor)
		subPlan := newPlan(t, &subConfig, nextIDs)
		plan.SubjobTaskCount += len(subPlan.TaskRunners) + subPlan.SubjobTaskCount
		fn.Steps = append(fn.Steps, Subjob{Prob: probValue(config, 1.0), Plan: subPlan})
	}
	return fn
}

// computeMaxPathDuration walks the Plan's DAG to determine the longest
// causal-time chain. In v1 (linear paths only) this is simpler than
// the full DAG case but uses the same structure for future enrichment.
func computeMaxPathDuration(plan *Plan) time.Duration {
	var maxPath time.Duration
	for _, step := range plan.Steps {
		st, ok := step.(StartTask)
		if !ok {
			continue
		}
		d := pathDurationFromRunner(plan, plan.TaskRunners[st.RunnerIndex])
		if d > maxPath {
			maxPath = d
		}
	}
	return maxPath
}

func pathDurationFromRunner(plan *Plan, runner *TaskRunner) time.Duration {
	d := funcDuration(runner.Body)
	for _, step := range runner.Body.Steps {
		s, ok := step.(Submit)
		if !ok {
			continue
		}
		switch s.SinkKind {
		case SinkCombiner:
			d += pathDurationFromCombiner(plan, plan.Combiners[s.SinkIndex])
		case SinkGatherer:
			d += funcDuration(plan.Gatherers[s.SinkIndex].Handle)
		}
	}
	runner.pathDuration = d
	return d
}

func pathDurationFromCombiner(plan *Plan, c *Combiner) time.Duration {
	d := funcDuration(c.Accumulate)
	for _, step := range c.Accumulate.Steps {
		s, ok := step.(Submit)
		if !ok {
			continue
		}
		switch s.SinkKind {
		case SinkCombiner:
			d += pathDurationFromCombiner(plan, plan.Combiners[s.SinkIndex])
		case SinkGatherer:
			d += funcDuration(plan.Gatherers[s.SinkIndex].Handle)
		}
	}
	c.pathDuration = d
	return d
}

func funcDuration(f *Func) time.Duration {
	var total time.Duration
	for _, s := range f.Steps {
		total += s.Duration()
	}
	return total
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
