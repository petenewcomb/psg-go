// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go"
)

type Skimmer[T any] struct {
	controller *Controller
	wrappedFn  psg.HandlerFunc[T]

	taskStartLatenciesSec *tdigest.TDigest
	taskDurationsSec      *tdigest.TDigest

	skimStartLatenciesSec *tdigest.TDigest
	skimDurationsSec      *tdigest.TDigest

	skimFn psg.HandlerFunc[TaskResult[T]]
}

func NewSkimmer[T any](c *Controller, skimFn psg.HandlerFunc[T]) {
	// TODO: pool
	g := &Skimmer[T]{
		controller: c,
		wrappedFn:  skimFn,
	}
	g.skimFn = g.skim
}

func (g *Skimmer[T]) SkimFn() psg.HandlerFunc[TaskResult[T]] {
	if g.skimFn == nil {
		g.skimFn = g.skim
	}
	return g.skimFn
}

func (g *Skimmer[T]) skim(ctx context.Context, res TaskResult[T], err error) error {
	skimStartTime := time.Now()
	err = g.wrappedFn(ctx, res.Value, err)
	skimDuration := time.Since(skimStartTime)

	if g.controller.Recording() {
		addToDigest(&g.taskStartLatenciesSec, res.StartLatency.Seconds(), 1.0)
		addToDigest(&g.taskDurationsSec, res.Duration.Seconds(), 1.0)

		addToDigest(&g.skimStartLatenciesSec, skimStartTime.Sub(res.StartTime.Add(res.Duration)).Seconds(), 1.0)
		addToDigest(&g.skimDurationsSec, skimDuration.Seconds(), 1.0)
	}

	return err
}
