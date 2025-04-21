// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"slices"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

type Plan struct {
	ID              int
	SubplanCount    int
	Config          PlanConfig
	PathCount       int
	TaskCount       int
	SubjobTaskCount int
	MaxPathDuration time.Duration
	RootTasks       []*Task
}

type PlanConfig struct {
	ConcurrencyLimits                  []int
	MaxGatherThreadCount               int
	SubjobProbability                  float64
	MaxSubjobCount                     int
	MaxSubjobDepth                     int
	MaxPathCount                       int
	MaxPathLength                      int
	MinNewIntermediateChildProbability float64
	MaxNewIntermediateChildProbability float64
	MinTaskDuration                    time.Duration
	MedTaskDuration                    time.Duration
	MaxTaskDuration                    time.Duration
	TaskErrorProbability               float64
	MinGatherDuration                  time.Duration
	MedGatherDuration                  time.Duration
	MaxGatherDuration                  time.Duration
	GatherErrorProbability             float64
	OverallDurationBudget              time.Duration
}

var DefaultPlanConfig = PlanConfig{
	ConcurrencyLimits:                  []int{1},
	MaxGatherThreadCount:               1,
	SubjobProbability:                  0.1,
	MaxSubjobCount:                     2,
	MaxSubjobDepth:                     3,
	MaxPathCount:                       100,
	MaxPathLength:                      10,
	MinNewIntermediateChildProbability: 0,
	MaxNewIntermediateChildProbability: 0.1,
	MinTaskDuration:                    10 * time.Microsecond,
	MedTaskDuration:                    10_000 * time.Microsecond,
	MaxTaskDuration:                    200_000 * time.Microsecond,
	TaskErrorProbability:               0.0,
	MinGatherDuration:                  10 * time.Microsecond,
	MedGatherDuration:                  100 * time.Microsecond,
	MaxGatherDuration:                  1000 * time.Microsecond,
	GatherErrorProbability:             0.0,
	OverallDurationBudget:              500 * time.Millisecond,
}

var nextIDs idCounters

func NewPlanConfig(t *rapid.T) *PlanConfig {
	config := &PlanConfig{}
	*config = DefaultPlanConfig
	config.ConcurrencyLimits = rapid.SliceOfN(
		rapid.Custom(func(t *rapid.T) int {
			mpc := 10 // max pool concurrency
			// bias toward pool size of 2
			return 2 + rapid.IntRange(-1, mpc-2).Draw(t, "concurrencyLimit")
		}), 1, 3).Draw(t, "concurrencyLimits")
	//config.MaxGatherThreadCount = rapid.IntRange(1, 3).Draw(t, "maxGatherThreadCount")
	return config
}

// NewPlan creates a hierarchy of simulated tasks for testing.
func NewPlan(t *rapid.T, config *PlanConfig) *Plan {
	plan := newPlan(t, config, &nextIDs, 0)
	t.Logf("NewPlan: %#v", plan)
	return plan
}

func newPlan(t *rapid.T, config *PlanConfig, nextIDs *idCounters, depth int) *Plan {
	plan := &Plan{
		ID:     nextIDs.Plan,
		Config: *config,
	}
	//t.Logf("NewPlanConfig: %+v", plan.Config)
	nextIDs.Plan++
	nextIDsOrigin := *nextIDs

	config = &plan.Config // use copy from here on out

	chk := require.New(t)

	var minConcurrencyLimit int
	chk.Greater(len(config.ConcurrencyLimits), 0)
	for i, limit := range config.ConcurrencyLimits {
		chk.Greater(limit, 0)
		if i == 0 || limit < minConcurrencyLimit {
			minConcurrencyLimit = limit
		}
	}

	chk.Greater(config.MedTaskDuration, time.Duration(0))
	chk.LessOrEqual(config.MinTaskDuration, config.MedTaskDuration)
	chk.LessOrEqual(config.MedTaskDuration, config.MaxTaskDuration)

	chk.Greater(config.MedGatherDuration, time.Duration(0))
	chk.LessOrEqual(config.MinGatherDuration, config.MedGatherDuration)
	chk.LessOrEqual(config.MedGatherDuration, config.MaxGatherDuration)

	minStepDuration := config.MinTaskDuration + config.MinGatherDuration
	medStepDuration := config.MedTaskDuration + config.MedGatherDuration
	maxStepDuration := config.MaxTaskDuration + config.MaxGatherDuration

	maxPathLength := int64(config.MaxPathLength)
	chk.Greater(maxPathLength, int64(0))
	if minStepDuration > 0 {
		maxPathLength = min(int64(config.OverallDurationBudget/minStepDuration), maxPathLength)
	}

	medPathLength := int64(1)
	if medStepDuration > 0 {
		medPathLength = min(int64(config.OverallDurationBudget/medStepDuration), maxPathLength)
	}
	medPathDuration := time.Duration(medPathLength) * medStepDuration

	budgetedPaths := int64(minConcurrencyLimit)
	if medPathDuration > 0 {
		budgetedPaths = max(1, min(
			int64(config.OverallDurationBudget)*int64(minConcurrencyLimit)/int64(medPathDuration),
			int64(config.MaxPathCount),
		))
	}
	plan.PathCount = int(biasedInt64(1, max(1, budgetedPaths/2), budgetedPaths).Draw(t, "pathCount"))

	newIntermediateChildProbability := rapid.Float64Range(
		config.MinNewIntermediateChildProbability, config.MaxNewIntermediateChildProbability,
	).Draw(t, "newIntermediateChildProbability")
	createNewIntermediateChild := func() bool {
		return BiasedBool(newIntermediateChildProbability).Draw(t, "createNewIntermediateChild")
	}

	newTask := func(plan *Plan, parent *Task, pathDurationBudget time.Duration) *Task {
		task := &Task{
			ID:                    nextIDs.Task,
			Pool:                  rapid.IntRange(0, len(config.ConcurrencyLimits)-1).Draw(t, "poolIndex"),
			Parent:                parent,
			ReturnErrorFromTask:   BiasedBool(config.TaskErrorProbability).Draw(t, "returnErrorFromTask"),
			ReturnErrorFromGather: BiasedBool(config.GatherErrorProbability).Draw(t, "returnErrorFromGather"),
		}
		nextIDs.Task++
		parent.Children = append(parent.Children, task)

		totalSelfTime := min(
			time.Duration(biasedInt64(
				int64(config.MinTaskDuration),
				int64(config.MedTaskDuration),
				int64(config.MaxTaskDuration),
			).Draw(t, "totalSelfTime")),
			pathDurationBudget,
		)

		task.PathDurationAtTaskEnd = parent.PathDurationAtTaskEnd + totalSelfTime

		totalGatherTime := min(
			time.Duration(biasedInt64(
				int64(config.MinGatherDuration),
				int64(config.MedGatherDuration),
				int64(config.MaxGatherDuration),
			).Draw(t, "totalGatherTime")),
			pathDurationBudget-totalSelfTime,
		)

		// Maybe add subjobs
		if config.MaxSubjobDepth > 0 {
			subjobDurationBudget := pathDurationBudget - totalSelfTime - totalGatherTime
			for subjobDurationBudget >= medStepDuration &&
				BiasedBool(config.SubjobProbability).Draw(t, "addSubjob") {
				subjobConfig := plan.Config
				subjobConfig.MaxSubjobDepth--
				subjobConfig.OverallDurationBudget = time.Duration(
					biasedInt64(
						int64(medStepDuration), int64(max(medStepDuration, subjobDurationBudget/2)), int64(subjobDurationBudget),
					).Draw(t, "subjobDurationBudget"),
				)
				subjobPlan := newPlan(t, &subjobConfig, nextIDs, depth+1)
				task.Subjobs = append(task.Subjobs, subjobPlan)
				subjobDurationBudget -= subjobPlan.MaxPathDuration
				plan.SubjobTaskCount += subjobPlan.TaskCount + subjobPlan.SubjobTaskCount
			}
		}

		// Interleave self time and subjobs
		task.SelfTimes = make([]time.Duration, len(task.Subjobs)+1)
		for i := range len(task.SelfTimes) - 1 {
			st := time.Duration(
				biasedInt64(
					0,
					int64(totalSelfTime)/int64(len(task.SelfTimes)-i),
					int64(totalSelfTime),
				).Draw(t, "selfTimeChunk"),
			)
			task.SelfTimes[i] = st
			totalSelfTime -= st
		}
		task.SelfTimes[len(task.SelfTimes)-1] = totalSelfTime

		// GatherTimes will be interleaved with Children later, after Children
		// has been populated. For now, just record the total gather time.
		task.GatherTimes = []time.Duration{totalGatherTime}

		return task
	}

	var addPath func(parent *Task, maxSteps int, pathDurationBudget time.Duration) time.Duration
	addPath = func(parent *Task, maxSteps int, pathDurationBudget time.Duration) time.Duration {
		if maxSteps <= 0 || pathDurationBudget <= 0 {
			return parent.PathDurationAtTaskEnd + parent.GatherDuration()
		}
		var child *Task
		if len(parent.Children) == 0 || pathDurationBudget <= medStepDuration || createNewIntermediateChild() {
			child = newTask(plan, parent, pathDurationBudget)
		} else {
			// Avoid rapid.SampledFrom because it will print the long-form (%#v)
			// representation of the task in the log.
			child = parent.Children[rapid.IntRange(0, len(parent.Children)-1).Draw(t, "child")]
		}
		return addPath(child, maxSteps-1, pathDurationBudget-child.TaskDuration())
	}

	var rootTask Task
	for range plan.PathCount {
		// Decide on a duration (length) for this particular path
		pathDurationBudget := time.Duration(biasedInt64(
			int64(medStepDuration),
			int64(min(maxStepDuration, config.OverallDurationBudget)),
			int64(max(maxStepDuration, config.OverallDurationBudget)),
		).Draw(t, "pathDurationBudget"))

		pathDuration := addPath(&rootTask, config.MaxPathLength, pathDurationBudget)

		plan.MaxPathDuration = max(plan.MaxPathDuration, pathDuration)
	}

	var populateChildGatherTimes func(parent *Task)
	populateChildGatherTimes = func(parent *Task) {
		for _, child := range parent.Children {
			totalGatherTime := child.GatherTimes[0]
			child.GatherTimes = slices.Grow(child.GatherTimes[:0], len(child.Children)+1)[:len(child.Children)+1]
			for i := range len(child.Children) {
				gt := time.Duration(
					biasedInt64(
						0,
						int64(totalGatherTime)/int64(len(child.GatherTimes)-i),
						int64(totalGatherTime),
					).Draw(t, "gatherTimeChunk"),
				)
				child.GatherTimes[i] = gt
				totalGatherTime -= gt
			}
			child.GatherTimes[len(child.GatherTimes)-1] = totalGatherTime
			populateChildGatherTimes(child)
		}
	}
	populateChildGatherTimes(&rootTask)

	// Create a root GatherTimes slice of the appropriate length so that
	// recalculateChildPathDurations doesn't get tripped up.
	rootTask.GatherTimes = make([]time.Duration, len(rootTask.Children)+1)

	// Recalculate accurate path durations now that the gather times have been
	// interleaved with the children.
	var recalculateChildPathDurations func(parent *Task)
	plan.MaxPathDuration = 0
	recalculateChildPathDurations = func(parent *Task) {
		if len(parent.Children) == 0 {
			plan.MaxPathDuration = max(plan.MaxPathDuration, parent.PathDurationAtTaskEnd+parent.GatherDuration())
			return
		}
		var parentGatherDuration time.Duration
		for i, child := range parent.Children {
			parentGatherDuration += parent.GatherTimes[i]
			child.PathDurationAtTaskEnd = parent.PathDurationAtTaskEnd + parentGatherDuration + child.TaskDuration()
			recalculateChildPathDurations(child)
		}
	}
	recalculateChildPathDurations(&rootTask)

	t.Logf("%v: nextIDs: %#v  nextIDsOrigin: %#v  subjobTaskCount: %d", plan, nextIDs, nextIDsOrigin, plan.SubjobTaskCount)
	plan.TaskCount = nextIDs.Task - nextIDsOrigin.Task - plan.SubjobTaskCount
	plan.SubplanCount = nextIDs.Plan - nextIDsOrigin.Plan
	plan.RootTasks = rootTask.Children

	return plan
}

type idCounters struct {
	Plan int
	Task int
}

func biasedInt64(minVal, medVal, maxVal int64) *rapid.Generator[int64] {
	if medVal < minVal || maxVal < medVal {
		panic("invalid biasedInt64 parameters")
	}
	return rapid.Custom(func(t *rapid.T) int64 {
		return medVal + rapid.Int64Range(minVal-medVal, maxVal-medVal).Draw(t, "biasedInt64")
	})
}

func (p *Plan) AppendSubplans(s []*Plan) []*Plan {
	for _, t := range p.RootTasks {
		s = p.appendTaskSubplans(s, t)
	}
	return s
}

func (p *Plan) appendTaskSubplans(s []*Plan, t *Task) []*Plan {
	for _, sp := range t.Subjobs {
		s = append(s, sp)
		s = sp.AppendSubplans(s)
	}
	for _, t := range t.Children {
		s = p.appendTaskSubplans(s, t)
	}
	return s
}

// Format implements fmt.Formatter for pretty-printing a plan.
func (p *Plan) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	if f.Flag('#') {
		p.formatInternal(f, "  ")
	} else {
		_, _ = fmt.Fprintf(f, "Plan#%d", p.ID)
	}
}

func (p *Plan) formatInternal(f fmt.State, indent string) {
	_, _ = fmt.Fprintf(f, "Plan#%d: pathCount=%d taskCount=%d maxPathDuration=%v config=%+v", p.ID, p.PathCount, p.TaskCount, p.MaxPathDuration, p.Config)
	for _, child := range p.RootTasks {
		_, _ = fmt.Fprintf(f, "\n%s", indent)
		child.formatInternal(f, indent+"  ")
	}
}
