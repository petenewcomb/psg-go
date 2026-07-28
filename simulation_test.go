// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/sim"
	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestBySimulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		traceRegion := "TestBySimulation"
		defer trace.StartRegion(context.Background(), traceRegion).End()

		// Build a simulation plan
		planConfig := sim.DefaultConfig

		if testing.Short() {
			// Adjust planConfig to shorten test
			planConfig.Path.Count = sim.BiasedIntConfig{Min: 1, Med: 5, Max: 10}
			planConfig.Path.Length = sim.BiasedIntConfig{Min: 1, Med: 2, Max: 3}
		}

		// The below commented configuration lines are provided to help reduce
		// test complexity and diagnose uncovered issues.

		//nolint:gocritic // ignore commented-out code
		// planConfig.Path.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.Path.Length = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.streampool.Task.UseFunnel.Probability = 0
		// planConfig.Funnel.Flush.Probability = 0

		//nolint:gocritic // ignore commented-out code
		// planConfig.streampool.Task.Func.ReturnError.Probability = 0
		// planConfig.Funnel.Func.ReturnError.Probability = 0
		// planConfig.Skim.Func.ReturnError.Probability = 0

		//nolint:gocritic // ignore commented-out code
		// planConfig.Subjob.MaxDepth = 0
		// planConfig.streampool.Task.Func.Subjob.Add.Probability = 0
		// planConfig.Funnel.Func.Subjob.Add.Probability = 0
		// planConfig.Skim.Func.Subjob.Add.Probability = 0

		//nolint:gocritic // ignore commented-out code
		// planConfig.Funnel.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.Skim.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.TaskPool.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.TaskPool.ConcurrencyLimit = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.FunnelPool.Count = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}
		// planConfig.FunnelPool.ConcurrencyLimit = sim.BiasedIntConfig{Min: 1, Med: 1, Max: 1}

		//nolint:gocritic // ignore commented-out code
		// planConfig.Launcher.Body.SelfTime = sim.BiasedDurationConfig{}
		// planConfig.Funnel.Accumulate.SelfTime = sim.BiasedDurationConfig{}
		// planConfig.Funnel.Flush.SelfTime = sim.BiasedDurationConfig{}
		// planConfig.Skimmer.Handle.SelfTime = sim.BiasedDurationConfig{}

		plan := sim.NewPlan(t, &planConfig)
		t.Logf("Test plan:\n%#v", plan)

		// Run the actual simulation
		simulationStart := time.Now()
		ctx := context.Background()
		err := sim.Run(ctx, t, plan)
		assert.NoError(t, err)
		t.Logf("simulation time: %v", time.Since(simulationStart))
	})
}
