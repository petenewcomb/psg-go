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
	"github.com/petenewcomb/psg-go/internal/state"
	"github.com/stretchr/testify/require"
)

func Run(t require.TestingT, ctx context.Context, plan *Plan, debug bool) (map[*Plan]*Result, error) {
	tp := &state.TimerPool{}
	tp.Init()
	return run(t, ctx, plan, debug, tp)
}

func run(t require.TestingT, ctx context.Context, plan *Plan, debug bool, timerPool *state.TimerPool) (map[*Plan]*Result, error) {
	pools := make([]*psg.Pool, len(plan.Pools))
	for i, poolPlan := range plan.Pools {
		pools[i] = psg.NewPool(poolPlan.ConcurrencyLimit)

	}
	c := &controller{
		Ctx:                  ctx,
		Plan:                 plan,
		Pools:                pools,
		ConcurrencyByPool:    make([]atomic.Int64, len(pools)),
		MaxConcurrencyByPool: make([]atomicMinMaxInt64, len(pools)),
		Gathers:              make([]*psg.Gather[*taskResult], plan.GatherCount),
		Combiners:            make([]*psg.Combiner[*taskResult, *taskResult], len(plan.Combiners)),
		ResultMap:            make(map[*Plan]*Result),
		Debug:                debug,
		TimerPool:            timerPool,
	}
	c.MinScatterDelay.Store(math.MaxInt64)
	c.MinGatherDelay.Store(math.MaxInt64)
	c.MinCombineDelay.Store(math.MaxInt64)
	c.MinCombineGatherDelay.Store(math.MaxInt64)
	return c.Run(t, ctx)
}

type controller struct {
	Ctx                   context.Context
	Plan                  *Plan
	Pools                 []*psg.Pool
	ConcurrencyByPool     []atomic.Int64
	MaxConcurrencyByPool  []atomicMinMaxInt64
	GathersLock           sync.Mutex
	Gathers               []*psg.Gather[*taskResult]
	CombinersLock         sync.Mutex
	Combiners             []*psg.Combiner[*taskResult, *taskResult]
	GatheredCount         atomic.Int64
	ResultMapMutex        sync.Mutex
	ResultMap             map[*Plan]*Result
	StartTime             time.Time
	MinScatterDelay       atomicMinMaxInt64
	MinGatherDelay        atomicMinMaxInt64
	MinCombineDelay       atomicMinMaxInt64
	MinCombineGatherDelay atomicMinMaxInt64
	Debug                 bool
	TimerPool             *state.TimerPool
}

func (c *controller) Run(t require.TestingT, ctx context.Context) (map[*Plan]*Result, error) {
	c.StartTime = time.Now()
	c.debugf("starting %v", c.Plan)

	job := psg.NewJob(ctx, c.Pools...)
	//defer job.CancelAndWait()

	for _, step := range c.Plan.Steps {
		switch step := step.(type) {
		case Scatter:
			c.scatterTask(t, ctx, step.Task)
		default:
			panic(fmt.Sprintf("unknown step type %T", step))
		}
	}

	chk := require.New(t)
	for {
		err := job.CloseAndGatherAll(ctx)
		if err == nil {
			break
		}
		if ge, ok := err.(ExpectedGatherError); ok {
			chk.True(ge.Func.ReturnError)
		} else {
			chk.NoError(err)
		}
	}
	overallDuration := time.Since(c.StartTime)

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
	c.debugf("ended %v with min delays scatter=%v gather=%v combine=%v combineGather=%v", c.Plan,
		time.Duration(c.MinScatterDelay.Load()),
		time.Duration(c.MinGatherDelay.Load()),
		time.Duration(c.MinCombineDelay.Load()),
		time.Duration(c.MinCombineGatherDelay.Load()),
	)
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
	switch rh := task.ResultHandler.(type) {
	case *Gather:
		gather := func() *psg.Gather[*taskResult] {
			c.GathersLock.Lock()
			defer c.GathersLock.Unlock()
			gather := c.Gathers[rh.GatherIndex]
			if gather == nil {
				gather = psg.NewGather(c.newGatherFunc(t))
				c.Gathers[rh.GatherIndex] = gather
			}
			return gather
		}()
		for {
			err := gather.Scatter(ctx, c.Pools[task.PoolIndex],
				c.newTaskFunc(task, &c.ConcurrencyByPool[task.PoolIndex]))
			if err == nil {
				break
			}
			chk := require.New(t)
			if ge, ok := err.(ExpectedGatherError); ok {
				chk.True(ge.Func.ReturnError)
			} else {
				chk.NoError(err)
			}
		}
	case *Combine:
		combiner := func() *psg.Combiner[*taskResult, *taskResult] {
			c.CombinersLock.Lock()
			defer c.CombinersLock.Unlock()
			combiner := c.Combiners[rh.CombinerIndex]
			if combiner == nil {
				combiner = psg.NewCombiner(
					c.Ctx,
					c.Plan.Combiners[rh.CombinerIndex].ConcurrencyLimit,
					c.newCombinerFactory(t),
					c.newCombinerGatherFunc(t),
				)
				c.Combiners[rh.CombinerIndex] = combiner
			}
			return combiner
		}()
		for {
			err := combiner.Scatter(ctx, c.Pools[task.PoolIndex],
				c.newTaskFunc(task, &c.ConcurrencyByPool[task.PoolIndex]))
			if err == nil {
				break
			}
			chk := require.New(t)
			if ge, ok := err.(ExpectedGatherError); ok {
				chk.True(ge.Func.ReturnError)
			} else {
				chk.NoError(err)
			}
		}
	default:
		panic(fmt.Sprintf("unknown ResultHandler type: %T", rh))
	}
}

type syncT struct {
	mu    sync.Mutex
	calls []func(require.TestingT)
}

func (st *syncT) Errorf(format string, args ...any) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = append(st.calls, func(t require.TestingT) {
		t.Errorf(format, args...)
	})
}

func (st *syncT) FailNow() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = append(st.calls, func(t require.TestingT) {
		t.FailNow()
	})
	panic(st)
}

func (st *syncT) DrainTo(t require.TestingT) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, call := range st.calls {
		call(t)
	}
}

func (st *syncT) Error() string {
	return "syncT passthrough error"
}

func (c *controller) newTaskFunc(task *Task, concurrency *atomic.Int64) psg.TaskFunc[*taskResult] {
	lt := &syncT{}
	scatterTime := time.Now()
	return func(ctx context.Context) (res *taskResult, err error) {
		c.MinScatterDelay.UpdateMin(int64(time.Since(scatterTime)))

		defer func() {
			if r := recover(); r != nil {
				if lt, ok := r.(*syncT); ok {
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
		c.debugf("starting %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAtStart)
		chk.Greater(res.ConcurrencyAtStart, int64(0))
		defer func() {
			res.ConcurrencyAfter = concurrency.Add(-1)
			c.debugf("ended %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAfter)
			elapsedTime := time.Since(c.StartTime)
			chk.GreaterOrEqual(elapsedTime, task.PathDuration())
			res.TaskEndTime = time.Now()
		}()
		timer := c.TimerPool.Get()
		defer c.TimerPool.Put(timer)
		for _, step := range task.Func.Steps {
			switch step := step.(type) {
			case SelfTime:
				c.debugf("%v self time %v", task, step.Duration())
				timer.Reset(step.Duration())
				select {
				case <-timer.C:
				case <-ctx.Done():
					return res, ctx.Err()
				}
			case Subjob:
				c.debugf("%v subjob %v", task, step.Plan)
				resultMap, err := Run(lt, ctx, step.Plan, c.Debug)
				chk.NoError(err)
				c.addResultMap(lt, resultMap)
			default:
				panic(fmt.Sprintf("unknown step type %T", step))
			}
		}
		if task.Func.ReturnError {
			return res, fmt.Errorf("%v error", task)
		} else {
			return res, nil
		}
	}
}

func (c *controller) newGatherFunc(t require.TestingT) psg.GatherFunc[*taskResult] {
	chk := require.New(t)
	return func(ctx context.Context, res *taskResult, err error) error {
		c.MinGatherDelay.UpdateMin(int64(time.Since(res.TaskEndTime)))

		c.updateTaskStats(t, res, err)

		task := res.Task
		gather := task.ResultHandler.(*Gather)

		gatheredCount := c.GatheredCount.Add(1)
		c.debugf("gathering %v, gathered count now %d", task, gatheredCount)
		chk.LessOrEqual(gatheredCount, int64(c.Plan.TaskCount))

		if err := c.executeGatherOrCombineFunc(t, ctx, gather, gather.Func); err != nil {
			return err
		}
		if gather.Func.ReturnError {
			return ExpectedGatherError{gather}
		} else {
			return nil
		}
	}
}

func (c *controller) newCombinerFactory(t require.TestingT) psg.CombinerFactory[*taskResult, *taskResult] {
	chk := require.New(t)
	return func() psg.CombinerFunc[*taskResult, *taskResult] {
		return func(ctx context.Context, flush bool, res *taskResult, err error) (bool, *taskResult, error) {
			if flush {
				return false, nil, nil
			}

			c.MinCombineDelay.UpdateMin(int64(time.Since(res.TaskEndTime)))

			c.updateTaskStats(t, res, err)

			task := res.Task
			combine := task.ResultHandler.(*Combine)

			if combine.FlushHandler == nil {
				gatheredCount := c.GatheredCount.Add(1)
				c.debugf("combining %v, gathered count now %d", task, gatheredCount)
				chk.LessOrEqual(gatheredCount, int64(c.Plan.TaskCount))
			} else {
				c.debugf("combining %v, not incrementing gathered count until flush", task)
			}

			if err := c.executeGatherOrCombineFunc(t, ctx, combine, combine.Func); err != nil {
				return false, nil, err
			}
			emit := true
			if combine.FlushHandler == nil {
				emit = false
				res = nil
			}
			if combine.Func.ReturnError {
				return emit, res, ExpectedCombineError{combine}
			} else {
				return emit, res, nil
			}
		}
	}
}

func (c *controller) newCombinerGatherFunc(t require.TestingT) psg.GatherFunc[*taskResult] {
	return func(ctx context.Context, res *taskResult, err error) error {
		if res == nil {
			return nil
		}

		c.MinCombineGatherDelay.UpdateMin(int64(time.Since(res.TaskEndTime)))

		chk := require.New(t)
		combine := res.Task.ResultHandler.(*Combine)
		if st, ok := err.(*syncT); ok {
			st.DrainTo(t)
		} else if combine.Func.ReturnError {
			chk.Error(err)
		} else {
			chk.NoError(err)
		}

		gather := combine.FlushHandler.(*Gather)

		gatheredCount := c.GatheredCount.Add(1)
		c.debugf("gathering %v, gathered count now %d", combine, gatheredCount)
		chk.LessOrEqual(gatheredCount, int64(c.Plan.TaskCount))

		if err := c.executeGatherOrCombineFunc(t, ctx, gather, gather.Func); err != nil {
			return err
		}
		if gather.Func.ReturnError {
			return ExpectedGatherError{gather}
		} else {
			return nil
		}
	}
}

func (c *controller) updateTaskStats(t require.TestingT, res *taskResult, err error) {
	chk := require.New(t)
	task := res.Task
	if st, ok := err.(*syncT); ok {
		st.DrainTo(t)
	} else if task.Func.ReturnError {
		chk.Error(err)
	} else {
		chk.NoError(err)
	}
	pool := task.PoolIndex
	chk.Greater(res.ConcurrencyAtStart, int64(0))
	chk.LessOrEqual(res.ConcurrencyAtStart, int64(c.Plan.Pools[pool].ConcurrencyLimit))
	chk.GreaterOrEqual(res.ConcurrencyAfter, int64(0))
	chk.Less(res.ConcurrencyAfter, int64(c.Plan.Pools[pool].ConcurrencyLimit))
	c.MaxConcurrencyByPool[pool].UpdateMax(res.ConcurrencyAtStart)
}

func (c *controller) executeGatherOrCombineFunc(t require.TestingT, ctx context.Context, rh ResultHandler, fn *Func) error {
	chk := require.New(t)
	timer := c.TimerPool.Get()
	defer c.TimerPool.Put(timer)
	for _, step := range fn.Steps {
		switch step := step.(type) {
		case SelfTime:
			c.debugf("%v self time %v", rh, step.Duration())
			timer.Reset(step.Duration())
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		case Subjob:
			c.debugf("%v subjob %v", rh, step.Plan)
			resultMap, err := Run(t, ctx, step.Plan, c.Debug)
			chk.NoError(err)
			c.addResultMap(t, resultMap)
		case Scatter:
			c.debugf("%v scatter %v", rh, step.Task)
			c.scatterTask(t, ctx, step.Task)
		default:
			panic(fmt.Sprintf("unknown step type %T", step))
		}
	}
	c.debugf("%v done", rh)
	return nil
}

func (c *controller) debugf(format string, args ...interface{}) {
	if c.Debug {
		fmt.Printf("%v "+format+"\n", append([]any{time.Now()}, args...)...)
	}
}

// Result represents the result of executing a simulated task.
type taskResult struct {
	Task               *Task
	ConcurrencyAtStart int64
	ConcurrencyAfter   int64
	TaskEndTime        time.Time
}

type ExpectedGatherError struct {
	*Gather
}

func (e ExpectedGatherError) Error() string {
	return fmt.Sprintf("%v error", e.Gather)
}

type ExpectedCombineError struct {
	*Combine
}

func (e ExpectedCombineError) Error() string {
	return fmt.Sprintf("%v error", e.Combine)
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
	mm.update(x, func(old int64) bool {
		return x > old
	})
}

func (mm *atomicMinMaxInt64) UpdateMin(x int64) {
	mm.update(x, func(old int64) bool {
		return x < old
	})
}

func (mm *atomicMinMaxInt64) update(x int64, t func(old int64) bool) {
	for {
		old := mm.value.Load()
		if !t(old) {
			break
		}
		if mm.value.CompareAndSwap(old, x) {
			break
		}
	}
}
