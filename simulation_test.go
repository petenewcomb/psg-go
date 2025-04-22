// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/sim"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestBySimulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Build a simulation plan
		planConfig := sim.NewPlanConfig(t)

		// Empirically determined to minimize execution time while maintaining
		// test stability. YMMV.
		planConfig.MaxSubjobDepth = 3
		planConfig.MaxPathCount = 10
		planConfig.MaxPathLength = 10
		planConfig.TaskErrorProbability = 0.1
		//planConfig.GatherErrorProbability = 0.1
		minJitterEstimationCount := 30
		medJitterEstimationCount := 30
		maxJitterEstimationCount := 30
		trialCount := 3
		if testing.Short() {
			planConfig.MaxPathCount /= 2
			planConfig.MaxPathLength /= 2
			planConfig.MinTaskDuration /= 10
			planConfig.MedTaskDuration /= 10
			planConfig.MaxTaskDuration /= 100
			planConfig.MinGatherDuration /= 10
			planConfig.MedGatherDuration /= 10
			planConfig.MaxGatherDuration /= 100
			planConfig.OverallDurationBudget /= 10
		}

		debug := false

		plan := sim.NewPlan(t, planConfig)
		estimationStart := time.Now()

		// Run simulations to generate range of expectations. Jitter ranges
		// empirically determined for test stability, YMMV.
		minJitterExpectations := sim.EstimateJob(t, plan, minJitterEstimationCount, &sim.JobConfig{
			MinJitter: 1 * time.Microsecond,
			MedJitter: 20 * time.Microsecond,
			MaxJitter: 50 * time.Microsecond,
			Debug:     debug,
		})
		medJitterExpectations := sim.EstimateJob(t, plan, medJitterEstimationCount, &sim.JobConfig{
			MinJitter: 10 * time.Microsecond,
			MedJitter: 200 * time.Microsecond,
			MaxJitter: 1000 * time.Microsecond,
			Debug:     debug,
		})
		maxJitterExpectations := sim.EstimateJob(t, plan, maxJitterEstimationCount, &sim.JobConfig{
			MinJitter: 1 * time.Millisecond,
			MedJitter: 10 * time.Millisecond,
			MaxJitter: 20 * time.Millisecond,
			Debug:     debug,
		})

		t.Logf("estimation time: %v", time.Since(estimationStart))

		expectations := make(map[*sim.Plan]*sim.ResultRange)
		sim.MergeResultRangeMap(t, expectations, minJitterExpectations)
		sim.MergeResultRangeMap(t, expectations, medJitterExpectations)
		sim.MergeResultRangeMap(t, expectations, maxJitterExpectations)

		// Run the actual simulation
		simulationStart := time.Now()
		warmUpCount := 1
		ctx := context.Background()
		chk := require.New(t)
		observations := make(map[*sim.Plan]*sim.ResultRange)
		for trial := range warmUpCount + trialCount {
			resultMap, err := sim.Run(t, ctx, plan, debug)
			chk.NoError(err)
			if trial >= warmUpCount {
				sim.MergeResultMap(t, observations, resultMap)
			}
		}
		t.Logf("simulation time: %v", time.Since(simulationStart))

		// Sort plans to ensure consistent reporting order
		var plans []*sim.Plan
		for plan := range expectations {
			plans = append(plans, plan)
		}
		slices.SortFunc(plans, func(a, b *sim.Plan) int {
			return a.ID - b.ID
		})

		for _, plan := range plans {
			if plan == nil {
				continue
			}

			t.Logf("%v: %d %v %v -> [%v, %v, %v, %v]",
				plan, plan.TaskCount, plan.Config.OverallDurationBudget, plan.MaxPathDuration,
				expectations[plan].MinOverallDuration,
				observations[plan].MinOverallDuration,
				observations[plan].MaxOverallDuration,
				expectations[plan].MaxOverallDuration)

			minE := minJitterExpectations[plan]
			if minE != nil {
				t.Logf("%v expectations: %d %d %v %v (min jitter)\n", plan,
					minE.MinMaxConcurrencyByPool, minE.MaxMaxConcurrencyByPool,
					minE.MinOverallDuration, minE.MaxOverallDuration)
			}
			medE := medJitterExpectations[plan]
			if medE != nil {
				t.Logf("%v expectations: %d %d %v %v (medium jitter)\n", plan,
					medE.MinMaxConcurrencyByPool, medE.MaxMaxConcurrencyByPool,
					medE.MinOverallDuration, medE.MaxOverallDuration)
			}
			maxE := maxJitterExpectations[plan]
			if maxE != nil {
				t.Logf("%v expectations: %d %d %v %v (max jitter)\n", plan,
					maxE.MinMaxConcurrencyByPool, maxE.MaxMaxConcurrencyByPool,
					maxE.MinOverallDuration, maxE.MaxOverallDuration)
			}
			e := expectations[plan]
			chk.NotNil(e)
			t.Logf("%v expectations: %d %d %v %v (combined)\n", plan,
				e.MinMaxConcurrencyByPool, e.MaxMaxConcurrencyByPool,
				e.MinOverallDuration, e.MaxOverallDuration)

			o := observations[plan]
			if o != nil {
				t.Logf("%v observations: %d %d %v %v\n", plan,
					o.MinMaxConcurrencyByPool, o.MaxMaxConcurrencyByPool,
					o.MinOverallDuration, o.MaxOverallDuration)

				chk.Equal(len(e.MaxMaxConcurrencyByPool), len(o.MinMaxConcurrencyByPool))
				for p := range e.MinMaxConcurrencyByPool {
					chk.GreaterOrEqual(o.MaxMaxConcurrencyByPool[p], max(0, e.MinMaxConcurrencyByPool[p]-1),
						"observed peak concurrency of pool %d is less than minimum expectation", p)
				}
				chk.Equal(len(e.MaxMaxConcurrencyByPool), len(o.MaxMaxConcurrencyByPool))
				for p := range e.MaxMaxConcurrencyByPool {
					chk.LessOrEqual(o.MaxMaxConcurrencyByPool[p], e.MaxMaxConcurrencyByPool[p]+2,
						"observed peak concurrency of pool %d is greater than maximum expectation", p)
				}
				chk.GreaterOrEqual(o.MinOverallDuration, e.MinOverallDuration*75/100,
					"observed minimum duration is less than expectation")
				chk.LessOrEqual(o.MaxOverallDuration, e.MaxOverallDuration,
					"observed maximum duration is greater than expectation")
			}
		}
	})
}
