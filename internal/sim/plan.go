// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"slices"
	"time"

	"pgregory.net/rapid"
)

type Plan struct {
	ID              int
	Pools           []Pool
	GatherCount     int
	Combiners       []Combiner
	Steps           []Step
	SubjobCount     int
	SubjobTaskCount int
	PathCount       int
	TaskCount       int
	MaxPathDuration time.Duration
}

// NewPlan creates a hierarchy of simulated tasks for testing.
func NewPlan(t *rapid.T, config *Config) *Plan {
	var nextIDs idCounters
	return newPlan(t, config, &nextIDs)
}

type idCounters struct {
	Plan     int
	Pool     int
	Combiner int
	Task     int
}

func newPlan(t *rapid.T, planConfig *Config, nextIDs *idCounters) *Plan {
	planID := nextIDs.Plan
	nextIDs.Plan++
	planName := fmt.Sprintf("Plan#%d", planID)
	plan := &Plan{
		ID: planID,
	}

	nextIDsOrigin := *nextIDs

	poolConfig := &planConfig.Pool
	plan.Pools = make([]Pool, poolConfig.Count.Draw(t, planName+".PoolCount"))
	for i := range plan.Pools {
		poolID := nextIDs.Pool
		nextIDs.Pool++
		poolName := fmt.Sprintf("Pool#%d", poolID)
		plan.Pools[i] = Pool{
			ID:               poolID,
			ConcurrencyLimit: poolConfig.ConcurrencyLimit.Draw(t, poolName+".ConcurrencyLimit"),
		}
	}

	plan.GatherCount = planConfig.Gather.Count.Draw(t, planName+".GatherCount")

	combinerConfig := &planConfig.Combiner
	plan.Combiners = make([]Combiner, combinerConfig.Count.Draw(t, planName+".CombinerCount"))
	for i := range plan.Combiners {
		combinerID := nextIDs.Combiner
		nextIDs.Combiner++
		combinerName := fmt.Sprintf("Combiner#%d", combinerID)
		plan.Combiners[i] = Combiner{
			ID:               combinerID,
			ConcurrencyLimit: combinerConfig.ConcurrencyLimit.Draw(t, combinerName+".ConcurrencyLimit"),
		}
	}

	plan.PathCount = planConfig.Path.Count.Draw(t, planName+".PathCount")
	t.Logf("%s: pathCount=%d", planName, plan.PathCount)
	paths := make([]*Path, plan.PathCount)

	newFunc := func(name string, funcConfig *FuncConfig, scatters []*Path) *Func {
		fn := &Func{
			ReturnError: funcConfig.ReturnError.Draw(t, name+".ReturnError"),
		}

		var subjobPlan *Plan
		if planConfig.Subjob.MaxDepth > 0 && funcConfig.Subjob.Add.Draw(t, name+".Subjob.Add") {
			subjobConfig := *planConfig
			subjobConfig.Subjob.MaxDepth--
			subjobConfig.Path.Length.Med = max(subjobConfig.Path.Length.Min, subjobConfig.Path.Length.Med/planConfig.Subjob.MaxDepth)
			subjobPlan = newPlan(t, &subjobConfig, nextIDs)
			plan.SubjobTaskCount += subjobPlan.TaskCount + subjobPlan.SubjobTaskCount
		}

		selfTime := funcConfig.SelfTime.Draw(t, name+".SelfTime")

		stepCount := 2*len(scatters) + 1
		if subjobPlan != nil {
			stepCount += 2
		}
		fn.Steps = make([]Step, 0, stepCount)

		if subjobPlan != nil {
			fn.Steps = append(fn.Steps, Subjob{Plan: subjobPlan})
		}
		for _, s := range scatters {
			fn.Steps = append(fn.Steps, Scatter{Task: s.RootTask})
		}
		permutedSteps := rapid.Permutation(fn.Steps).Draw(t, name+".StepsPermutation")

		remainingSelfTimeDuration := selfTime
		remainingSelfTimeChunks := len(permutedSteps) + 1
		fn.Steps = fn.Steps[:0]
		for i := range stepCount {
			var step Step
			if i%2 == 0 {
				var stepTime time.Duration
				if remainingSelfTimeChunks == 1 {
					stepTime = remainingSelfTimeDuration
				} else {
					c := BiasedDurationConfig{
						Med: remainingSelfTimeDuration / time.Duration(remainingSelfTimeChunks),
						Max: remainingSelfTimeDuration,
					}
					stepTime = c.Draw(t, fmt.Sprintf("%s.Step[%d].SelfTime", name, i))
				}
				remainingSelfTimeChunks--
				remainingSelfTimeDuration -= stepTime
				step = SelfTime(stepTime)
			} else {
				step = permutedSteps[i/2]
			}
			fn.Steps = append(fn.Steps, step)
		}

		return fn
	}

	newTaskID := func() int {
		id := nextIDs.Task
		nextIDs.Task++
		return id
	}

	newTask := func(id int, resultHandler ResultHandler) *Task {
		taskName := fmt.Sprintf("Task#%d", id)
		return &Task{
			ID:            id,
			PoolIndex:     rapid.IntRange(0, len(plan.Pools)-1).Draw(t, taskName+".PoolIndex"),
			Func:          newFunc(taskName, &planConfig.Task.Func, nil),
			ResultHandler: resultHandler,
		}
	}

	newGather := func(id int, paths []*Path) *Gather {
		gatherName := fmt.Sprintf("Gather#%d", id)
		return &Gather{
			ID:          id,
			GatherIndex: rapid.IntRange(0, plan.GatherCount-1).Draw(t, gatherName+".GatherIndex"),
			Func:        newFunc(gatherName, &planConfig.Gather.Func, paths),
		}
	}

	newCombine := func(id int, paths []*Path) *Combine {
		combineName := fmt.Sprintf("Combine#%d", id)
		var flush ResultHandler
		if planConfig.Combine.Flush.Draw(t, combineName+".Flush") {
			flushScatterCount := (&BiasedIntConfig{Med: len(paths) / 2, Max: len(paths)}).Draw(t, combineName+".FlushScatterCount")
			flush = newGather(id, paths[:flushScatterCount])
			paths = paths[flushScatterCount:]
		}
		return &Combine{
			ID:            id,
			CombinerIndex: rapid.IntRange(0, len(plan.Combiners)-1).Draw(t, combineName+".CombinerIndex"),
			Func:          newFunc(combineName, &planConfig.Combine.Func, paths),
			FlushHandler:  flush,
		}
	}

	newGatherTask := func(id int, paths []*Path) *Task {
		return newTask(id, newGather(id, paths))
	}

	newCombineTask := func(id int, paths []*Path) *Task {
		return newTask(id, newCombine(id, paths))
	}

	// Collect the next set of paths to be scattered from a new parent task's
	// gather or combine function. Returns the new parent task and the size of
	// the set.
	pathGroupID := 0
	nextGroupTask := func(availablePaths []*Path) (*Task, []*Path) {
		id := newTaskID()
		t.Logf("%s: nextGroupTask", planName)
		useCombine := planConfig.Task.UseCombine.Draw(t, fmt.Sprintf("Task#%d.UseCombine", id))
		t.Logf("%s: nextGroupTask useCombine=%v", planName, useCombine)
		var sizeConfig BiasedIntConfig
		var newGroupTask func(int, []*Path) *Task
		if useCombine {
			sizeConfig = planConfig.Combine.ScatterCount
			newGroupTask = newCombineTask
		} else {
			sizeConfig = planConfig.Gather.ScatterCount
			newGroupTask = newGatherTask
		}
		size := 0
		if len(availablePaths) > 0 {
			sizeConfig.Min = max(1, min(sizeConfig.Min, len(availablePaths)))
			sizeConfig.Med = max(1, min(sizeConfig.Med, len(availablePaths)))
			sizeConfig.Max = max(1, min(sizeConfig.Max, len(availablePaths)))
			size = sizeConfig.Draw(t, fmt.Sprintf("%s.PathGroup#%d.Size", planName, pathGroupID))
		}
		group := availablePaths[:size]
		task := newGroupTask(id, group)
		t.Logf("%v -> %v", task, group)
		t.Logf("%#v", task)
		return task, availablePaths[size:]
	}

	t.Logf("%s: making initial paths", planName)

	// Initialize paths with leaf tasks, will build from leaves to roots
	for i := range paths {
		t.Logf("%s: making path %d", planName, i)
		pathName := fmt.Sprintf("%s.Path[%d]", planName, i)
		leafTask, _ := nextGroupTask(nil)
		paths[i] = &Path{
			RemainingLength: planConfig.Path.Length.Draw(t, pathName+".Length"),
			RootTask:        leafTask,
		}
	}

	t.Logf("%s: building path tree", planName)

	// Build the path tree from the leaves to the root by repeatedly grouping
	// some of the paths with the most growth remaining into a gather or combine
	// until none need further growth.
	nextPermutationNumber := 0
	for len(paths) > 0 {
		// Put the longest remaining lengths at the end
		slices.SortStableFunc(paths, func(a, b *Path) int {
			return a.RemainingLength - b.RemainingLength
		})

		t.Logf("paths = %v", paths)

		// Find the set of paths that share the longest remaining length
		last := len(paths) - 1
		first := last - 1
		remainingLength := paths[last].RemainingLength
		for first >= 0 && paths[first].RemainingLength == remainingLength {
			first--
		}
		first++ // Will always have gone back one too far

		// Permute the set of paths that share the longest remaining length
		t.Logf("permuting candidates starting at index %d of %d: %d", first, len(paths), nextPermutationNumber)
		permutationGenerator := rapid.Permutation(paths[first:])
		t.Logf("got permutationGenerator: %d", nextPermutationNumber)
		permutedCandidates := permutationGenerator.Draw(t, fmt.Sprintf("%s.PathsPermutation[%d]", planName, nextPermutationNumber))
		t.Logf("permuted candidates: %d", nextPermutationNumber)
		nextPermutationNumber++

		// Create a task with a gather or combine that will scatter some number
		// of tasks from the end of the list of paths.
		task, remainingCandidates := nextGroupTask(permutedCandidates)

		remainingLength--
		if remainingLength == 0 {
			// This set of paths is complete, add the new task to the plan's
			// root-level steps
			paths = paths[:len(paths)-len(permutedCandidates)]
			plan.Steps = append(plan.Steps, Scatter{Task: task})
		} else {
			// Collapse the group down to one element and update it to hold the
			// new task
			paths = append(paths[:len(paths)-len(permutedCandidates)], &Path{
				RemainingLength: remainingLength,
				RootTask:        task,
			})
		}
		paths = append(paths, remainingCandidates...)
	}

	plan.Steps = rapid.Permutation(plan.Steps).Draw(t, planName+".StepsPermutation")

	var setPathDurations func(pathDuration time.Duration, steps []Step) time.Duration
	setPathDurations = func(pathDuration time.Duration, steps []Step) time.Duration {
		for _, step := range steps {
			switch step := step.(type) {
			case SelfTime:
				// Nothing special to do
			case Subjob:
				// Nothing special to do
			case Scatter:
				step.Task.pathDuration = setPathDurations(pathDuration, step.Task.Func.Steps)
				switch rh := step.Task.ResultHandler.(type) {
				case *Gather:
					rh.pathDuration = setPathDurations(step.Task.pathDuration, rh.Func.Steps)
				case *Combine:
					rh.pathDuration = setPathDurations(step.Task.pathDuration, rh.Func.Steps)
				default:
					panic(fmt.Sprintf("unknown ResultHandler type: %T", step))
				}
			default:
				panic(fmt.Sprintf("unknown Step type: %T", step))
			}
			pathDuration += step.Duration()
		}
		if pathDuration > plan.MaxPathDuration {
			plan.MaxPathDuration = pathDuration
		}
		return pathDuration
	}
	setPathDurations(0, plan.Steps)

	plan.TaskCount = nextIDs.Task - nextIDsOrigin.Task - plan.SubjobTaskCount
	plan.SubjobCount = nextIDs.Plan - nextIDsOrigin.Plan

	return plan
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
	_, _ = fmt.Fprintf(fs, "%s: pathCount=%d taskCount=%d maxPathDuration=%v", name, p.PathCount, p.TaskCount, p.MaxPathDuration)
	var t time.Duration
	for i, s := range p.Steps {
		_, _ = fmt.Fprintf(fs, "\n%s%s step %d/%d (+%v): ", indent, name, i+1, len(p.Steps)+1, t)
		s.Dump(fs, indent)
		t += s.Duration()
	}
	_, _ = fmt.Fprintf(fs, "\n%s%s step %d/%d (+%v): ends at %v", indent, name, len(p.Steps)+1, len(p.Steps)+1, t, p.MaxPathDuration)
}
