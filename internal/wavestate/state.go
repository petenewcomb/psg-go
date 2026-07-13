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
	doneChan        chan struct{}

	// onFlushing, if non-nil, is invoked synchronously each time the wave enters (or
	// re-enters) the Flushing stage with references still outstanding — the signal to
	// force pending funnel flushes. The
	// wave wires it to an enqueue-only sweep that pushes flush work to the global pool.
	// It must not block or run user code (it runs inside the work-completion path).
	onFlushing func()

	// onDone, if non-nil, is invoked exactly once on the Flushing→Done transition (the
	// goroutine that drops the last reference). The wave wires it to releaseCaches —
	// dropping its self-ref on each permit cache it created. Like onFlushing it runs
	// inside the work-completion path: it must not block or run user code.
	onDone func()
}

// Init initializes an uninitialized WaveState to the Open stage, and must be
// called exactly once before any other methods (and again to re-arm a drained
// state). An Init method is provided instead of a New function because WaveState
// is expected to be an embedded field of Wave. onFlushing and onDone (either may be
// nil) are invoked on the Closed→Flushing and Flushing→Done transitions respectively;
// see the fields.
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) Init(onFlushing, onDone func()) {
	traceRegion := "WaveState.Init"
	trace.Logf(context.Background(), traceRegion,
		"WaveState=%p, inFlightWork=%p, totalReferences=%p",
		ws, &ws.inFlightWork, &ws.totalReferences)

	ws.currentStage.Store(int32(stageOpen))
	ws.onFlushing = onFlushing
	ws.onDone = onDone
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

// TryIncrementReference adds a non-work reference only if the wave has not reached
// (or irrevocably committed to) Done, reporting success. It is the pin a caller must
// use when it needs to hold open a wave whose liveness it cannot otherwise
// guarantee — e.g. the suspend bracket and the flush sweep, which pin the very wave
// they are driving to Done. A plain IncrementReference cannot serve there: an
// increment that resurrects the count from zero cannot stop a Flushing→Done
// transition already in flight on the goroutine that dropped the last reference (the
// Done CAS and the onDone cache teardown would run regardless), so the "pinned"
// wave's forest could be torn down under the pinner. The pin therefore serializes
// against the transition through the counter itself: noMoreReferences claims the
// zero count before committing Done, a claimed counter refuses pins, and a pin that
// lands first (including on an idle-but-open wave, where holding the wave open is
// exactly the point) makes the claim fail — the pin's own release re-triggers the
// transition. Failure means Done is reached or committed: there is nothing left to
// drive, and the caller must not touch the wave's forest.
func (ws *WaveState) TryIncrementReference() bool {
	if !ws.totalReferences.IncrementUnlessClaimed() {
		return false
	}
	if ws.IsDone() {
		// The transition fully completed before our increment landed (a claim no
		// longer excludes us once released). Back the pin out; the zero-crossing
		// re-runs noMoreReferences, which no-ops on a Done stage.
		ws.DecrementReference()
		return false
	}
	return true
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

// Close attempts to transition from Open to Closed, reporting whether THIS call
// performed the transition (so the caller can run once-only close side effects, e.g.
// dropping the wave's owner reference).
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) Close() bool {
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
	return swapped
}

// Done returns the channel that will be closed when the wave transitions to Done
func (ws *WaveState) Done() <-chan struct{} {
	return ws.doneChan
}

// PanicIfDone panics if the wave is in the done stage
func (ws *WaveState) PanicIfDone() {
	if lifecycleStage(ws.currentStage.Load()) == stageDone {
		panic("wave is closed and no longer running")
	}
}

// IsDone reports whether the wave has reached the Done stage. It reads only the
// atomic stage, so it is safe on a zero-value (uninitialized) WaveState — which
// reads as Open (stageOpen == 0) — letting a Wave's lazy init distinguish a fresh
// zero value from one whose prior cycle has fully drained (and so must re-arm).
func (ws *WaveState) IsDone() bool {
	return lifecycleStage(ws.currentStage.Load()) == stageDone
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

	// Signal the flush sweep for the flushing state. The wave's callback is an
	// enqueue-only sweep (push pending funnel flushes to the global pool); it fires on
	// each Flushing entry/re-entry.
	if currentStage == stageFlushing && ws.onFlushing != nil {
		ws.onFlushing()
	}
}

// noMoreReferences attempts to transition from Flushing to Done
//
//nolint:contextcheck // background context used only for tracing
func (ws *WaveState) noMoreReferences() {
	traceRegion := "WaveState.noMoreReferences"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "WaveState=%p", ws)

	// Only a Flushing wave can advance; any other stage makes this zero-crossing a
	// no-op (an Open/Closed wave draining to zero is idle, not done). Checked before
	// claiming so the transient claim never appears on a wave that cannot advance —
	// a TryIncrementReference pin on an idle wave must not be refused.
	if lifecycleStage(ws.currentStage.Load()) != stageFlushing {
		return
	}
	// Claim the zero count before committing Done: this is the serialization against
	// TryIncrementReference. A pin (or misuse-class late increment) that landed first
	// makes the claim fail — the wave stays Flushing on that reference, and its
	// release re-triggers this transition.
	if !ws.totalReferences.ClaimZero() {
		return
	}
	var swapped bool
	trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(flushing, done)", func() {
		swapped = ws.currentStage.CompareAndSwap(int32(stageFlushing), int32(stageDone))
	})
	if swapped {
		// Successfully changed from Flushing to Done. Run onDone (the cache teardown)
		// BEFORE closing doneChan: the close releases a CloseAndSkimAll waiter, which may
		// immediately re-arm the wave (initState → Init re-writes onDone and the substrate),
		// racing both the onDone field read here and the teardown itself. Running it first
		// keeps the whole Done transition strictly before any reuse. The claim is held
		// across the teardown (pins fail throughout) and released BEFORE the close: a
		// re-arm racing a still-standing claim would refuse the new cycle's pins.
		if ws.onDone != nil {
			ws.onDone()
		}
		ws.totalReferences.ReleaseClaim()
		trace.WithRegion(context.Background(), traceRegion+".close(ws.doneChan)", func() {
			close(ws.doneChan)
		})
		return
	}
	ws.totalReferences.ReleaseClaim()
}
