// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// stepsOnlyFlowPlan hand-builds a plan whose flow scope wraps ONLY the steps
// (Plan.FlowSteps), so the scope exits with dispatched work outstanding and the
// follow-up fires asynchronously from the drain. The shape forces every carrier
// class the conservation oracle counts: dispatched tasks (StartTask → launcher
// body), skim items (submits from the launcher body, the flush body, and the
// steps-only-scoped subjob), and funnel items (launcher → funnel accumulate →
// zero-deadline flush). The subjob also exercises expectation inheritance
// probing across the boundary while the parent's scope is a steps-only one.
func stepsOnlyFlowPlan() *Plan {
	subPlan := &Plan{
		ID:                    1,
		CancelTriggerRunnerID: -1,
		Flow:                  true,
		FlowSteps:             true,
		Skimmers:              []*Skimmer{{ID: 10, Handle: &Func{}}},
		Launchers: []*Launcher{{
			ID:    11,
			Depth: 1,
			Body: &Func{Steps: []Step{
				Submit{Prob: 1.0, SinkKind: SinkSkimmer, SinkIndex: 0},
			}},
		}},
		Steps: []Step{StartTask{Prob: 1.0, RunnerIndex: 0}},
	}
	return &Plan{
		ID:                    0,
		CancelTriggerRunnerID: -1,
		Flow:                  true,
		FlowSteps:             true,
		Skimmers:              []*Skimmer{{ID: 0, Handle: &Func{}}},
		Funnels: []*Funnel{{
			ID:         1,
			Accumulate: &Func{},
			Flush: &Func{Steps: []Step{
				Submit{Prob: 1.0, SinkKind: SinkSkimmer, SinkIndex: 0},
			}},
		}},
		Launchers: []*Launcher{{
			ID:    2,
			Depth: 2,
			Body: &Func{Steps: []Step{
				Submit{Prob: 1.0, SinkKind: SinkFunnel, SinkIndex: 0},
				Subjob{Prob: 1.0, Plan: subPlan},
				Submit{Prob: 1.0, SinkKind: SinkSkimmer, SinkIndex: 0},
			}},
		}},
		Steps: []Step{
			StartTask{Prob: 1.0, RunnerIndex: 0},
			StartTask{Prob: 1.0, RunnerIndex: 0},
		},
	}
}

// TestFlowStepsOnlyScopeEndToEnd runs the steps-only-scope plan repeatedly:
// each run asserts the propagation contracts in every body, the carrier
// conservation at the (async) fire, and the fires-exactly-once nominal end
// after the drain (flow.go oracles).
func TestFlowStepsOnlyScopeEndToEnd(t *testing.T) {
	asyncBefore := flowStepsAsyncFires.Load()
	for i := 0; i < 50; i++ {
		require.NoError(t, Run(context.Background(), t, stepsOnlyFlowPlan()))
	}
	// Not vacuous: at least one scope must have exited with its fire still
	// pending — the asynchronous executor-fire path this checkpoint exists to
	// exercise. (An inline fire at scope exit is legal per run, but 150 scopes
	// per test call — parent plus two subjob runs per iteration — with async
	// task bodies outstanding cannot ALL complete before their steps return.)
	require.Greater(t, flowStepsAsyncFires.Load(), asyncBefore,
		"no steps-only scope fired asynchronously")
}
