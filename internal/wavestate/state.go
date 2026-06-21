// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package wavestate

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/trace"
)

// lifecycleStage represents the possible stages in a wave's lifecycle
type lifecycleStage int32

//go:generate go run golang.org/x/tools/cmd/stringer@v0.35.0 -type=lifecycleStage -linecomment
const (
	// stageOpen indicates that the wave is accepting new tasks
	stageOpen lifecycleStage = iota // Open
	// stageClosed indicates that the wave is closed for new tasks but
	// existing tasks continue to run
	stageClosed // Closed
	// stageFlushing indicates that all tasks have completed and the wave
	// is waiting for funnels to finish
	stageFlushing // Flushing
	// stageDone indicates that the wave is completely done, all tasks and
	// funnels have completed
	stageDone // Done
)

// WaveState encapsulates the state management for a scatter-gather wave
type WaveState struct {
	currentStage    atomic.Int32    // Contains a lifecycleStage value
	inFlightWork    InFlightCounter // Tracks only executing work
	totalReferences InFlightCounter // Tracks both work and funnels
	nextFlushChan   atomic.Value    // Stores chan struct{} for flush signals
	doneChan        chan struct{}
	flushListener   atomic.Value // Stores func() callback for flush events
}

// Init initializes an uninitialized WaveState to the Open stage, and must be
// called exactly once before any other methods. An Init method is provided
// instead of a New function because WaveState is expected to be an embedded
// field of Wave.
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) Init() {
	traceRegion := "WaveState.Init"
	trace.Logf(context.Background(), traceRegion,
		"WaveState=%p, inFlightWork=%p, totalReferences=%p",
		ws, &ws.inFlightWork, &ws.totalReferences)

	ws.currentStage.Store(int32(stageOpen))
	ws.nextFlushChan.Store(make(chan struct{}))
	ws.doneChan = make(chan struct{})
}

// IncrementWork increments both the work counter and total references counter
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) IncrementWork() {
	traceRegion := "WaveState.IncrementWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)
	ws.totalReferences.Increment()
	ws.inFlightWork.Increment()
}

// DecrementWork decrements the work counter and attempts stage transitions if needed
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) DecrementWork() {
	traceRegion := "WaveState.DecrementWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	// Decrement the total references count and check if it hit zero. If noMoreWork is true,
	// ws.noMoreWork will handle the transition logic. But if noMoreWork is false
	// and the total references counter hit zero, we need to call ws.noMoreReferences directly to
	// handle the race condition where flushers complete between the decrement and
	// the IsZero() check in noMoreWork().
	//
	// Further, decrementing the total references counter first ensures that if
	// both transition to zero then there's no gap within which a separate
	// goroutine might observe zero work and non-zero references and thus
	// trigger an unnecessary extra flush.
	noMoreReferences := ws.totalReferences.Decrement()

	noMoreWork := ws.inFlightWork.Decrement()

	if noMoreWork {
		// Last work just completed.
		ws.noMoreWork()
	} else if noMoreReferences {
		// Total references counter hit zero but work counter didn't - this means flushers completed
		ws.noMoreReferences()
	}
}

// IncrementReference adds a non-work reference to the wave. A funnel
// instance holds exactly one such reference for its live lifetime
// (from accumulator allocation until flush) so the wave cannot
// transition to Done while any accumulator is still unflushed —
// independent of which worker ultimately runs the flush. Unlike
// [WaveState.IncrementWork] it does not touch the in-flight-work
// counter, so it does not gate the Closed → Flushing transition (a
// live-but-idle instance must not keep the wave out of Flushing; only
// genuine in-flight work does).
func (ws *WaveState) IncrementReference() {
	ws.totalReferences.Increment()
}

// DecrementReference drops a reference added by [WaveState.IncrementReference],
// advancing the wave to Done if it was the last outstanding reference.
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) DecrementReference() {
	traceRegion := "WaveState.DecrementReference"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	if ws.totalReferences.Decrement() {
		// Last reference just completed (work or funnel instance)
		ws.noMoreReferences()
	}
}

// FlushChan returns the channel that is closed the next time all
// in-flight work drains to zero and the wave enters (or re-enters) the
// Flushing stage — the signal for funnel workers to force their
// pending flushes. It adds no reference: the flush barrier is carried
// per-instance via [WaveState.IncrementReference] /
// [WaveState.DecrementReference]. The channel is rotated on each
// Flushing cycle (see [WaveState.noMoreWork]), so a worker re-reads it
// after handling a signal to wait for the next cycle.
func (ws *WaveState) FlushChan() <-chan struct{} {
	return ws.nextFlushChan.Load().(chan struct{})
}

// Close attempts to transition from Open to Closed.
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) Close() {
	traceRegion := "WaveState.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	var swapped bool
	trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(open, closed)", func() {
		swapped = ws.currentStage.CompareAndSwap(int32(stageOpen), int32(stageClosed))
	})
	if swapped {
		// Successfully changed from Open to Closed
		if ws.inFlightWork.IsZero() {
			ws.noMoreWork()
		}
	}
}

// Done returns the channel that will be closed when the wave transitions to Done
func (ws *WaveState) Done() <-chan struct{} {
	return ws.doneChan
}

// SetFlushListener sets the function to be called when all work has completed
// and the wave is waiting for funnels to emit their results. Pass nil to remove
// any existing listener.
func (ws *WaveState) SetFlushListener(fn func()) {
	ws.flushListener.Store(fn)
}

// PanicIfDone panics if the wave is in the done stage
func (ws *WaveState) PanicIfDone() {
	if lifecycleStage(ws.currentStage.Load()) == stageDone {
		panic("wave is closed and no longer running")
	}
}

// noMoreWork attempts to transition from Closed to Flushing, and will also
// advance to Done by calling noMoreReferences if appropriate
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) noMoreWork() {
	traceRegion := "WaveState.noMoreWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	currentStage := lifecycleStage(ws.currentStage.Load())

	// Try to transition from Closed to Flushing if needed
	if currentStage == stageClosed {
		var swapped bool
		trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(closed, flushing)", func() {
			swapped = ws.currentStage.CompareAndSwap(int32(stageClosed), int32(stageFlushing))
		})
		if swapped {
			currentStage = stageFlushing
		}
	}

	// If totalReferences is zero, there is nothing left to do.
	if ws.totalReferences.IsZero() {
		ws.noMoreReferences()
		return
	}

	// Handle flush channel for flushing state
	if currentStage == stageFlushing {
		// Call the flushListener callback if set (before closing the channel)
		if fn, ok := ws.flushListener.Load().(func()); ok && fn != nil {
			trace.WithRegion(context.Background(), traceRegion+".flushListener", fn)
		}

		// Create new channel and swap with old one
		newCh := make(chan struct{})
		oldCh := ws.nextFlushChan.Swap(newCh).(chan struct{})
		// Close old channel after replacing it
		close(oldCh)
	}
}

// noMoreReferences attempts to transition from Flushing to Done
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) noMoreReferences() {
	traceRegion := "WaveState.noMoreReferences"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	var swapped bool
	trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(flushing, done)", func() {
		swapped = ws.currentStage.CompareAndSwap(int32(stageFlushing), int32(stageDone))
	})
	if swapped {
		// Successfully changed from Flushing to Done
		trace.WithRegion(context.Background(), traceRegion+".close(ws.doneChan)", func() {
			close(ws.doneChan)
		})
	}
}
