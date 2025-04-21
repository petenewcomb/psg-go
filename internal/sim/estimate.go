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
	MinJitter time.Duration
	MedJitter time.Duration
	MaxJitter time.Duration
	Debug     bool
}

func EstimateJob(t *rapid.T, plan *Plan, trialCount int, config *JobConfig) map[*Plan]*ResultRange {
	chk := require.New(t)
	estimateMap := make(map[*Plan]*ResultRange)

	var estimateSubjobs func(tasks []*Task)
	runTrials := func(plan *Plan) {
		estimateMapLenOrigin := len(estimateMap)
		estimateSubjobs(plan.RootTasks)
		rr := &ResultRange{}
		durations := make([]time.Duration, trialCount)
		for tr := range trialCount {
			r := estimateJob(t, plan, config, estimateMap, 0, "")
			durations[tr] = r.OverallDuration
			if tr == 0 {
				rr.MinMaxConcurrencyByPool = slices.Clone(r.MaxConcurrencyByPool)
				rr.MaxMaxConcurrencyByPool = slices.Clone(r.MaxConcurrencyByPool)
				rr.MinOverallDuration = r.OverallDuration
				rr.MaxOverallDuration = r.OverallDuration
			} else {
				for i := range len(r.MaxConcurrencyByPool) {
					rr.MinMaxConcurrencyByPool[i] = min(rr.MinMaxConcurrencyByPool[i], r.MaxConcurrencyByPool[i])
					rr.MaxMaxConcurrencyByPool[i] = max(rr.MaxMaxConcurrencyByPool[i], r.MaxConcurrencyByPool[i])
				}
				rr.MinOverallDuration = min(rr.MinOverallDuration, r.OverallDuration)
				rr.MaxOverallDuration = max(rr.MaxOverallDuration, r.OverallDuration)
			}
		}
		slices.Sort(durations)
		rr.MedOverallDuration = durations[len(durations)/2]
		estimateMap[plan] = rr
		chk.Equal(1+plan.SubplanCount, len(estimateMap)-estimateMapLenOrigin)
	}

	estimateSubjobs = func(tasks []*Task) {
		for _, task := range tasks {
			for _, subjob := range task.Subjobs {
				runTrials(subjob)
			}
			estimateSubjobs(task.Children)
		}
	}

	runTrials(plan)
	return estimateMap
}

func estimateJob(
	t *rapid.T,
	plan *Plan,
	config *JobConfig,
	subjobEstimates map[*Plan]*ResultRange,
	simTimeOrigin time.Duration,
	indent string,
) *Result {
	simTime := simTimeOrigin
	chk := require.New(t)

	var eventHeap heap.Heap[taskEvent, heap.Min]

	if config.MinJitter < 0 {
		panic("MinJitter may not be less than zero")
	}
	if config.MedJitter < config.MinJitter {
		panic("MedJitter may not be less than MinJitter")
	}
	if config.MaxJitter < config.MedJitter {
		panic("MaxJitter may not be less than MedJitter")
	}

	jitter := func() time.Duration {
		return time.Duration(biasedInt64(
			int64(config.MinJitter), int64(config.MedJitter), int64(config.MaxJitter),
		).Draw(t, "jitterNoise"))
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
			e := subjobEstimates[subjobPlan]
			endTime += time.Duration(biasedInt64(
				int64(e.MinOverallDuration), int64(e.MedOverallDuration), int64(e.MaxOverallDuration),
			).Draw(t, "subjobDuration"))
			endTime += task.SelfTimes[step+1]
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
		chk.GreaterOrEqual(simTime, task.PathDurationAtTaskEnd)
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
		advanceGather(task, 0)
	}

	var endGather func(task *Task)
	advanceGather = func(task *Task, step int) {
		endTime := simTime + jitter()
		if config.Debug && step == 0 {
			t.Logf("%v%s starting %v gather at %v", simTime, indent, task, endTime)
		}
		endTime += task.GatherTimes[step]

		if step < len(task.Children) {
			heap.PushOrderable(&eventHeap, taskEvent{
				Time: endTime,
				Func: func() {
					scatterTask(task.Children[step], func() {
						advanceGather(task, step+1)
					})
				},
			})
		} else {
			heap.PushOrderable(&eventHeap, taskEvent{
				Time: endTime,
				Func: func() {
					endGather(task)
				},
			})
		}
	}

	stoppedGatherThreads := 0
	endGather = func(task *Task) {
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

	result := &Result{
		MaxConcurrencyByPool: maxConcurrencyByPool,
		OverallDuration:      simTime - simTimeOrigin,
	}

	chk.GreaterOrEqual(result.OverallDuration, plan.MaxPathDuration)

	if config.Debug {
		t.Logf("%v %v estimate done: %v", simTime, plan, *result)
	}

	return result
}

type taskEvent struct {
	Time time.Duration
	Func func()
}

func (a *taskEvent) Cmp(b *taskEvent) int {
	return cmp.Compare(a.Time, b.Time)
}
