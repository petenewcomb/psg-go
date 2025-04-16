// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"fmt"
	"time"

	"pgregory.net/rapid"
)

type Plan struct {
	ID              int
	SubplanCount    int
	Config          PlanConfig
	PathCount       int
	TaskCount       int
	MaxPathDuration time.Duration
	RootTasks       []*Task
}

type PlanConfig struct {
	ConcurrencyLimits      []int
	MaxGatherThreadCount   int
	SubjobProbability      float64
	MaxSubjobCount         int
	MaxSubjobDepth         int
	MaxPathCount           int
	MaxPathLength          int
	MinTaskDuration        time.Duration
	MaxTaskDuration        time.Duration
	TaskErrorProbability   float64
	MinGatherDuration      time.Duration
	MaxGatherDuration      time.Duration
	GatherErrorProbability float64
	OverallDurationBudget  time.Duration
}

var DefaultPlanConfig = PlanConfig{
	ConcurrencyLimits:      []int{1},
	MaxGatherThreadCount:   1,
	SubjobProbability:      0.0,
	MaxSubjobCount:         1,
	MaxSubjobDepth:         3,
	MaxPathCount:           100,
	MaxPathLength:          10,
	MinTaskDuration:        10 * time.Microsecond,
	TaskErrorProbability:   0.0,
	MaxTaskDuration:        1000 * time.Microsecond,
	MinGatherDuration:      1 * time.Microsecond,
	MaxGatherDuration:      100 * time.Microsecond,
	GatherErrorProbability: 0.0,
	OverallDurationBudget:  100 * time.Millisecond,
}

func NewPlanConfig(t *rapid.T) *PlanConfig {
	config := &PlanConfig{}
	*config = DefaultPlanConfig
	config.ConcurrencyLimits = rapid.SliceOfN(rapid.IntRange(1, 10), 1, 3).Draw(t, "concurrencyLimits")
	//config.MaxGatherThreadCount = rapid.IntRange(1, 3).Draw(t, "maxGatherThreadCount")
	return config
}

// NewPlan creates a hierarchy of simulated tasks for testing.
func NewPlan(t *rapid.T, config *PlanConfig) *Plan {
	var nextIDs idCounters
	return newPlan(t, config, &nextIDs, 0)
}

func newPlan(t *rapid.T, config *PlanConfig, nextIDs *idCounters, depth int) *Plan {
	plan := &Plan{
		ID:     nextIDs.Plan,
		Config: *config,
	}
	nextIDs.Plan++
	subplanCountOrigin := nextIDs.Plan

	config = &plan.Config // use copy from here on out

	avgConcurrencyLimit := 0
	for _, limit := range config.ConcurrencyLimits {
		avgConcurrencyLimit += limit
	}
	avgConcurrencyLimit /= len(config.ConcurrencyLimits)

	avgTaskDuration := config.MinTaskDuration + (config.MaxTaskDuration-config.MinTaskDuration)/2
	avgGatherDuration := config.MinGatherDuration + (config.MaxGatherDuration-config.MinGatherDuration)/2

	budgetedPaths := int(
		config.OverallDurationBudget * time.Duration(avgConcurrencyLimit) / (avgTaskDuration + avgGatherDuration),
	)
	plan.PathCount = rapid.IntRange(1, min(config.MaxPathCount, max(1, budgetedPaths))).Draw(t, "pathCount")

	newIntermediateChildProbability := rapid.Float64Range(0, 1).Draw(t, "newIntermediateChildProbability")
	createNewIntermediateChild := func() bool {
		return BiasedBool(newIntermediateChildProbability).Draw(t, "createNewIntermediateChild")
	}

	var rootTask Task
	for range plan.PathCount {
		pathLength := rapid.IntRange(1, config.MaxPathLength).Draw(t, "pathLength")
		var pathDuration time.Duration
		parent := &rootTask
		for step := range pathLength {
			var child *Task
			if step == pathLength-1 || len(parent.Children) == 0 || createNewIntermediateChild() {
				subjobCount := 0
				if depth < config.MaxSubjobDepth {
					for subjobCount < config.MaxSubjobCount && BiasedBool(config.SubjobProbability).Draw(t, "subjob") {
						subjobCount++
					}
				}
				timeChunkCount := 2*subjobCount + 1
				allocateSelfTime := func() time.Duration {
					return time.Duration(
						rapid.Int64Range(
							int64(config.MinTaskDuration)/int64(timeChunkCount),
							int64(config.MaxTaskDuration)/int64(timeChunkCount),
						).Draw(t, "taskTimeChunk"),
					)
				}
				child = &Task{
					ID:                    nextIDs.Task,
					Pool:                  rapid.IntRange(0, len(config.ConcurrencyLimits)-1).Draw(t, "poolIndex"),
					SelfTimes:             make([]time.Duration, subjobCount+1),
					Subjobs:               make([]*Plan, subjobCount),
					ReturnErrorFromTask:   BiasedBool(config.TaskErrorProbability).Draw(t, "returnErrorFromTask"),
					ReturnErrorFromGather: BiasedBool(config.GatherErrorProbability).Draw(t, "returnErrorFromGather"),
				}
				nextIDs.Task++
				for i := range len(child.SelfTimes) {
					child.SelfTimes[i] = allocateSelfTime()
				}
				for i := range len(child.Subjobs) {
					subjobConfig := plan.Config
					subjobConfig.MaxPathLength -= pathLength + 1
					if subjobConfig.MaxPathLength < 1 {
						subjobConfig.MaxPathLength = 1
					}
					subjobConfig.OverallDurationBudget = allocateSelfTime()
					child.Subjobs[i] = newPlan(t, &subjobConfig, nextIDs, depth+1)
				}
				parent.Children = append(parent.Children, child)
				plan.TaskCount++
			} else {
				child = rapid.SampledFrom(parent.Children).Draw(t, "child")
			}

			// Update path duration
			for _, d := range child.SelfTimes {
				pathDuration += d
			}
			for _, subjob := range child.Subjobs {
				pathDuration += subjob.Config.OverallDurationBudget
			}

			parent = child
		}
		plan.MaxPathDuration = max(plan.MaxPathDuration, pathDuration)
		plan.PathCount++
	}

	var populateGatherTimes func(task *Task)
	populateGatherTimes = func(task *Task) {
		timeChunkCount := len(task.Children) + 1
		task.GatherTimes = make([]time.Duration, timeChunkCount)
		for i := range timeChunkCount {
			task.GatherTimes[i] = time.Duration(
				rapid.Int64Range(
					int64(config.MinGatherDuration)/int64(timeChunkCount),
					int64(config.MaxGatherDuration)/int64(timeChunkCount),
				).Draw(t, "taskTimeChunk"),
			)
		}
		for _, child := range task.Children {
			populateGatherTimes(child)
		}
	}
	for _, task := range rootTask.Children {
		populateGatherTimes(task)
	}

	plan.SubplanCount = nextIDs.Plan - subplanCountOrigin
	plan.RootTasks = rootTask.Children

	t.Logf("NewPlan: %#v", plan)
	return plan
}

type idCounters struct {
	Plan int
	Task int
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
		fmt.Fprintf(f, "Plan#%d", p.ID)
	}
}

func (p *Plan) formatInternal(f fmt.State, indent string) {
	fmt.Fprintf(f, "Plan#%d: config=%+v", p.ID, p.Config)
	for _, child := range p.RootTasks {
		fmt.Fprintf(f, "\n%s", indent)
		child.formatInternal(f, indent+"  ")
	}
}
