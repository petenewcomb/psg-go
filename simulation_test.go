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

		debug := false

		plan := sim.NewPlan(t, &planConfig)
		t.Logf("Test plan:\n%#v", plan)

		// Run the actual simulation
		simulationStart := time.Now()
		ctx := context.Background()
		chk := require.New(t)
		_, err := sim.Run(t, ctx, plan, debug)
		chk.NoError(err)
		t.Logf("simulation time: %v", time.Since(simulationStart))
	})
}
