// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go/psgfn"
)

type Gatherer[T any] struct {
	controller *Controller
	wrappedFn  psgfn.Gather[T]

	taskStartLatenciesSec *tdigest.TDigest
	taskDurationsSec      *tdigest.TDigest

	gatherStartLatenciesSec *tdigest.TDigest
	gatherDurationsSec      *tdigest.TDigest

	gatherFn psgfn.Gather[TaskResult[T]]
}

func NewGatherer[T any](c *Controller, gatherFn psgfn.Gather[T]) {
	// TODO: pool
	g := &Gatherer[T]{
		controller: c,
		wrappedFn:  gatherFn,
	}
	g.gatherFn = g.gather
}

func (g *Gatherer[T]) GatherFn() psgfn.Gather[TaskResult[T]] {
	if g.gatherFn == nil {
		g.gatherFn = g.gather
	}
	return g.gatherFn
}

func (g *Gatherer[T]) gather(ctx context.Context, res TaskResult[T], err error) error {
	gatherStartTime := time.Now()
	err = g.wrappedFn(ctx, res.Value, err)
	gatherDuration := time.Since(gatherStartTime)

	if g.controller.Recording() {
		addToDigest(&g.taskStartLatenciesSec, res.StartLatency.Seconds(), 1.0)
		addToDigest(&g.taskDurationsSec, res.Duration.Seconds(), 1.0)

		addToDigest(&g.gatherStartLatenciesSec, gatherStartTime.Sub(res.StartTime.Add(res.Duration)).Seconds(), 1.0)
		addToDigest(&g.gatherDurationsSec, gatherDuration.Seconds(), 1.0)
	}

	return err
}
