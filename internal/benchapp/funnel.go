// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/psgwf"
)

type FunnelResult[T any] struct {
	FunnelAge               time.Duration
	FlushStartTime          time.Time
	FlushDuration           time.Duration
	TaskStartLatenciesSec   *tdigest.TDigest
	TaskDurationsSec        *tdigest.TDigest
	FunnelStartLatenciesSec *tdigest.TDigest
	FunnelDurationsSec      *tdigest.TDigest
	Value                   T
}

type Funnel[T, C any] struct {
	pool                    *omnipool.Pool[Funnel[T, C]]
	skimmer                 *FunnelSkimmer[T]
	creationTime            time.Time
	wrapped                 psgwf.GenericFunnel[T, C]
	taskStartLatenciesSec   *tdigest.TDigest
	taskDurationsSec        *tdigest.TDigest
	funnelStartLatenciesSec *tdigest.TDigest
	funnelDurationsSec      *tdigest.TDigest
	fallbackFn              func(res FunnelResult[T])
}

func NewFunnel[T, C any](
	skimmer *FunnelSkimmer[T],
	wrappedFunnel psgwf.GenericFunnel[T, C],
	fallbackFn func(res FunnelResult[T]),
) *Funnel[T, C] {
	pool := omnipool.For[Funnel[T, C]]()
	c := pool.Get()
	c.pool = pool
	c.skimmer = skimmer
	c.creationTime = time.Now()
	c.wrapped = wrappedFunnel
	c.fallbackFn = fallbackFn
	c.taskStartLatenciesSec = tdigestPool.Get()
	c.taskDurationsSec = tdigestPool.Get()
	c.funnelStartLatenciesSec = tdigestPool.Get()
	c.funnelDurationsSec = tdigestPool.Get()
	return c
}

func (c *Funnel[T, C]) Accumulate(ctx context.Context, wf *psgwf.GenericWorkflow[C],
	res TaskResult[T], err error) (time.Time, error) {
	funnelStartTime := time.Now()
	funnelStartLatency := funnelStartTime.Sub(res.StartTime.Add(res.Duration))
	c.funnelStartLatenciesSec.Add(funnelStartLatency.Seconds(), 1.0)

	c.taskStartLatenciesSec.Add(res.StartLatency.Seconds(), 1.0)
	c.taskDurationsSec.Add(res.Duration.Seconds(), 1.0)

	flushDeadline, err := c.wrapped.Accumulate(ctx, wf, res.Value, err)

	c.funnelDurationsSec.Add(time.Since(funnelStartTime).Seconds(), 1.0)

	return flushDeadline, err
}

func (c *Funnel[T, C]) Flush(ctx context.Context) error {
	flushStartTime := time.Now()

	err := c.wrapped.Flush(ctx)

	flushDuration := time.Since(flushStartTime)

	res := FunnelResult[T]{
		FunnelAge:               flushStartTime.Sub(c.creationTime),
		FlushStartTime:          flushStartTime,
		FlushDuration:           flushDuration,
		TaskStartLatenciesSec:   c.taskStartLatenciesSec,
		TaskDurationsSec:        c.taskDurationsSec,
		FunnelStartLatenciesSec: c.funnelStartLatenciesSec,
		FunnelDurationsSec:      c.funnelDurationsSec,
	}

	defer c.pool.Put(c)
	defer c.skimmer.recordFunnelTime(c.creationTime)

	// In the new shape there is no aggregated "Value" return — the
	// wrapped accumulator's Flush is responsible for routing data
	// downstream itself. We still call the fallback to record stats
	// from this benchapp wrapper.
	c.fallbackFn(res)
	return err
}

type FunnelSkimmer[T any] struct {
	controller *Controller
	wrappedFn  streampool.HandlerFunc[T]

	taskStartLatenciesSec   *tdigest.TDigest
	taskDurationsSec        *tdigest.TDigest
	funnelStartLatenciesSec *tdigest.TDigest
	funnelDurationsSec      *tdigest.TDigest

	funnelAgesSec         *tdigest.TDigest
	funnelCounts          *tdigest.TDigest
	skimStartLatenciesSec *tdigest.TDigest
	skimDurationsSec      *tdigest.TDigest

	cumulativeFunnelTime atomic.Int64 // time.Duration

	skimFn streampool.HandlerFunc[FunnelResult[T]]
}

func NewFunnelSkimmer[T any](c *Controller, skimFn streampool.HandlerFunc[T]) {
	// TODO: pool
	g := &FunnelSkimmer[T]{
		controller: c,
		wrappedFn:  skimFn,
	}
	g.skimFn = g.skim
}

func (g *FunnelSkimmer[T]) SkimFn() streampool.HandlerFunc[FunnelResult[T]] {
	if g.skimFn == nil {
		g.skimFn = g.skim
	}
	return g.skimFn
}

func (g *FunnelSkimmer[T]) recordFunnelTime(creationTime time.Time) {
	recordedFunnelTime := g.controller.RecordedDurationSince(creationTime)
	if recordedFunnelTime != 0 {
		g.cumulativeFunnelTime.Add(int64(recordedFunnelTime))
	}
}

func (g *FunnelSkimmer[T]) skim(ctx context.Context, res FunnelResult[T], err error) error {
	skimStartTime := time.Now()
	err = g.wrappedFn(ctx, res.Value, err)
	skimDuration := time.Since(skimStartTime)

	if g.controller.Recording() {
		adoptOrMergeDigest(&g.taskStartLatenciesSec, res.TaskStartLatenciesSec)
		adoptOrMergeDigest(&g.taskDurationsSec, res.TaskDurationsSec)
		adoptOrMergeDigest(&g.funnelStartLatenciesSec, res.FunnelStartLatenciesSec)
		adoptOrMergeDigest(&g.funnelDurationsSec, res.FunnelDurationsSec)

		addToDigest(&g.funnelAgesSec, res.FunnelAge.Seconds(), 1.0)
		addToDigest(&g.funnelCounts, res.FunnelStartLatenciesSec.Count(), 1.0)
		addToDigest(&g.skimStartLatenciesSec, skimStartTime.Sub(res.FlushStartTime.Add(res.FlushDuration)).Seconds(), 1.0)
		addToDigest(&g.skimDurationsSec, skimDuration.Seconds(), 1.0)
	}

	return err
}
