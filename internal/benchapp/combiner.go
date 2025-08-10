// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgwf"
)

type CombinerResult[T any] struct {
	CombinerAge              time.Duration
	FlushStartTime           time.Time
	FlushDuration            time.Duration
	TaskStartLatenciesSec    *tdigest.TDigest
	TaskDurationsSec         *tdigest.TDigest
	CombineStartLatenciesSec *tdigest.TDigest
	CombineDurationsSec      *tdigest.TDigest
	Value                    T
}

type Combiner[I, O, C any] struct {
	pool                     *omnipool.Pool[Combiner[I, O, C]]
	gatherer                 *CombinerGatherer[O]
	creationTime             time.Time
	wrapped                  psgwf.GenericCombiner[I, O, C]
	taskStartLatenciesSec    *tdigest.TDigest
	taskDurationsSec         *tdigest.TDigest
	combineStartLatenciesSec *tdigest.TDigest
	combineDurationsSec      *tdigest.TDigest
	fallbackFn               func(res CombinerResult[O])
}

func NewCombiner[I, O, C any](
	gatherer *CombinerGatherer[O],
	wrappedCombiner psgwf.GenericCombiner[I, O, C],
	fallbackFn func(res CombinerResult[O]),
) *Combiner[I, O, C] {
	pool := omnipool.For[Combiner[I, O, C]]()
	c := pool.Get()
	c.pool = pool
	c.gatherer = gatherer
	c.creationTime = time.Now()
	c.wrapped = wrappedCombiner
	c.taskStartLatenciesSec = tdigestPool.Get()
	c.taskDurationsSec = tdigestPool.Get()
	c.combineStartLatenciesSec = tdigestPool.Get()
	c.combineDurationsSec = tdigestPool.Get()
	return c
}

func (c *Combiner[I, O, C]) Combine(ctx context.Context, wf *psgwf.GenericWorkflow[C],
	res TaskResult[I], err error) (time.Time, error) {
	combineStartTime := time.Now()
	combineStartLatency := combineStartTime.Sub(res.StartTime.Add(res.Duration))
	c.combineStartLatenciesSec.Add(combineStartLatency.Seconds(), 1.0)

	c.taskStartLatenciesSec.Add(res.StartLatency.Seconds(), 1.0)
	c.taskDurationsSec.Add(res.Duration.Seconds(), 1.0)

	flushDeadline, err := c.wrapped.Combine(ctx, wf, res.Value, err)

	c.combineDurationsSec.Add(time.Since(combineStartTime).Seconds(), 1.0)

	return flushDeadline, err
}

func (c *Combiner[I, O, C]) Flush(ctx context.Context) (*psgwf.GenericWorkflow[C], CombinerResult[O], error) {
	flushStartTime := time.Now()

	res := CombinerResult[O]{
		CombinerAge:              flushStartTime.Sub(c.creationTime),
		FlushStartTime:           flushStartTime,
		TaskStartLatenciesSec:    c.taskStartLatenciesSec,
		TaskDurationsSec:         c.taskDurationsSec,
		CombineStartLatenciesSec: c.combineStartLatenciesSec,
		CombineDurationsSec:      c.combineDurationsSec,
	}

	wf, value, err := c.wrapped.Flush(ctx)
	res.FlushDuration = time.Since(flushStartTime)
	res.Value = value

	defer c.pool.Put(c)
	defer c.gatherer.recordCombinerTime(c.creationTime)

	if errors.Is(err, psgfn.ErrDoNotGather) {
		c.fallbackFn(res)
		return nil, CombinerResult[O]{}, err
	}
	return wf, res, err
}

type CombinerGatherer[T any] struct {
	controller *Controller
	wrappedFn  psgfn.Gather[T]

	taskStartLatenciesSec    *tdigest.TDigest
	taskDurationsSec         *tdigest.TDigest
	combineStartLatenciesSec *tdigest.TDigest
	combineDurationsSec      *tdigest.TDigest

	combinerAgesSec         *tdigest.TDigest
	combinerCounts          *tdigest.TDigest
	gatherStartLatenciesSec *tdigest.TDigest
	gatherDurationsSec      *tdigest.TDigest

	cumulativeCombinerTime atomic.Int64 // time.Duration

	gatherFn psgfn.Gather[CombinerResult[T]]
}

func NewCombinerGatherer[T any](c *Controller, gatherFn psgfn.Gather[T]) {
	// TODO: pool
	g := &CombinerGatherer[T]{
		controller: c,
		wrappedFn:  gatherFn,
	}
	g.gatherFn = g.gather
}

func (g *CombinerGatherer[T]) GatherFn() psgfn.Gather[CombinerResult[T]] {
	if g.gatherFn == nil {
		g.gatherFn = g.gather
	}
	return g.gatherFn
}

func (g *CombinerGatherer[T]) recordCombinerTime(creationTime time.Time) {
	recordedCombinerTime := g.controller.RecordedDurationSince(creationTime)
	if recordedCombinerTime != 0 {
		g.cumulativeCombinerTime.Add(int64(recordedCombinerTime))
	}
}

func (g *CombinerGatherer[T]) gather(ctx context.Context, res CombinerResult[T], err error) error {
	gatherStartTime := time.Now()
	err = g.wrappedFn(ctx, res.Value, err)
	gatherDuration := time.Since(gatherStartTime)

	if g.controller.Recording() {
		adoptOrMergeDigest(&g.taskStartLatenciesSec, res.TaskStartLatenciesSec)
		adoptOrMergeDigest(&g.taskDurationsSec, res.TaskDurationsSec)
		adoptOrMergeDigest(&g.combineStartLatenciesSec, res.CombineStartLatenciesSec)
		adoptOrMergeDigest(&g.combineDurationsSec, res.CombineDurationsSec)

		addToDigest(&g.combinerAgesSec, res.CombinerAge.Seconds(), 1.0)
		addToDigest(&g.combinerCounts, res.CombineStartLatenciesSec.Count(), 1.0)
		addToDigest(&g.gatherStartLatenciesSec, gatherStartTime.Sub(res.FlushStartTime.Add(res.FlushDuration)).Seconds(), 1.0)
		addToDigest(&g.gatherDurationsSec, gatherDuration.Seconds(), 1.0)
	}

	return err
}
