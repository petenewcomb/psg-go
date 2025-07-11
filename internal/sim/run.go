// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
	"github.com/stretchr/testify/assert"
)

func Run(ctx context.Context, t assert.TestingT, plan *Plan) error {
	traceRegion := "sim.Run"
	if trace.IsEnabled() {
		//nolint:lll // long url
		// See [MaxEventTrailerDataSize] defined to be 1<<10
		// [MaxEventTrailerDataSize]: https://cs.opensource.google/go/go/+/master:src/internal/trace/tracev2/events.go;drc=6c3b5a2798c83d583cb37dba9f39c47300d19f1f;l=588
		header := "Test plan:\n\n"
		continuationHeader := "Test plan (continued):\n\n"
		maxChunkSize := 1<<10 - max(len(header), len(continuationHeader)) - 1
		planText := fmt.Sprintf("%#v", plan)
		for len(planText) > maxChunkSize {
			chunk := planText[:maxChunkSize]
			lastNewlineIndex := strings.LastIndexByte(chunk, '\n')
			if lastNewlineIndex != -1 {
				chunk = planText[:lastNewlineIndex]
				planText = planText[len(chunk)+1:]
			} else {
				l := len(chunk)
				for l > 0 && !utf8.RuneStart(chunk[l-1]) {
					l--
				}
				chunk = chunk[:l]
				planText = planText[l:]
			}
			trace.Log(ctx, traceRegion, header+chunk+"\n")
			header = continuationHeader
		}
		trace.Log(ctx, traceRegion, header+planText+"\n")
	}
	return run(ctx, t, plan)
}

func run(ctx context.Context, t assert.TestingT, plan *Plan) error {
	defer trace.StartRegion(ctx, "sim.run").End()

	job := psg.NewJob(ctx)
	defer func() {
		defer trace.StartRegion(ctx, "sim.run.cleanup").End()
		job.CancelAndWait()
	}()

	c := &controller{
		Plan:                         plan,
		Job:                          job,
		TaskPools:                    make([]*psg.TaskPool, len(plan.TaskPools)),
		ConcurrencyByTaskPool:        make([]atomic.Int64, len(plan.TaskPools)),
		MaxConcurrencyByTaskPool:     make([]atomicMinMaxInt64, len(plan.TaskPools)),
		Gathers:                      make([]*psg.GatherOp[*taskResult], plan.GatherCount),
		Combines:                     make([]*psg.CombineOp[*taskResult, *combineResult], len(plan.CombinerPoolIndexes)),
		CombinerPools:                make([]*psg.CombinerPool, len(plan.CombinerPools)),
		ConcurrencyByCombinerPool:    make([]atomic.Int64, len(plan.CombinerPools)),
		MaxConcurrencyByCombinerPool: make([]atomicMinMaxInt64, len(plan.CombinerPools)),
	}
	c.MinScatterDelay.Store(math.MaxInt64)
	c.MinGatherDelay.Store(math.MaxInt64)
	c.MinCombineDelay.Store(math.MaxInt64)
	c.MinCombineGatherDelay.Store(math.MaxInt64)
	return c.Run(ctx, t)
}

type controller struct {
	Plan                         *Plan
	Job                          *psg.Job
	TaskPoolsLock                sync.Mutex
	TaskPools                    []*psg.TaskPool
	ConcurrencyByTaskPool        []atomic.Int64
	MaxConcurrencyByTaskPool     []atomicMinMaxInt64
	GathersLock                  sync.Mutex
	Gathers                      []*psg.GatherOp[*taskResult]
	CombinesLock                 sync.Mutex
	Combines                     []*psg.CombineOp[*taskResult, *combineResult]
	CombinerPools                []*psg.CombinerPool
	ConcurrencyByCombinerPool    []atomic.Int64
	MaxConcurrencyByCombinerPool []atomicMinMaxInt64
	GatheredCount                atomic.Int64
	StartTime                    time.Time
	MinScatterDelay              atomicMinMaxInt64
	MinGatherDelay               atomicMinMaxInt64
	MinCombineDelay              atomicMinMaxInt64
	MinCombineGatherDelay        atomicMinMaxInt64
}

func (c *controller) Run(ctx context.Context, t assert.TestingT) error {
	traceRegion := "sim.controller.Run"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, "plan", "%v", c.Plan)
	c.StartTime = time.Now()

	for _, step := range c.Plan.Steps {
		switch step := step.(type) {
		case Scatter:
			c.scatterTask(ctx, t, step.Task)
		default:
			panic(fmt.Sprintf("unknown step type %T", step))
		}
	}

	chk := assert.New(t)
	// Loop to handle expected errors from gathers
	trace.WithRegion(ctx, "sim.CloseAndGatherAll(loop)", func() {
		for {
			err := c.Job.CloseAndGatherAll(ctx)
			trace.Logf(ctx, "sim.CloseAndGatherAll", "err=%v", err)
			if err == nil {
				break
			}
			var ge ExpectedGatherError
			if errors.As(err, &ge) {
				chk.True(ge.g.Func.ReturnError)
			} else {
				chk.NoError(err)
			}
		}
	})

	gatheredCount := c.GatheredCount.Load()
	chk.GreaterOrEqual(gatheredCount, int64(c.Plan.MinGatherCount))
	chk.LessOrEqual(gatheredCount, int64(c.Plan.MaxGatherCount))

	maxConcurrencyByTaskPool := make([]int64, len(c.MaxConcurrencyByTaskPool))
	for i := range len(maxConcurrencyByTaskPool) {
		maxConcurrencyByTaskPool[i] = c.MaxConcurrencyByTaskPool[i].Load()
	}

	trace.Logf(ctx, "sim.controller.Run(summary)", "min delays: scatter=%v gather=%v combine=%v combineGather=%v",
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
		pool = psg.NewTaskPool(c.Job, psgopt.WithMaxConcurrency(c.Plan.TaskPools[index].ConcurrencyLimit))
		c.TaskPools[index] = pool
	}
	return pool
}

func (c *controller) scatterTask(ctx context.Context, t assert.TestingT, task *Task) {
	defer trace.StartRegion(ctx, "sim.scatterTask").End()
	trace.Logf(ctx, "task", "%v", task)

	switch rh := task.ResultHandler.(type) {
	case *Gather:
		gatherOp := func() *psg.GatherOp[*taskResult] {
			c.GathersLock.Lock()
			defer c.GathersLock.Unlock()
			gatherOp := c.Gathers[rh.Index]
			if gatherOp == nil {
				gatherOp = psg.NewGatherOp(c.newGatherFunc(t))
				c.Gathers[rh.Index] = gatherOp
			}
			return gatherOp
		}()
		taskPool := c.getTaskPool(task.PoolIndex)
		baseTaskFn := c.newTaskFunc(t, task, &c.ConcurrencyByTaskPool[task.PoolIndex])
		taskFn := func(ctx context.Context) (*taskResult, error) {
			defer trace.StartRegion(ctx, "sim.scatterTask.Task").End()
			trace.Logf(ctx, "task", "%v", task)
			return baseTaskFn(ctx)
		}
		// Loop to handle expected errors from gathers that are processed by
		// Scatter as it applies backpressure
		for {
			err := gatherOp.Scatter(ctx, taskPool, taskFn)
			if err == nil {
				break
			}
			chk := assert.New(t)
			var ge ExpectedGatherError
			if errors.As(err, &ge) {
				chk.True(ge.g.Func.ReturnError)
			} else {
				chk.NoError(err)
			}
		}
	case *Combine:
		c.debugf(ctx, "Scattering %v to Combine", task)
		combineOp := func() *psg.CombineOp[*taskResult, *combineResult] {
			c.debugf(ctx, "Getting combine for %v", task)
			defer c.debugf(ctx, "Got combine for %v", task)
			c.CombinesLock.Lock()
			defer c.CombinesLock.Unlock()
			combineOp := c.Combines[rh.Index]
			if combineOp == nil {
				// Create a gather for the combiner output
				gatherOp := psg.NewGatherOp(c.newCombinerGatherFunc(t))

				combinerPoolIndex := c.Plan.CombinerPoolIndexes[rh.Index]
				combinerPool := c.CombinerPools[combinerPoolIndex]
				if combinerPool == nil {
					// Create a combiner pool with the concurrency limit
					combinerPool = psg.NewCombinerPool(c.Job,
						psgopt.WithConcurrencyBounds(0, c.Plan.CombinerPools[combinerPoolIndex].ConcurrencyLimit))
					c.CombinerPools[combinerPoolIndex] = combinerPool
				}

				// Create a combine operation that uses the gather and factory
				combineOp = psg.NewCombineOp(
					gatherOp,
					combinerPool,
					c.newCombinerFactory(t, rh.Index),
				)
				c.Combines[rh.Index] = combineOp
			}
			return combineOp
		}()
		// Loop to handle expected errors from gathers that are processed by
		// Scatter as it applies backpressure
		for {
			c.debugf(ctx, "Calling combine.Scatter for %v", task)
			err := combineOp.Scatter(ctx, c.getTaskPool(task.PoolIndex),
				c.newTaskFunc(t, task, &c.ConcurrencyByTaskPool[task.PoolIndex]))
			c.debugf(ctx, "Called combine.Scatter for %v: %v", task, err)
			if err == nil {
				break
			}
			chk := assert.New(t)
			var ge ExpectedGatherError
			if errors.As(err, &ge) {
				chk.True(ge.g.Func.ReturnError)
			} else {
				chk.NoError(err)
			}
		}
	default:
		panic(fmt.Sprintf("unknown ResultHandler type: %T", rh))
	}
}

func (c *controller) newTaskFunc(t assert.TestingT, task *Task, concurrency *atomic.Int64) psgfn.Task[*taskResult] {
	scatterTime := time.Now()
	return func(ctx context.Context) (res *taskResult, err error) {
		c.MinScatterDelay.UpdateMin(int64(time.Since(scatterTime)))

		res = &taskResult{
			Task:               task,
			ConcurrencyAtStart: concurrency.Add(1),
		}

		chk := assert.New(t)

		c.debugf(ctx, "starting %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAtStart)
		chk.Positive(res.ConcurrencyAtStart)
		defer func() {
			res.ConcurrencyAfter = concurrency.Add(-1)
			c.debugf(ctx, "ended %v on pool %d, concurrency now %d", task, task.PoolIndex, res.ConcurrencyAfter)
			res.EndTime = time.Now()
		}()
		timer := timerp.Get()
		defer timerp.Put(timer)
		for _, step := range task.Func.Steps {
			switch step := step.(type) {
			case SelfTime:
				c.debugf(ctx, "%v self time %v", task, step.Duration())
				timerp.Reset(timer, step.Duration())
				select {
				case <-timer.C:
				case <-ctx.Done():
					return res, ctx.Err()
				}
			case Subjob:
				c.debugf(ctx, "%v subjob %v", task, step.Plan)
				err := run(ctx, t, step.Plan)
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

func (c *controller) newGatherFunc(t assert.TestingT) psgfn.Gather[*taskResult] {
	return func(ctx context.Context, res *taskResult, err error) (retErr error) {
		defer trace.StartRegion(ctx, "sim.gatherFunc").End()
		trace.Logf(ctx, "gather", "%v", res.Task.ResultHandler.(*Gather))

		c.MinGatherDelay.UpdateMin(int64(time.Since(res.EndTime)))

		chk := assert.New(t)

		c.updateTaskStats(t, res, err)

		task := res.Task
		gather := task.ResultHandler.(*Gather)

		gatheredCount := c.GatheredCount.Add(1)
		trace.Logf(ctx, "sim.GatheredCount", "%d", gatheredCount)
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

func (c *controller) newCombinerFactory(
	t assert.TestingT,
	combineIndex int,
) psg.CombinerFactory[*taskResult, *combineResult] {
	return func() psg.Combiner[*taskResult, *combineResult] {
		cRes := &combineResult{
			Index: combineIndex,
		}
		flush := func(ctx context.Context, combine *Combine, err error, emit psgfn.Emit[*combineResult]) {
			cRes.Combine = combine
			cRes.EndTime = time.Now()
			emit(ctx, cRes, err)
			cRes = &combineResult{
				Index: cRes.Index,
			}
		}
		return psgfn.Combiner[*taskResult, *combineResult]{
			CombineFn: func(ctx context.Context, tRes *taskResult, err error, emit psgfn.Emit[*combineResult]) {
				c.MinCombineDelay.UpdateMin(int64(time.Since(tRes.EndTime)))

				chk := assert.New(t)

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
			FlushFn: func(ctx context.Context, emit psgfn.Emit[*combineResult]) {
				flush(ctx, nil, nil, emit)
			},
		}
	}
}

func (c *controller) newCombinerGatherFunc(t assert.TestingT) psgfn.Gather[*combineResult] {
	return func(ctx context.Context, res *combineResult, err error) error {
		c.MinCombineGatherDelay.UpdateMin(int64(time.Since(res.EndTime)))

		chk := assert.New(t)

		combine := res.Combine
		if combine == nil {
			// Flush independent of combine case (i.e., linger timeout or job shutdown)
			chk.NoError(err)
		} else {
			// FlushHandler and/or combine error case
			if combine.Func.ReturnError {
				chk.Error(err)
				var ce ExpectedCombineError
				if errors.As(err, &ce) {
					chk.Equal(combine.Func, ce.c.Func)
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
		c.debugf(ctx, "gathering %d combined tasks from CombineIndex#%d, gathered count now %d",
			res.TaskCount, res.Index, gatheredCount)
		chk.LessOrEqual(gatheredCount, int64(c.Plan.MaxGatherCount))

		return err
	}
}

func (c *controller) updateTaskStats(t assert.TestingT, res *taskResult, err error) {
	chk := assert.New(t)
	task := res.Task
	if task.Func.ReturnError {
		chk.Error(err)
	} else {
		if err != nil {
			panic("unexpected error: " + err.Error())
		}
		chk.NoError(err)
	}

	taskPool := task.PoolIndex
	chk.Positive(res.ConcurrencyAtStart)
	chk.LessOrEqual(res.ConcurrencyAtStart, int64(c.Plan.TaskPools[taskPool].ConcurrencyLimit))
	chk.GreaterOrEqual(res.ConcurrencyAfter, int64(0))
	chk.Less(res.ConcurrencyAfter, int64(c.Plan.TaskPools[taskPool].ConcurrencyLimit))
	c.MaxConcurrencyByTaskPool[taskPool].UpdateMax(res.ConcurrencyAtStart)

	elapsedTime := res.EndTime.Sub(c.StartTime)
	chk.GreaterOrEqual(elapsedTime, task.PathDuration())
}

func (c *controller) executeGatherOrCombineFunc(
	t assert.TestingT,
	ctx context.Context,
	rh ResultHandler,
	fn *Func,
) error {
	chk := assert.New(t)
	timer := timerp.Get()
	defer timerp.Put(timer)
	for _, step := range fn.Steps {
		switch step := step.(type) {
		case SelfTime:
			c.debugf(ctx, "%v self time %v", rh, step.Duration())
			timerp.Reset(timer, step.Duration())
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		case Subjob:
			c.debugf(ctx, "%v subjob %v", rh, step.Plan)
			err := run(ctx, t, step.Plan)
			chk.NoError(err)
		case Scatter:
			c.debugf(ctx, "%v scatter %v", rh, step.Task)
			c.scatterTask(ctx, t, step.Task)
		default:
			panic(fmt.Sprintf("unknown step type %T", step))
		}
	}
	c.debugf(ctx, "%v done", rh)
	return nil
}

func (c *controller) debugf(ctx context.Context, format string, args ...interface{}) {
	trace.Logf(ctx, "sim.debugf", format, args...)
}

// taskResult represents the result of executing a simulated task.
type taskResult struct {
	Task               *Task
	ConcurrencyAtStart int64
	ConcurrencyAfter   int64
	EndTime            time.Time
}

// combineResult represents a result emitted by a simulated combiner.
type combineResult struct {
	Index     int
	Combine   *Combine
	TaskCount int
	EndTime   time.Time
}

type ExpectedGatherError struct {
	g *Gather
}

func (e ExpectedGatherError) Error() string {
	return fmt.Sprintf("expected %v error", e.g)
}

type ExpectedCombineError struct {
	c *Combine
}

func (e ExpectedCombineError) Error() string {
	return fmt.Sprintf("expected %v error", e.c)
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
