// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
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
		planConfig.MaxPathCount = 100
		planConfig.MaxPathLength = 20
		planConfig.SubjobProbability = 0.1
		planConfig.TaskErrorProbability = 0.1
		//planConfig.GatherErrorProbability = 0.1
		minJitterEstimationCount := 100
		medJitterEstimationCount := 100
		maxJitterEstimationCount := 100
		trialCount := 100
		if testing.Short() {
			planConfig.MaxPathCount = 20
			planConfig.MaxPathLength = 10
			minJitterEstimationCount = 20
			medJitterEstimationCount = 20
			maxJitterEstimationCount = 20
			trialCount = 20
		}

		plan := sim.NewPlan(t, planConfig)
		allPlans := make([]*sim.Plan, 0, 1+plan.SubplanCount)
		allPlans = append(allPlans, plan)
		allPlans = plan.AppendSubplans(allPlans)
		minJitterExpectations := make(map[*sim.Plan]*sim.ResultRange)
		medJitterExpectations := make(map[*sim.Plan]*sim.ResultRange)
		maxJitterExpectations := make(map[*sim.Plan]*sim.ResultRange)
		for _, p := range allPlans {
			// Run simulations to generate range of expectations. Jitter ranges
			// empirically determined for test stability, YMMV.
			sim.MergeResultRangeMap(t, minJitterExpectations,
				sim.EstimateJob(t, p, minJitterEstimationCount, &sim.JobConfig{
					JitterUnit: 1 * time.Microsecond,
					MinJitter:  0,
					MaxJitter:  0,
					//Debug:      true,
				}),
			)
			sim.MergeResultRangeMap(t, medJitterExpectations,
				sim.EstimateJob(t, p, medJitterEstimationCount, &sim.JobConfig{
					JitterUnit: 10 * time.Microsecond,
					MinJitter:  0,
					MaxJitter:  100,
					//Debug:      true,
				}),
			)
			sim.MergeResultRangeMap(t, maxJitterExpectations,
				sim.EstimateJob(t, p, maxJitterEstimationCount, &sim.JobConfig{
					JitterUnit: 2000 * time.Microsecond,
					MinJitter:  0,
					MaxJitter:  100,
					//Debug:      true,
				}),
			)
		}

		expectations := make(map[*sim.Plan]*sim.ResultRange)
		sim.MergeResultRangeMap(t, expectations, minJitterExpectations)
		sim.MergeResultRangeMap(t, expectations, medJitterExpectations)
		sim.MergeResultRangeMap(t, expectations, maxJitterExpectations)

		// Adjust to account for imperfect estimation due to limited repetition
		if testing.Short() {
			for p, e := range expectations {
				for pool, limit := range p.Config.ConcurrencyLimits {
					if e.MinMaxConcurrencyByPool[pool] > 1 {
						e.MinMaxConcurrencyByPool[pool]--
					}
					// Sometimes simulation beats estimation concurrency by one.
					// Higher estimation iteration counts help, but there's still
					// always a chance.
					if e.MaxMaxConcurrencyByPool[pool] < int64(limit) {
						e.MaxMaxConcurrencyByPool[pool]++
					}
				}
			}
		}

		/*
			// Adjust to account for imperfect estimation due to limited simulation repetition
			minMaxConcurrencyTolerance := 0.2
			maxMaxConcurrencyTolerance := 0.2
			minDurationTolerance := 0.3
			maxDurationTolerance := 0.4
			if testing.Short() {
				maxDurationTolerance = 0.6
			}
			expectations.MinMaxConcurrency = max(1, int64(math.Floor((1-minMaxConcurrencyTolerance)*float64(expectations.MinMaxConcurrency))))
			expectations.MaxMaxConcurrency = int64(math.Ceil((1 + maxMaxConcurrencyTolerance) * float64(expectations.MaxMaxConcurrency)))
			expectations.MinOverallDuration = max(plan.MaxPathDuration, time.Duration(math.Floor((1-minDurationTolerance)*float64(expectations.MinOverallDuration))))
			expectations.MaxOverallDuration = time.Duration(math.Ceil((1 + maxDurationTolerance) * float64(expectations.MaxOverallDuration)))
		*/

		// Run the actual simulation
		warmUpCount := 5
		ctx := context.Background()
		chk := require.New(t)
		observations := make(map[*sim.Plan]*sim.ResultRange)
		for trial := range warmUpCount + trialCount {
			resultMap, err := sim.Run(t, ctx, plan)
			chk.NoError(err)
			if trial >= warmUpCount {
				sim.MergeResultMap(t, observations, resultMap)
			}
		}

		// Sort plans to ensure consistent reporting order
		plans := make([]*sim.Plan, 1+plan.SubplanCount)
		for plan := range expectations {
			plans[plan.ID] = plan
		}

		for _, plan := range plans {
			if plan == nil {
				continue
			}
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
					chk.GreaterOrEqual(o.MaxMaxConcurrencyByPool[p], e.MinMaxConcurrencyByPool[p])
				}
				chk.Equal(len(e.MaxMaxConcurrencyByPool), len(o.MaxMaxConcurrencyByPool))
				for p := range e.MaxMaxConcurrencyByPool {
					chk.LessOrEqual(o.MaxMaxConcurrencyByPool[p], e.MaxMaxConcurrencyByPool[p])
				}
				chk.GreaterOrEqual(o.MinOverallDuration, e.MinOverallDuration)
				chk.LessOrEqual(o.MaxOverallDuration, e.MaxOverallDuration)
			}
		}
	})
}
