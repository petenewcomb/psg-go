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
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/stretchr/testify/require"
)

func Run(ctx context.Context, t require.TestingT, plan *Plan, debug bool) error {
	return run(ctx, t, plan, debug)
}

func run(ctx context.Context, t require.TestingT, plan *Plan, debug bool) error {
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	c := &controller{
		Ctx:                          ctx,
		Plan:                         plan,
		Job:                          job,
		TaskPools:                    make([]*psg.TaskPool, len(plan.TaskPools)),
		ConcurrencyByTaskPool:        make([]atomic.Int64, len(plan.TaskPools)),
		MaxConcurrencyByTaskPool:     make([]atomicMinMaxInt64, len(plan.TaskPools)),
		Gathers:                      make([]*psg.Gather[*taskResult], plan.GatherCount),
		Combines:                     make([]*psg.Combine[*taskResult, *combineResult], len(plan.CombinerPoolIndexes)),
		CombinerPools:                make([]*psg.CombinerPool, len(plan.CombinerPools)),
		ConcurrencyByCombinerPool:    make([]atomic.Int64, len(plan.CombinerPools)),
		MaxConcurrencyByCombinerPool: make([]atomicMinMaxInt64, len(plan.CombinerPools)),
		Debug:                        debug,
	}
	c.MinScatterDelay.Store(math.MaxInt64)
	c.MinGatherDelay.Store(math.MaxInt64)
	c.MinCombineDelay.Store(math.MaxInt64)
	c.MinCombineGatherDelay.Store(math.MaxInt64)
	return c.Run(ctx, t)
}

type controller struct {
	Ctx                          context.Context
	Plan                         *Plan
	Job                          *psg.Job
	TaskPoolsLock                sync.Mutex
	TaskPools                    []*psg.TaskPool
	ConcurrencyByTaskPool        []atomic.Int64
	MaxConcurrencyByTaskPool     []atomicMinMaxInt64
	GathersLock                  sync.Mutex
	Gathers                      []*psg.Gather[*taskResult]
	CombinesLock                 sync.Mutex
	Combines                     []*psg.Combine[*taskResult, *combineResult]
	CombinerPools                []*psg.CombinerPool
	ConcurrencyByCombinerPool    []atomic.Int64
	MaxConcurrencyByCombinerPool []atomicMinMaxInt64
	GatheredCount                atomic.Int64
	StartTime                    time.Time
	MinScatterDelay              atomicMinMaxInt64
	MinGatherDelay               atomicMinMaxInt64
	MinCombineDelay              atomicMinMaxInt64
	MinCombineGatherDelay        atomicMinMaxInt64
	Debug                        bool
}

func (c *controller) Run(ctx context.Context, t require.TestingT) error {
	c.StartTime = time.Now()
	c.debugf("starting %v", c.Plan)

	for _, step := range c.Plan.Steps {
		switch step := step.(type) {
		case Scatter:
			c.scatterTask(ctx, t, step.Task)
		default:
			panic(fmt.Sprintf("unknown step type %T", step))
		}
	}

	chk := require.New(t)
	for {
		err := c.Job.CloseAndGatherAll(ctx)
		if err == nil {
			break
		}
		if ge, ok := err.(ExpectedGatherError); ok {
			chk.True(ge.Func.ReturnError)
		} else {
			chk.NoError(err)
		}
	}

	gatheredCount := c.GatheredCount.Load()
	chk.Equal(int64(c.Plan.TaskCount), gatheredCount)

	maxConcurrencyByTaskPool := make([]int64, len(c.MaxConcurrencyByTaskPool))
	for i := range len(maxConcurrencyByTaskPool) {
		maxConcurrencyByTaskPool[i] = c.MaxConcurrencyByTaskPool[i].Load()
	}

	c.debugf("ended %v with min delays scatter=%v gather=%v combine=%v combineGather=%v", c.Plan,
		time.Duration(c.MinScatterDelay.Load()),
		time.Duration(c.MinGatherDelay.Load()),
		time.Duration(c.MinCombineDelay.Load()),
		time.Duration(c.MinCombineGatherDelay.Load()),
	)
	return nil
}

func (c *controller) getTaskPool(index int) *psg.TaskPool {
	c.TaskPoolsLock.Lock()
	defer c.TaskPoolsLock.Unlock()
	pool := c.TaskPools[index]
	if pool == nil {
		pool = psg.NewTaskPool(c.Job, c.Plan.TaskPools[index].ConcurrencyLimit)
		c.TaskPools[index] = pool
	}
	return pool
}

func (c *controller) scatterTask(ctx context.Context, t require.TestingT, task *Task) {
	switch rh := task.ResultHandler.(type) {
	case *Gather:
		gather := func() *psg.Gather[*taskResult] {
			c.GathersLock.Lock()
			defer c.GathersLock.Unlock()
			gather := c.Gathers[rh.Index]
			if gather == nil {
				gather = psg.NewGather(c.newGatherFunc(t))
				c.Gathers[rh.Index] = gather
			}
			return gather
		}()
		for {
			err := gather.Scatter(ctx, c.getTaskPool(task.PoolIndex),
				c.newTaskFunc(task, &c.ConcurrencyByTaskPool[task.PoolIndex]))
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
		combine := func() *psg.Combine[*taskResult, *combineResult] {
			c.CombinesLock.Lock()
			defer c.CombinesLock.Unlock()
			combine := c.Combines[rh.Index]
			if combine == nil {
				// Create a gather for the combiner output
				gather := psg.NewGather(c.newCombinerGatherFunc(t))

				combinerPoolIndex := c.Plan.CombinerPoolIndexes[rh.Index]
				combinerPool := c.CombinerPools[combinerPoolIndex]
				if combinerPool == nil {
					// Create a combiner pool with the concurrency limit
					combinerPool = psg.NewCombinerPool(c.Job)
					combinerPool.SetLimit(c.Plan.CombinerPools[combinerPoolIndex].ConcurrencyLimit)
					c.CombinerPools[combinerPoolIndex] = combinerPool
				}

				// Create a combine operation that uses the gather and factory
				combine = psg.NewCombine(
					gather,
					combinerPool,
					c.newCombinerFactory(t, rh.Index),
				)
				c.Combines[rh.Index] = combine
			}
			return combine
		}()
		for {
			err := combine.Scatter(ctx, c.getTaskPool(task.PoolIndex),
				c.newTaskFunc(task, &c.ConcurrencyByTaskPool[task.PoolIndex]))
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
	panic(localTPanicType{})
}

func (lt *localT) DrainTo(t require.TestingT) {
	for _, call := range lt.calls {
		call(t)
	}
}

type localTPanicType struct{}

func recoverLocalTPanic(f func()) {
	if r := recover(); r != nil {
		if _, ok := r.(localTPanicType); ok {
			if f != nil {
				f()
			}
		} else {
			panic(r)
		}
	}
}

func (c *controller) newTaskFunc(task *Task, concurrency *atomic.Int64) psg.TaskFunc[*taskResult] {
	scatterTime := time.Now()
	return func(ctx context.Context) (res *taskResult, err error) {
		c.MinScatterDelay.UpdateMin(int64(time.Since(scatterTime)))

		res = &taskResult{
			Task:               task,
			ConcurrencyAtStart: concurrency.Add(1),
		}
		defer recoverLocalTPanic(nil)

		t := &res.T
		chk := require.New(t)

		c.debugf("starting %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAtStart)
		chk.Greater(res.ConcurrencyAtStart, int64(0))
		defer func() {
			res.ConcurrencyAfter = concurrency.Add(-1)
			c.debugf("ended %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAfter)
			res.EndTime = time.Now()
		}()
		timer := timerp.Get()
		defer timerp.Put(timer)
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
				err := run(ctx, t, step.Plan, c.Debug)
				chk.NoError(err)
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
	return func(ctx context.Context, res *taskResult, err error) (retErr error) {
		c.MinGatherDelay.UpdateMin(int64(time.Since(res.EndTime)))

		res.T.DrainTo(t)
		chk := require.New(t)

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

func (c *controller) newCombinerFactory(pt require.TestingT, combineIndex int) psg.CombinerFactory[*taskResult, *combineResult] {
	return func() psg.Combiner[*taskResult, *combineResult] {
		cRes := &combineResult{
			Index: combineIndex,
		}
		flush := func(ctx context.Context, combine *Combine, err error, emit psg.CombinerEmitFunc[*combineResult]) {
			cRes.Combine = combine
			cRes.EndTime = time.Now()
			emit(ctx, cRes, err)
			cRes = &combineResult{
				Index: cRes.Index,
			}
		}
		return psg.FuncCombiner[*taskResult, *combineResult]{
			CombineFunc: func(ctx context.Context, tRes *taskResult, err error, emit psg.CombinerEmitFunc[*combineResult]) {
				c.MinCombineDelay.UpdateMin(int64(time.Since(tRes.EndTime)))

				defer recoverLocalTPanic(func() { flush(ctx, nil, nil, emit) })
				t := &cRes.T
				tRes.T.DrainTo(t)
				chk := require.New(t)

				c.updateTaskStats(t, tRes, err)

				task := tRes.Task
				combine := task.ResultHandler.(*Combine)

				chk.Equal(cRes.Index, combine.Index)

				err = c.executeGatherOrCombineFunc(t, ctx, combine, combine.Func)
				if err == nil && combine.Func.ReturnError {
					err = ExpectedCombineError{combine}
				}

				cRes.TaskCount++

				if combine.FlushHandler != nil || err != nil {
					flush(ctx, combine, err, emit)
				}
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[*combineResult]) {
				if cRes.TaskCount > 0 {
					flush(ctx, nil, nil, emit)
				}
			},
		}
	}
}

func (c *controller) newCombinerGatherFunc(t require.TestingT) psg.GatherFunc[*combineResult] {
	return func(ctx context.Context, res *combineResult, err error) error {
		c.MinCombineGatherDelay.UpdateMin(int64(time.Since(res.EndTime)))

		res.T.DrainTo(t)
		chk := require.New(t)

		combine := res.Combine
		if combine == nil {
			// Flush independent of combine case (i.e., linger timeout or job shutdown)
			chk.NoError(err)
		} else {
			// FlushHandler and/or combine error case
			if combine.Func.ReturnError {
				chk.Error(err)
				if ce, ok := err.(ExpectedCombineError); ok {
					chk.Equal(combine.Func, ce.Func)
				} else {
					chk.NoError(err)
				}
				err = nil // reset for return value
			} else {
				chk.NotNil(combine.FlushHandler)
				chk.NoError(err)
			}
			if combine.FlushHandler != nil {
				gather := combine.FlushHandler.(*Gather)
				if err := c.executeGatherOrCombineFunc(t, ctx, gather, gather.Func); err != nil {
					return err
				}
				if gather.Func.ReturnError {
					err = ExpectedGatherError{gather}
				} else {
					err = nil
				}
			}
		}

		gatheredCount := c.GatheredCount.Add(int64(res.TaskCount))
		c.debugf("gathering %d combined tasks from CombineIndex#%d, gathered count now %d", res.TaskCount, res.Index, gatheredCount)
		chk.LessOrEqual(gatheredCount, int64(c.Plan.TaskCount))

		return err
	}
}

func (c *controller) updateTaskStats(t require.TestingT, res *taskResult, err error) {
	chk := require.New(t)
	task := res.Task
	if task.Func.ReturnError {
		chk.Error(err)
	} else {
		chk.NoError(err)
	}

	taskPool := task.PoolIndex
	chk.Greater(res.ConcurrencyAtStart, int64(0))
	chk.LessOrEqual(res.ConcurrencyAtStart, int64(c.Plan.TaskPools[taskPool].ConcurrencyLimit))
	chk.GreaterOrEqual(res.ConcurrencyAfter, int64(0))
	chk.Less(res.ConcurrencyAfter, int64(c.Plan.TaskPools[taskPool].ConcurrencyLimit))
	c.MaxConcurrencyByTaskPool[taskPool].UpdateMax(res.ConcurrencyAtStart)

	elapsedTime := res.EndTime.Sub(c.StartTime)
	chk.GreaterOrEqual(elapsedTime, task.PathDuration())
}

func (c *controller) executeGatherOrCombineFunc(t require.TestingT, ctx context.Context, rh ResultHandler, fn *Func) error {
	chk := require.New(t)
	timer := timerp.Get()
	defer timerp.Put(timer)
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
			err := run(ctx, t, step.Plan, c.Debug)
			chk.NoError(err)
		case Scatter:
			c.debugf("%v scatter %v", rh, step.Task)
			c.scatterTask(ctx, t, step.Task)
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

// taskResult represents the result of executing a simulated task.
type taskResult struct {
	T                  localT
	Task               *Task
	ConcurrencyAtStart int64
	ConcurrencyAfter   int64
	EndTime            time.Time
}

// combineResult represents a result emitted by a simulated combiner.
type combineResult struct {
	T         localT
	Index     int
	Combine   *Combine
	TaskCount int
	EndTime   time.Time
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
