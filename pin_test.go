// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestPinFlowRetention: a pinned ctx is an ordinary retainable Go context —
// flow values stay readable after the source scope has exited, follow-ups
// wait for the unpin (the pin is a carrier), fire inline at UnpinFlow, and
// their errors join UnpinFlow's return.
func TestPinFlowRetention(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()
	fireErr := errors.New("fire error")

	var fired atomic.Bool
	var pinned context.Context
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = streampool.PinFlow(ctx)
		return nil
	}, key.Value("kept"), tag.FollowUpFn(func(context.Context) error {
		fired.Store(true)
		return fireErr
	})))

	// The scope has exited; the pin alone holds the flow.
	chk.False(fired.Load(), "the pin is a carrier: the follow-up waits for the unpin")
	v, ok := key.From(pinned)
	chk.True(ok, "flow values stay readable on the pinned ctx")
	chk.Equal("kept", v)
	chk.True(tag.InFlow(pinned))

	// The pinned ctx carries no cancellation or deadline (Background root).
	chk.NoError(pinned.Err())
	_, hasDeadline := pinned.Deadline()
	chk.False(hasDeadline)

	err := streampool.UnpinFlow(pinned)
	chk.True(fired.Load(), "the last carrier's release fires the follow-up inline")
	chk.ErrorIs(err, fireErr, "inline fire errors join UnpinFlow's return")
}

// TestPinFlowDispatch: dispatch from a pinned ctx after the source extent is
// gone — an ordinary top-level submission into an explicitly named wave. The
// body reads the pinned flow's values, and the follow-up waits for BOTH the
// dispatched work and the unpin.
func TestPinFlowDispatch(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()
	tag := streampool.NewFlowTag()

	var fired atomic.Bool
	var pinned context.Context
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = streampool.PinFlow(ctx)
		return nil
	}, key.Value("carried"), tag.FollowUpFn(func(context.Context) error {
		fired.Store(true)
		return nil
	})))

	wave := streampool.NewWave()
	var bodySaw atomic.Value
	release := make(chan struct{})
	task := streampool.NewTaskLauncher(func(ctx context.Context) error {
		<-release
		v, ok := key.From(ctx)
		bodySaw.Store([2]any{v, ok})
		return nil
	})
	chk.NoError(task.In(wave).Start(pinned))

	// The pin released while the dispatched body still runs: the work's own
	// carrier refs hold the flow, so the follow-up waits for the body too.
	chk.NoError(streampool.UnpinFlow(pinned))
	chk.False(fired.Load(), "follow-up waits for work dispatched from the pin")

	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
	chk.Equal([2]any{"carried", true}, bodySaw.Load(), "the body inherits the pinned flow")
	chk.Eventually(fired.Load, 5*time.Second, 2*time.Millisecond,
		"the follow-up fires once pin and work have both released")
}

// TestPinFlowRequiresExplicitWave: a pin carries no ambient wave — dispatch
// without op.In(wave) panics exactly as from a bare top-level ctx.
func TestPinFlowRequiresExplicitWave(t *testing.T) {
	chk := require.New(t)
	pinned := streampool.PinFlow(context.Background())
	defer func() { chk.NoError(streampool.UnpinFlow(pinned)) }()

	task := streampool.NewTaskLauncher(func(context.Context) error { return nil })
	chk.PanicsWithValue(
		"op constructed with nil wave dispatched without op.In(wave) and outside any wave body",
		func() { _ = task.Start(pinned) })
}

// TestUnpinFlowValidation: UnpinFlow requires the exact token — a non-pin, a
// derivative, and a double unpin all panic. While the expired pin's meta is
// still held (work dispatched from it is in flight), every framework entry
// (PinFlow, WithFlow, dispatch) panics on it; once the meta recycles,
// detection is impossible — the documented residual — so the deterministic
// window here is created by an in-flight task.
func TestUnpinFlowValidation(t *testing.T) {
	chk := require.New(t)

	chk.Panics(func() { _ = streampool.UnpinFlow(context.Background()) }, "non-pin")

	pinned := streampool.PinFlow(context.Background())
	type k struct{}
	derived := context.WithValue(pinned, k{}, 1)
	chk.Panics(func() { _ = streampool.UnpinFlow(derived) }, "derivative is not the token")

	// Keep the pin meta alive past the unpin: an in-flight task's borrow
	// chain refs it, so the expired marker stays resolvable.
	wave := streampool.NewWave()
	release := make(chan struct{})
	holder := streampool.NewTaskLauncher(func(context.Context) error { <-release; return nil })
	chk.NoError(holder.In(wave).Start(pinned))

	chk.NoError(streampool.UnpinFlow(pinned))
	chk.Panics(func() { _ = streampool.UnpinFlow(pinned) }, "double unpin")

	chk.Panics(func() { streampool.PinFlow(pinned) }, "re-pin after unpin cannot resurrect")
	chk.Panics(func() {
		_ = streampool.WithFlow(pinned, func(context.Context) error { return nil },
			streampool.NewFlowKey[int]().Value(1))
	}, "scope over an expired pin")
	task := streampool.NewTaskLauncher(func(context.Context) error { return nil })
	chk.Panics(func() { _ = task.In(wave).Start(pinned) }, "dispatch from an expired pin")

	close(release)
	chk.NoError(wave.CloseAndSkimAll(context.Background()))
}

// TestPinFlowCompose: pins compose — pinning a pinned ctx mints a new,
// independent pin with no extent-window precondition, which is the handoff
// idiom (overlap, then release). The flow ends at the last release; the
// follow-up fires exactly once.
func TestPinFlowCompose(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[int]()
	tag := streampool.NewFlowTag()

	var fires atomic.Int32
	var p1 context.Context
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		p1 = streampool.PinFlow(ctx)
		return nil
	}, key.Value(42), tag.FollowUpFn(func(context.Context) error {
		fires.Add(1)
		return nil
	})))

	p2 := streampool.PinFlow(p1) // handoff: overlap…
	chk.NoError(streampool.UnpinFlow(p1))
	chk.Equal(int32(0), fires.Load(), "p2 still carries the flow")

	v, ok := key.From(p2)
	chk.True(ok)
	chk.Equal(42, v, "values survive the handoff")

	chk.NoError(streampool.UnpinFlow(p2))
	chk.Equal(int32(1), fires.Load(), "the flow ends at the last release, firing once")
}

// TestPinFlowDegenerate: pinning a bare, flow-less ctx is the allowed
// degenerate case — a pin of the empty flow.
func TestPinFlowDegenerate(t *testing.T) {
	chk := require.New(t)
	pinned := streampool.PinFlow(context.Background())
	_, ok := streampool.NewFlowKey[int]().From(pinned)
	chk.False(ok)
	chk.NoError(streampool.UnpinFlow(pinned))
}

// TestPinFlowConcurrentDispatch: one pinned ctx shared by many goroutines,
// each dispatching into its own wave — the pinned meta is immutable and every
// dispatch mints its own per-dispatch state, so this is race-free by design.
func TestPinFlowConcurrentDispatch(t *testing.T) {
	chk := require.New(t)
	key := streampool.NewFlowKey[string]()

	var pinned context.Context
	chk.NoError(streampool.WithFlow(context.Background(), func(ctx context.Context) error {
		pinned = streampool.PinFlow(ctx)
		return nil
	}, key.Value("shared")))

	const workers = 8
	var misses atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wave := streampool.NewWave()
			task := streampool.NewTaskLauncher(func(ctx context.Context) error {
				if v, ok := key.From(ctx); !ok || v != "shared" {
					misses.Add(1)
				}
				return nil
			})
			for j := 0; j < 4; j++ {
				if err := task.In(wave).Start(pinned); err != nil {
					misses.Add(1)
				}
			}
			//nolint:contextcheck // a fresh top-level drive ctx per goroutine, by design
			if err := wave.CloseAndSkimAll(context.Background()); err != nil {
				misses.Add(1)
			}
		}()
	}
	wg.Wait()
	chk.Zero(misses.Load())
	chk.NoError(streampool.UnpinFlow(pinned))
}
