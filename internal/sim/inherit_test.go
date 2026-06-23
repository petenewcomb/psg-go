// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"testing"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestInheritedLimiterAliasing pins the controller-level wiring: an
// inherited limiter entry aliases the parent's streampool.Limiter AND its
// concurrency tracker (split counters would each assert a subset of the
// joint topology and could miss a violation); fresh entries do not.
func TestInheritedLimiterAliasing(t *testing.T) {
	var parentWave streampool.Wave
	parentPlan := &Plan{
		TaskLimiters:   []Limiter{{ID: 1, Permits: 2, InheritFromParent: -1}},
		FunnelLimiters: []Limiter{{ID: 2, Permits: 3, InheritFromParent: -1}},
	}
	parent := newController(parentPlan, &parentWave, nil)
	parent.ensurePools()

	var childWave streampool.Wave
	childPlan := &Plan{
		TaskLimiters: []Limiter{
			{ID: 1, Permits: 2, InheritFromParent: 0},
			{ID: 7, Permits: 5, InheritFromParent: -1},
		},
		FunnelLimiters: []Limiter{{ID: 2, Permits: 3, InheritFromParent: 0}},
	}
	child := newController(childPlan, &childWave, parent)
	child.ensurePools()

	// streampool.Limiter shares state by reference; == is identity of the
	// underlying impl. (require.Equal would deep-compare and pass for
	// two distinct same-permit semaphores — too weak here.)
	if child.TaskLimiters[0] != parent.TaskLimiters[0] {
		t.Fatal("inherited task limiter must alias the parent's streampool.Limiter")
	}
	require.Same(t, parent.taskLimiterTrackers[0], child.taskLimiterTrackers[0],
		"inherited task limiter must share the parent's tracker")

	if child.TaskLimiters[1] == parent.TaskLimiters[0] {
		t.Fatal("fresh task limiter must not alias the parent's")
	}
	require.NotSame(t, parent.taskLimiterTrackers[0], child.taskLimiterTrackers[1])

	if child.FunnelLimiters[0] != parent.FunnelLimiters[0] {
		t.Fatal("inherited funnel limiter must alias the parent's streampool.Limiter")
	}
	require.Same(t, parent.funnelLimiterTrackers[0], child.funnelLimiterTrackers[0],
		"inherited funnel limiter must share the parent's tracker")
}

// twoLevelSharedLimiterPlan builds a parent plan whose single launcher
// (bound to a task limiter with the given permits) drives a subjob whose
// own launcher shares that limiter via inheritance.
func twoLevelSharedLimiterPlan(permits int) *Plan {
	subPlan := &Plan{
		ID:           1,
		TaskLimiters: []Limiter{{ID: 0, Permits: permits, InheritFromParent: 0}},
		Skimmers: []*Skimmer{{
			ID:     1,
			Handle: &Func{},
		}},
		Launchers: []*Launcher{{
			ID:             1,
			Depth:          1,
			LimiterIndexes: []int{0},
			Body: &Func{Steps: []Step{
				Submit{Prob: 1.0, SinkKind: SinkSkimmer, SinkIndex: 0},
			}},
		}},
		Steps: []Step{StartTask{Prob: 1.0, RunnerIndex: 0}},
	}
	return &Plan{
		ID:           0,
		TaskLimiters: []Limiter{{ID: 0, Permits: permits, InheritFromParent: -1}},
		Skimmers: []*Skimmer{{
			ID:     0,
			Handle: &Func{},
		}},
		Launchers: []*Launcher{{
			ID:             0,
			Depth:          2,
			LimiterIndexes: []int{0},
			Body: &Func{Steps: []Step{
				Subjob{Prob: 1.0, Plan: subPlan},
				Submit{Prob: 1.0, SinkKind: SinkSkimmer, SinkIndex: 0},
			}},
		}},
		Steps: []Step{StartTask{Prob: 1.0, RunnerIndex: 0}},
	}
}

// TestSubjobLimiterInheritanceEndToEnd runs the two-level shared-limiter
// plan at permits=2 (contention-free), exercising run→runSubjob parent
// threading, the inherited-limiter aliasing, the Subjob-step
// active-concurrency drop, and the skip-inherited assertion path.
func TestSubjobLimiterInheritanceEndToEnd(t *testing.T) {
	require.NoError(t, Run(context.Background(), t, twoLevelSharedLimiterPlan(2)))
}

// TestSharedLimiterSelfDeadlockWitness is the shared-limiter
// self-deadlock witness from docs/limiter-suspend-resume.md ("Where
// suspend fires") as a deterministic regression test: a parent body
// holds the ONLY permit of limiter L while it drives a subwave whose
// launcher shares L. The subjob-top-level dispatch must blockingAcquire
// L on the same goroutine that holds it — without the block-and-help
// suspend bracket this spins forever; with it, the parent's handle
// suspends, the subjob acquires, completes, and the parent reclaims.
func TestSharedLimiterSelfDeadlockWitness(t *testing.T) {
	require.NoError(t, Run(context.Background(), t, twoLevelSharedLimiterPlan(1)))
}
