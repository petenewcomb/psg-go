// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
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

type Combiner[T, C any] struct {
	pool                     *omnipool.Pool[Combiner[T, C]]
	gatherer                 *CombinerGatherer[T]
	creationTime             time.Time
	wrapped                  psgwf.GenericCombiner[T, C]
	taskStartLatenciesSec    *tdigest.TDigest
	taskDurationsSec         *tdigest.TDigest
	combineStartLatenciesSec *tdigest.TDigest
	combineDurationsSec      *tdigest.TDigest
	fallbackFn               func(res CombinerResult[T])
}

func NewCombiner[T, C any](
	gatherer *CombinerGatherer[T],
	wrappedCombiner psgwf.GenericCombiner[T, C],
	fallbackFn func(res CombinerResult[T]),
) *Combiner[T, C] {
	pool := omnipool.For[Combiner[T, C]]()
	c := pool.Get()
	c.pool = pool
	c.gatherer = gatherer
	c.creationTime = time.Now()
	c.wrapped = wrappedCombiner
	c.fallbackFn = fallbackFn
	c.taskStartLatenciesSec = tdigestPool.Get()
	c.taskDurationsSec = tdigestPool.Get()
	c.combineStartLatenciesSec = tdigestPool.Get()
	c.combineDurationsSec = tdigestPool.Get()
	return c
}

func (c *Combiner[T, C]) Accumulate(ctx context.Context, wf *psgwf.GenericWorkflow[C],
	res TaskResult[T], err error) (time.Time, error) {
	combineStartTime := time.Now()
	combineStartLatency := combineStartTime.Sub(res.StartTime.Add(res.Duration))
	c.combineStartLatenciesSec.Add(combineStartLatency.Seconds(), 1.0)

	c.taskStartLatenciesSec.Add(res.StartLatency.Seconds(), 1.0)
	c.taskDurationsSec.Add(res.Duration.Seconds(), 1.0)

	flushDeadline, err := c.wrapped.Accumulate(ctx, wf, res.Value, err)

	c.combineDurationsSec.Add(time.Since(combineStartTime).Seconds(), 1.0)

	return flushDeadline, err
}

func (c *Combiner[T, C]) Flush(ctx context.Context) error {
	flushStartTime := time.Now()

	err := c.wrapped.Flush(ctx)

	flushDuration := time.Since(flushStartTime)

	res := CombinerResult[T]{
		CombinerAge:              flushStartTime.Sub(c.creationTime),
		FlushStartTime:           flushStartTime,
		FlushDuration:            flushDuration,
		TaskStartLatenciesSec:    c.taskStartLatenciesSec,
		TaskDurationsSec:         c.taskDurationsSec,
		CombineStartLatenciesSec: c.combineStartLatenciesSec,
		CombineDurationsSec:      c.combineDurationsSec,
	}

	defer c.pool.Put(c)
	defer c.gatherer.recordCombinerTime(c.creationTime)

	// In the new shape there is no aggregated "Value" return — the
	// wrapped accumulator's Flush is responsible for routing data
	// downstream itself. We still call the fallback to record stats
	// from this benchapp wrapper.
	c.fallbackFn(res)
	return err
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
