// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func Run(t require.TestingT, ctx context.Context, plan *Plan, debug bool) (map[*Plan]*Result, error) {
	pools := make([]*psg.Pool, len(plan.Config.ConcurrencyLimits))
	for i, limit := range plan.Config.ConcurrencyLimits {
		pools[i] = psg.NewPool(limit)
	}
	c := &controller{
		Plan:                 plan,
		Pools:                pools,
		ConcurrencyByPool:    make([]atomic.Int64, len(pools)),
		MaxConcurrencyByPool: make([]atomicMinMaxInt64, len(pools)),
		ResultMap:            make(map[*Plan]*Result),
		Debug:                debug,
	}
	c.MinScatterDelay.Store(math.MaxInt64)
	c.MinGatherDelay.Store(math.MaxInt64)
	return c.Run(t, ctx)
}

type controller struct {
	Plan                 *Plan
	Pools                []*psg.Pool
	ConcurrencyByPool    []atomic.Int64
	MaxConcurrencyByPool []atomicMinMaxInt64
	GatheredCount        atomic.Int64
	ResultMapMutex       sync.Mutex
	ResultMap            map[*Plan]*Result
	StartTime            time.Time
	MinScatterDelay      atomicMinMaxInt64
	MinGatherDelay       atomicMinMaxInt64
	Debug                bool
}

func (c *controller) Run(t require.TestingT, ctx context.Context) (map[*Plan]*Result, error) {
	c.StartTime = time.Now()
	c.debugf("%v starting %v", time.Since(c.StartTime), c.Plan)

	job := psg.NewJob(ctx, c.Pools...)
	defer job.CancelAndWait()

	for _, task := range c.Plan.RootTasks {
		c.scatterTask(t, ctx, task)
	}

	chk := require.New(t)
	err := job.CloseAndGatherAll(ctx)
	overallDuration := time.Since(c.StartTime)
	if ge, ok := err.(expectedGatherError); ok {
		chk.True(ge.task.ReturnErrorFromGather)
	} else {
		chk.NoError(err)
	}
	gatheredCount := c.GatheredCount.Load()
	chk.Equal(int64(c.Plan.TaskCount), gatheredCount)

	maxConcurrencyByPool := make([]int64, len(c.MaxConcurrencyByPool))
	for i := range len(maxConcurrencyByPool) {
		maxConcurrencyByPool[i] = c.MaxConcurrencyByPool[i].Load()
	}

	c.addResultMap(t, map[*Plan]*Result{
		c.Plan: {
			MaxConcurrencyByPool: maxConcurrencyByPool,
			OverallDuration:      overallDuration,
		},
	})
	c.debugf("%v ended %v with min delays scatter=%v gather=%v", overallDuration, c.Plan,
		time.Duration(c.MinScatterDelay.Load()), time.Duration(c.MinGatherDelay.Load()))
	return c.ResultMap, nil
}

func (c *controller) addResultMap(t require.TestingT, rm map[*Plan]*Result) {
	chk := require.New(t)
	c.ResultMapMutex.Lock()
	defer c.ResultMapMutex.Unlock()
	for p, r := range rm {
		chk.Nil(c.ResultMap[p])
		c.ResultMap[p] = r
	}
}

func (c *controller) scatterTask(t require.TestingT, ctx context.Context, task *Task) {
	err := psg.Scatter(
		ctx,
		c.Pools[task.Pool],
		c.newTaskFunc(task, &c.ConcurrencyByPool[task.Pool]),
		c.newGatherFunc(t, task),
	)
	chk := require.New(t)
	if ge, ok := err.(expectedGatherError); ok {
		chk.True(ge.task.ReturnErrorFromGather)
	} else {
		chk.NoError(err)
	}
}

type localT struct {
	calls []func(require.TestingT)
}

func (lt *localT) Errorf(format string, args ...any) {
	lt.calls = append(lt.calls, func(t require.TestingT) {
		t.Errorf(format, args...)
	})
}

func (lt *localT) FailNow() {
	lt.calls = append(lt.calls, func(t require.TestingT) {
		t.FailNow()
	})
	panic(lt)
}

func (lt *localT) DrainTo(t require.TestingT) {
	for _, call := range lt.calls {
		call(t)
	}
}

func (lt *localT) Error() string {
	return "localT passthrough error"
}

func (c *controller) newTaskFunc(task *Task, concurrency *atomic.Int64) psg.TaskFunc[*taskResult] {
	lt := &localT{}
	scatterTime := time.Now()
	return func(ctx context.Context) (res *taskResult, err error) {
		c.MinScatterDelay.UpdateMin(int64(time.Since(scatterTime)))

		defer func() {
			if r := recover(); r != nil {
				if lt, ok := r.(*localT); ok {
					err = lt
				} else {
					panic(r)
				}
			}
		}()

		chk := require.New(lt)

		res = &taskResult{
			Task:               task,
			ConcurrencyAtStart: concurrency.Add(1),
		}
		c.debugf("%v starting %v on pool %d, concurrency now %d", time.Since(c.StartTime), task, task.Pool, res.ConcurrencyAtStart)
		chk.Greater(res.ConcurrencyAtStart, int64(0))
		defer func() {
			res.ConcurrencyAfter = concurrency.Add(-1)
			elapsedTime := time.Since(c.StartTime)
			c.debugf("%v ended %v on pool %d, concurrency now %d", elapsedTime, task, task.Pool, res.ConcurrencyAfter)
			chk.GreaterOrEqual(elapsedTime, task.PathDurationAtTaskEnd)
			res.TaskEndTime = time.Now()
		}()
		for i, d := range task.SelfTimes {
			if i > 0 {
				subjobPlan := task.Subjobs[i-1]
				resultMap, err := Run(lt, ctx, subjobPlan, c.Debug)
				chk.NoError(err)
				c.addResultMap(lt, resultMap)
			}
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return res, ctx.Err()
			}
		}
		if task.ReturnErrorFromTask {
			return res, fmt.Errorf("%v error", task)
		} else {
			return res, nil
		}
	}
}

func (c *controller) newGatherFunc(t require.TestingT, task *Task) psg.GatherFunc[*taskResult] {
	chk := require.New(t)
	return func(ctx context.Context, res *taskResult, err error) error {
		c.MinGatherDelay.UpdateMin(int64(time.Since(res.TaskEndTime)))

		if lt, ok := err.(*localT); ok {
			lt.DrainTo(t)
		} else if task.ReturnErrorFromTask {
			chk.Error(err)
		} else {
			chk.NoError(err)
		}

		pool := task.Pool
		gatheredCount := c.GatheredCount.Add(1)

		c.debugf("%v gathering %v, gathered count now %d", time.Since(c.StartTime), task, gatheredCount)

		chk.LessOrEqual(gatheredCount, int64(c.Plan.TaskCount))
		chk.Greater(res.ConcurrencyAtStart, int64(0))
		chk.LessOrEqual(res.ConcurrencyAtStart, int64(c.Plan.Config.ConcurrencyLimits[pool]))
		chk.GreaterOrEqual(res.ConcurrencyAfter, int64(0))
		chk.Less(res.ConcurrencyAfter, int64(c.Plan.Config.ConcurrencyLimits[pool]))

		c.MaxConcurrencyByPool[pool].UpdateMax(res.ConcurrencyAtStart)

		for i, d := range task.GatherTimes {
			if i > 0 {
				child := task.Children[i-1]
				c.scatterTask(t, ctx, child)
			}
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if task.ReturnErrorFromGather {
			return expectedGatherError{task}
		} else {
			return nil
		}
	}
}

func (c *controller) debugf(format string, args ...interface{}) {
	if c.Debug {
		fmt.Printf(format+"\n", args...)
	}
}

// Result represents the result of executing a simulated task.
type taskResult struct {
	Task               *Task
	ConcurrencyAtStart int64
	ConcurrencyAfter   int64
	TaskEndTime        time.Time
}

type expectedGatherError struct {
	task *Task
}

func (e expectedGatherError) Error() string {
	return fmt.Sprintf("%v gather error", e.task)
}

type atomicMinMaxInt64 struct {
	value atomic.Int64
}

func (mm *atomicMinMaxInt64) Store(x int64) {
	mm.value.Store(x)
}

func (mm *atomicMinMaxInt64) Load() int64 {
	return mm.value.Load()
}

func (mm *atomicMinMaxInt64) UpdateMax(x int64) {
	mm.update(x, func(a, b int64) bool {
		return a > b
	})
}

func (mm *atomicMinMaxInt64) UpdateMin(x int64) {
	mm.update(x, func(a, b int64) bool {
		return a > b
	})
}

func (mm *atomicMinMaxInt64) update(x int64, t func(a, b int64) bool) {
	for {
		old := mm.value.Load()
		if !t(x, old) {
			break
		}
		if mm.value.CompareAndSwap(old, x) {
			break
		}
	}
}
