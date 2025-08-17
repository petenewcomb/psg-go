// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/heap"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
)

type activeCombinerMap struct {
	m map[combinerInstanceID]*flushDeadlineEntry
	h heap.Heap[*flushDeadlineEntry]
}

//nolint:contextcheck // background context used only for tracing
func (cm *activeCombinerMap) Push(c combinerFlusher, flushDeadline time.Time) {
	traceRegion := "activeCombinerMap.Push"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	id := c.InstanceID()
	e := cm.m[id]
	if e == nil {
		if cm.m == nil {
			cm.m = make(map[combinerInstanceID]*flushDeadlineEntry)
		}
		c.Ref() // lock held by pooledCombine.execute
		e = flushDeadlineEntryPool.Get()
		e.c = c
		cm.m[id] = e
	}
	e.flushDeadline = flushDeadline
	if flushDeadline.IsZero() {
		cm.h.Remove(e)
	} else {
		cm.h.Push(e)
	}
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"cm=%p, Combiner=%p, flushDeadline=%v, id=%v, len(m)=%d, h.Len()=%d",
			cm, c, flushDeadline, id, len(cm.m), cm.h.Len())
	}
}

//nolint:contextcheck // background context used only for tracing
func (cm *activeCombinerMap) NextToFlush() (combinerFlusher, time.Time) {
	traceRegion := "activeCombinerMap.NextToFlush"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "cm=%p, len(m)=%d, h.Len()=%d", cm, len(cm.m), cm.h.Len())
	}
	if cm.h.Len() == 0 {
		return nil, time.Time{}
	}
	// Peek at the earliest deadline
	e := cm.h.Peek()
	return e.c, e.flushDeadline
}

// Remove removes a combiner from both the map and the deadline heap
//
//nolint:contextcheck // background context used only for tracing
func (cm *activeCombinerMap) Remove(c combinerFlusher) {
	traceRegion := "activeCombinerMap.Remove"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	id := c.InstanceID()
	e := cm.m[id]
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"cm=%p, id=%v, len(m)=%d, h.Len()=%d, e=%v",
			cm, id, len(cm.m), cm.h.Len(), e)
	}
	if e != nil {
		_ = cm.h.Remove(e)
		delete(cm.m, id)
		flushDeadlineEntryPool.Put(e)
	}
}

func (cm *activeCombinerMap) Merge(other *activeCombinerMap) {
	traceRegion := "activeCombinerMap.Merge"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "cm=%p, other=%p", cm, other)
	}

	if cm.m == nil {
		cm.m = other.m
		cm.h = other.h
		return
	}

	for id, e := range other.m {
		if existing, ok := cm.m[id]; ok {
			// Update the flush deadline if needed
			if !e.flushDeadline.IsZero() && (existing.flushDeadline.IsZero() || e.flushDeadline.Before(existing.flushDeadline)) {
				existing.flushDeadline = e.flushDeadline
				cm.h.Push(existing)
			}
			e.c.Unref() // Must unref because we're not adopting the entry (nor flushing it)
			flushDeadlineEntryPool.Put(e)
		} else {
			// Otherwise, adopt the entry
			cm.m[id] = e
			if !e.flushDeadline.IsZero() {
				e.pos = 0 // Reset position since it was relative to a different heap
				cm.h.Push(e)
			}
		}
	}
}

func (cm *activeCombinerMap) FlushExcess(ctx context.Context, emitOutbox *workq.Outbox, maxRetentionCount int) {
	traceRegion := "activeCombinerMap.FlushExcess"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "cm=%p, len=%d", cm, len(cm.m))
	}

	// First iterate over the heap so that we preferentially flush the excess
	// combiners that are nearest their deadline.
	for cm.h.Len() > 0 {
		e := cm.h.Pop()
		c := e.c
		id := c.InstanceID()
		if c.InstanceCount() > maxRetentionCount {
			delete(cm.m, id)
			flushDeadlineEntryPool.Put(e)
			c.Flush(ctx, emitOutbox)
		}
	}

	// Then flush any remaining excess combiners that had no flush deadline
	for id, e := range cm.m {
		c := e.c
		if c.InstanceCount() > maxRetentionCount {
			delete(cm.m, id)
			flushDeadlineEntryPool.Put(e)
			c.Flush(ctx, emitOutbox)
		}
	}
}

func (cm *activeCombinerMap) FlushAll(ctx context.Context, emitOutbox *workq.Outbox) {
	traceRegion := "activeCombinerMap.FlushAll"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "cm=%p, len(m)=%d, h.Len()=%d", cm, len(cm.m), cm.h.Len())
	}
	cm.h.Reset()
	for _, e := range cm.m {
		if trace.IsEnabled() {
			trace.Logf(ctx, traceRegion, "Combiner=%p, flushDeadline=%v", e.c, e.flushDeadline)
		}
		c := e.c
		flushDeadlineEntryPool.Put(e)
		c.Flush(ctx, emitOutbox)
	}
	clear(cm.m)
}

type combinerFlusher interface {
	InstanceID() combinerInstanceID
	InstanceCount() int
	Ref()
	Unref()

	// Flush is assumed to also Unref()
	Flush(ctx context.Context, emitOutbox *workq.Outbox)
}

type flushDeadlineEntry struct {
	pos           int // Position in the deadline heap, 0 if not in heap
	c             combinerFlusher
	flushDeadline time.Time
}

// Less implements heap.Item interface
func (e *flushDeadlineEntry) Less(other *flushDeadlineEntry) bool {
	return e.flushDeadline.Before(other.flushDeadline)
}

// SetPosition implements heap.Item interface
func (e *flushDeadlineEntry) SetPosition(position int) {
	e.pos = position
}

// Position implements heap.Item interface
func (e *flushDeadlineEntry) Position() int {
	return e.pos
}

var flushDeadlineEntryPool = omnipool.For[flushDeadlineEntry]()
