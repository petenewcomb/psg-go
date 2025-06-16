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
		planConfig := sim.DefaultConfig

		if testing.Short() {
			// Adjust planConfig to shorten test
			planConfig.Path.Count = sim.BiasedIntConfig{Min: 1, Med: 5, Max: 10}
			planConfig.Path.Length = sim.BiasedIntConfig{Min: 1, Med: 2, Max: 3}
		}

		// This flag and the below commented configuration lines are provided to
		// help reduce test complexity and diagnose uncovered issues.
		debug := false

		//planConfig.Path.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.Path.Length = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.Task.UseCombine.Probability = 0
		//planConfig.Combine.Flush.Probability = 0

		//planConfig.Subjob.MaxDepth = 0
		//planConfig.Task.Func.Subjob.Add.Probability = 0
		//planConfig.Combine.Func.Subjob.Add.Probability = 0
		//planConfig.Gather.Func.Subjob.Add.Probability = 0

		//planConfig.Combine.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.Gather.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.TaskPool.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.TaskPool.ConcurrencyLimit = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.CombinerPool.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		//planConfig.CombinerPool.ConcurrencyLimit = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}

		//planConfig.Task.Func.SelfTime = sim.BiasedDurationConfig{}
		//planConfig.Combine.Func.SelfTime = sim.BiasedDurationConfig{}
		//planConfig.Gather.Func.SelfTime = sim.BiasedDurationConfig{}

		plan := sim.NewPlan(t, &planConfig)
		t.Logf("Test plan:\n%#v", plan)

		// Run the actual simulation
		simulationStart := time.Now()
		ctx := context.Background()
		chk := require.New(t)
		err := sim.Run(ctx, t, plan, debug)
		chk.NoError(err)
		t.Logf("simulation time: %v", time.Since(simulationStart))
	})
}
