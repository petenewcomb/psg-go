// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"cmp"
	"slices"
	"time"

	"github.com/addrummond/heap"
	"github.com/gammazero/deque"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

type JobConfig struct {
	JitterUnit time.Duration
	MinJitter  int
	MaxJitter  int
	Debug      bool
}

func EstimateJob(t *rapid.T, plan *Plan, trialCount int, config *JobConfig) map[*Plan]*ResultRange {
	em := make(map[*Plan]*ResultRange)
	for range trialCount {
		MergeResultMap(t, em, estimateJob(t, plan, config, 0, ""))
	}
	return em
}

func estimateJob(t *rapid.T, plan *Plan, config *JobConfig, simTimeOrigin time.Duration, indent string) map[*Plan]*Result {
	simTime := simTimeOrigin
	chk := require.New(t)

	estimates := make(map[*Plan]*Result)
	var eventHeap heap.Heap[taskEvent, heap.Min]

	if config.MinJitter < 0 {
		panic("MinJitter may not be less than zero")
	}
	if config.MaxJitter < config.MinJitter {
		panic("MaxJitter may not be less than MinJitter")
	}
	if config.JitterUnit < 0 {
		panic("JitterUnit may not be less than zero")
	}

	jitter := func() time.Duration {
		return config.JitterUnit*time.Duration(rapid.IntRange(config.MinJitter, config.MaxJitter-1).Draw(t, "jitterUnits")) +
			time.Duration(rapid.Int64Range(0, int64(config.JitterUnit)).Draw(t, "jitterNoise"))
	}
	if config.MaxJitter == 0 || config.JitterUnit == 0 {
		jitter = func() time.Duration {
			return 0
		}
	}

	poolCount := len(plan.Config.ConcurrencyLimits)
	waitersByPool := make([][]func(), poolCount)
	concurrencyByPool := make([]int, poolCount)
	maxConcurrencyByPool := make([]int64, poolCount)

	var startTask func(task *Task)
	var scatterTask func(task *Task, then func())
	scatterTask = func(task *Task, then func()) {
		pool := task.Pool
		concurrency := &concurrencyByPool[pool]
		if *concurrency < plan.Config.ConcurrencyLimits[pool] {
			*concurrency++
			maxConcurrencyByPool[pool] = max(maxConcurrencyByPool[pool], int64(*concurrency))
			if config.Debug {
				t.Logf("%v%s %v launching on pool %d (concurrency now %d)", simTime, indent, task, pool, *concurrency)
			}
			startTask(task)
			then()
		} else {
			waiters := &waitersByPool[pool]
			if config.Debug {
				t.Logf("%v%s %v blocked on pool %d along with %d others", simTime, indent, task, task.Pool, len(*waiters))
			}
			*waiters = append(*waiters, func() {
				scatterTask(task, then)
			})
		}
	}

	var scatterRootTask func(i int)
	scatterRootTask = func(i int) {
		scatterNext := func() {
			next := i + 1
			if next < len(plan.RootTasks) {
				scatterRootTask(next)
			}
		}
		scatterTask(plan.RootTasks[i], scatterNext)
	}

	var endTask func(task *Task)
	startTask = func(task *Task) {
		endTime := simTime + jitter()
		if config.Debug {
			t.Logf("%v%s starting %v at %v", simTime, indent, task, endTime)
		}
		endTime += task.SelfTimes[0]
		for step, subjobPlan := range task.Subjobs {
			if config.Debug {
				t.Logf("%v%s estimating %v subjob %d of %d", simTime, indent, task, step+1, len(task.Subjobs))
			}
			subjobEstimates := estimateJob(t, subjobPlan, config, simTime, indent+"  ")
			for p, e := range subjobEstimates {
				chk.Nil(estimates[p])
				estimates[p] = e
			}
			e := subjobEstimates[subjobPlan]
			endTime += e.OverallDuration + task.SelfTimes[step+1]
		}
		heap.PushOrderable(&eventHeap, taskEvent{
			Time: endTime,
			Func: func() {
				endTask(task)
			},
		})
		if config.Debug {
			t.Logf("%v%s scheduled %v end at %v", simTime, indent, task, endTime)
		}
	}

	var activeGatherThreadCount int
	var gatherQueue deque.Deque[*Task]
	var postGather func(task *Task)
	endTask = func(task *Task) {
		pool := task.Pool
		concurrency := &concurrencyByPool[pool]
		*concurrency--
		if config.Debug {
			t.Logf("%v%s %v released pool %d", simTime, indent, task, pool)
		}
		waiters := &waitersByPool[pool]
		if len(*waiters) > 0 {
			wi := rapid.IntRange(0, len(*waiters)-1).Draw(t, "waiter")
			waiter := (*waiters)[wi]
			*waiters = slices.Delete(*waiters, wi, wi+1)
			waiter()
		}
		postGather(task)
	}

	var startGather func(task *Task)
	postGather = func(task *Task) {
		if activeGatherThreadCount < plan.Config.MaxGatherThreadCount {
			activeGatherThreadCount++
			startGather(task)
		} else {
			if config.Debug {
				t.Logf("%v%s queuing %v gather (already %d active gather threads)", simTime, indent, task, activeGatherThreadCount)
			}
			gatherQueue.PushBack(task)
		}
	}

	var advanceGather func(task *Task, step int)
	startGather = func(task *Task) {
		endTime := simTime + jitter()
		if config.Debug {
			t.Logf("%v%s starting %v gather at %v", simTime, indent, task, endTime)
		}
		endTime += task.GatherTimes[0]
		heap.PushOrderable(&eventHeap, taskEvent{
			Time: endTime,
			Func: func() {
				advanceGather(task, 0)
			},
		})
	}

	stoppedGatherThreads := 0
	advanceGather = func(task *Task, step int) {
		if step < len(task.Children) {
			child := task.Children[step]
			scatterTask(child, func() {
				advanceGather(task, step+1)
			})
		} else {
			// Gather complete, start next if needed
			if task.ReturnErrorFromGather {
				// Do not start next gather nor decrement activeGatherThreadCount
				if config.Debug {
					t.Logf("%v%s %v gather returns error, stopping", simTime, indent, task)
				}
				stoppedGatherThreads++
			} else {
				if config.Debug {
					t.Logf("%v%s %v gather complete", simTime, indent, task)
				}
				if gatherQueue.Len() == 0 {
					activeGatherThreadCount--
					chk.GreaterOrEqual(activeGatherThreadCount, 0)
				} else {
					startGather(gatherQueue.PopFront())
				}
			}
		}
	}

	scatterRootTask(0)
	var concurrentEvents []taskEvent
	for stoppedGatherThreads < plan.Config.MaxGatherThreadCount {
		event, ok := heap.PopOrderable(&eventHeap)
		if !ok {
			break
		}
		concurrentEvents = concurrentEvents[:0]
		for {
			concurrentEvents = append(concurrentEvents, event)
			event, ok = heap.Peek(&eventHeap)
			if !ok || event.Time != concurrentEvents[0].Time {
				break
			}
			_, _ = heap.PopOrderable(&eventHeap)
		}
		if config.Debug && len(concurrentEvents) > 1 {
			t.Logf("%v%s have %d concurrent events", simTime, indent, len(concurrentEvents))
		}
		if len(concurrentEvents) > 1 {
			concurrentEvents = rapid.Permutation(concurrentEvents).Draw(t, "concurrentEvents")
		}
		for _, event := range concurrentEvents {
			simTime = event.Time
			event.Func()
		}
	}

	if config.Debug {
		for pool, waiters := range waitersByPool {
			t.Logf("%d waiters remain in pool %d", len(waiters), pool)
		}
		t.Logf("%d gathers in queue", gatherQueue.Len())
	}

	chk.Nil(estimates[plan])
	estimates[plan] = &Result{
		MaxConcurrencyByPool: maxConcurrencyByPool,
		OverallDuration:      simTime - simTimeOrigin,
	}

	//chk.Equal(1+plan.SubplanCount, len(estimates))

	t.Logf("%v %v estimate done: %v", simTime, plan, *estimates[plan])

	return estimates
}

type taskEvent struct {
	Time time.Duration
	Func func()
}

func (a *taskEvent) Cmp(b *taskEvent) int {
	return cmp.Compare(a.Time, b.Time)
}
