// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"

	"github.com/stretchr/testify/assert"
)

func TestNewLauncherNilTaskPanic(t *testing.T) {
	chk := assert.New(t)
	_, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	chk.PanicsWithValue("handler must be non-nil", func() {
		// Nil handler should panic at construction.
		psg.NewLauncher[struct{}](wave, nil)
	})
}

func TestSkimScatterNilSkimPanic(t *testing.T) {
	chk := assert.New(t)
	_, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()
	chk.PanicsWithValue("handler must be non-nil", func() {
		psg.NewSkimmer[int](wave, nil)
	})
}

// Nil-sentinel resolution: a Skimmer constructed with nil wave is
// wave-independent. At dispatch, the wave is resolved from the
// ctx, which descends from a NewWave call.
func TestSkimmerNilWaveResolvesFromCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	var got int
	skimmer := psg.NewFnSkimmer(nil,
		func(_ context.Context, v int, err error) error {
			chk.NoError(err)
			got = v
			return nil
		},
	)
	chk.NoError(skimmer.Submit(ctx, 42))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(42, got)
}

// Dispatching a nil-wave Skimmer on a ctx with no wave panics.
func TestSkimmerNilWaveDispatchWithoutCtxWavePanics(t *testing.T) {
	chk := assert.New(t)
	skimmer := psg.NewFnSkimmer(nil,
		func(_ context.Context, _ int, _ error) error { return nil },
	)
	chk.PanicsWithValue(
		"op constructed with nil wave dispatched from a ctx with no wave (call NewWave first)",
		func() { _ = skimmer.Submit(context.Background(), 1) },
	)
}

// Same nil-sentinel resolution for Launcher0.
func TestLauncherNilWaveResolvesFromCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	ran := false
	runner := psg.NewTaskLauncher(nil, func(_ context.Context) error {
		ran = true
		return nil
	})
	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.True(ran)
}

// Nil-wave Skimmer dispatched from inside a task body resolves to
// the dispatching wave via the worker plumbing: taskWork carries
// the dispatching wave and Execute stamps it onto the worker's
// ctxMeta around task.Run.
func TestSkimmerNilWaveResolvesFromTaskBodyCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	var got int
	skimmer := psg.NewFnSkimmer(nil,
		func(_ context.Context, v int, err error) error {
			chk.NoError(err)
			got = v
			return nil
		},
	)
	runner := psg.NewTaskLauncher(wave, func(taskCtx context.Context) error {
		return skimmer.Submit(taskCtx, 99)
	})
	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(99, got)
}

// Nil-wave dispatch from inside a Funnel psg.Accumulator body. Verifies
// that the ctx reaching Accumulate carries the dispatching wave so
// downstream nil-wave ops resolve.
func TestSkimmerNilWaveResolvesFromAccumulateBodyCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	var got int
	downstream := psg.NewFnSkimmer(nil,
		func(_ context.Context, v int, err error) error {
			chk.NoError(err)
			got = v
			return nil
		},
	)
	funnelPool := psg.NewFunnelPool(wave.Pool())
	funnel := psg.NewFunnel(funnelPool, psg.NewAccumulatorFactory(func() psg.Accumulator[int] {
		return psg.FuncAccumulator[int]{
			AccumulateFn: func(accCtx context.Context, v int, _ error) (time.Time, error) {
				return time.Time{}, downstream.Submit(accCtx, v+1)
			},
		}
	}, nil))
	defer funnel.Close()
	chk.NoError(funnel.Submit(ctx, 100))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(101, got)
}

// Thread C: TrySubmit with zero deadline fail-fasts when the
// dispatch path is contended.
func TestTrySubmitZeroDeadlineFailFast(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	// Saturate a limiter so the next TrySubmit can't dispatch
	// immediately.
	limit := psg.NewSemaphore(1)
	blocking := make(chan struct{})
	released := make(chan struct{})
	runner := psg.NewTaskLauncher(wave, func(_ context.Context) error {
		close(blocking)
		<-released
		return nil
	}, psg.WithLimits(limit))
	chk.NoError(runner.Start(ctx))
	<-blocking // first task is now occupying the limiter permit

	// Zero deadline → fail-fast.
	contender := psg.NewTaskLauncher(wave, func(_ context.Context) error {
		return nil
	}, psg.WithLimits(limit))
	ok, err := contender.TryStart(ctx, time.Time{})
	chk.False(ok, "TryStart with zero deadline should fail-fast when contended")
	chk.NoError(err, "fail-fast should not return an error")

	close(released)
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

// Thread C: Submit (non-Try) uses Forever internally; passes
// through the dispatch path as the "block until success" sentinel.
// Contended Submit (via limiter) blocks and then succeeds when the
// limiter is freed — verifies the Forever path through Pool.block.
func TestSubmitBlocksOnContendedLimiter(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	limit := psg.NewSemaphore(1)
	blocking := make(chan struct{})
	released := make(chan struct{})
	runner := psg.NewTaskLauncher(wave, func(_ context.Context) error {
		close(blocking)
		<-released
		return nil
	}, psg.WithLimits(limit))
	chk.NoError(runner.Start(ctx))
	<-blocking

	contended := false
	contenderRan := make(chan struct{})
	go func() {
		contender := psg.NewTaskLauncher(wave, func(_ context.Context) error {
			return nil
		}, psg.WithLimits(limit))
		err := contender.Start(ctx) // uses Forever internally
		contended = err == nil
		close(contenderRan)
	}()

	time.Sleep(20 * time.Millisecond)
	close(released)
	<-contenderRan
	chk.True(contended, "Submit should block then succeed")

	chk.NoError(wave.CloseAndSkimAll(ctx))
}

// NewErrSkimmer convenience constructor + SubmitErr — exercises
// the named-intent err sink shape end-to-end.
func TestNewErrSkimmer(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	wantErr := errors.New("propagate me")
	var got error
	sink := psg.NewErrSkimmer(wave, func(_ context.Context, err error) error {
		got = err
		return nil
	})
	var _ psg.ErrSkimmer = sink //nolint:staticcheck // intentional alias type-check
	chk.NoError(sink.SubmitErr(ctx, wantErr))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.ErrorIs(got, wantErr)
}

// NewTaskLauncher convenience constructor + Start — exercises the
// no-arg launcher shape end-to-end.
func TestNewTaskLauncher(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	ran := false
	runner := psg.NewTaskLauncher(wave, func(_ context.Context) error {
		ran = true
		return nil
	})
	var _ psg.TaskLauncher = runner //nolint:staticcheck // intentional alias type-check
	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.True(ran)
}

// NewErrLauncher convenience constructor + SubmitErr — exercises
// the worker-side err handling shape end-to-end.
func TestNewErrLauncher(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	wantErr := errors.New("propagate me")
	var got error
	sink := psg.NewErrLauncher(wave, func(_ context.Context, err error) error {
		got = err
		return nil
	})
	var _ psg.ErrLauncher = sink //nolint:staticcheck // intentional alias type-check
	chk.NoError(sink.SubmitErr(ctx, wantErr))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.ErrorIs(got, wantErr)
}

// Err-only sink via psg.ErrHandler + SubmitErr(ctx, err) — verifies
// the err-only convenience pair lands cleanly.
func TestSkimmerErrHandlerErrOnlySink(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	wantErr := errors.New("propagate me")
	var got error
	errSink := psg.NewErrSkimmer(wave,
		func(_ context.Context, err error) error {
			got = err
			return nil
		},
	)
	chk.NoError(errSink.SubmitErr(ctx, wantErr))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.ErrorIs(got, wantErr)
}

// Launcher's err-only dispatch: psg.Task adapter + SubmitErr should
// short-circuit (psg.Task.Handle returns err immediately when non-nil),
// so the task body never runs.
func TestLauncherTaskShortCircuitsOnSubmitErr(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	bodyRan := false
	runner := psg.NewTaskLauncher(wave, func(_ context.Context) error {
		bodyRan = true
		return nil
	})
	chk.NoError(runner.SubmitErr(ctx, errors.New("propagated")))
	// CloseAndSkimAll should surface the propagated err.
	err := wave.CloseAndSkimAll(ctx)
	chk.Error(err)
	chk.False(bodyRan, "psg.Task body must not run when err is non-nil")
}

// Nil-wave dispatch from inside a Skimmer handler body. Verifies
// that the ctx reaching the handler still carries the wave so
// downstream nil-wave ops resolve.
func TestSkimmerNilWaveResolvesFromSkimBodyCtx(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	var got int
	downstream := psg.NewFnSkimmer(nil,
		func(_ context.Context, v int, err error) error {
			chk.NoError(err)
			got = v
			return nil
		},
	)
	upstream := psg.NewFnSkimmer(wave,
		func(skimCtx context.Context, v int, _ error) error {
			return downstream.Submit(skimCtx, v*2)
		},
	)
	chk.NoError(upstream.Submit(ctx, 21))
	chk.NoError(wave.CloseAndSkimAll(ctx))
	chk.Equal(42, got)
}

func TestLauncherStartFromTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewFnSkimmer(wave,
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	innerRunner := psg.NewTaskLauncher(wave, func(ctx context.Context) error {
		chk.Fail("should not get here")
		return nil
	})
	outerRunner := psg.NewTaskLauncher(wave, func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return skimmer.Submit(ctx, 0)
	})
	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCanStartTaskInSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	// Variable to track execution flow
	subJobTaskRan := false

	skimmer := psg.NewFnSkimmer(parentWave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		// Create a sub-wave inside the task
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		// This should succeed - dispatching a task to the sub-wave's pool
		subSkimmer := psg.NewFnSkimmer(subWave,
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskLauncher(subWave, func(ctx context.Context) error {
			subJobTaskRan = true
			return subSkimmer.Submit(ctx, true)
		})
		chk.NoError(subRunner.Start(subCtx))

		// Skim all results in the sub-wave
		chk.NoError(subWave.CloseAndSkimAll(subCtx))

		return skimmer.Submit(ctx, true)
	})

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))

	// Verify the sub-wave task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-wave should have run")
}

func TestTaskCannotStartTaskOnParentPool(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewFnSkimmer(parentWave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	innerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		chk.Fail("should not get here - parent pool task should not run")
		return nil
	})
	outerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		chk.PanicsWithValue(
			"Start called from task context but allowed only by top-level, skim, or funnel context",
			func() {
				_ = innerRunner.Start(ctx)
			},
		)
		return skimmer.Submit(ctx, true)
	})

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkim(t *testing.T) {
	chk := assert.New(t)
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait()

	skimmer := psg.NewFnSkimmer(wave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	runner := psg.NewTaskLauncher(wave, func(ctx context.Context) error {
		chk.PanicsWithValue("Skim called from task context but allowed only by top-level or skim context", func() {
			_, _ = wave.TrySkim(ctx)
		})
		return skimmer.Submit(ctx, true)
	})

	chk.NoError(runner.Start(ctx))
	chk.NoError(wave.CloseAndSkimAll(ctx))
}

func TestTaskCannotSkimParentJob(t *testing.T) {
	chk := assert.New(t)
	ctx, parentWave := psg.NewWave(context.Background())
	defer parentWave.CancelAndWait()

	skimmer := psg.NewFnSkimmer(parentWave,
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	outerRunner := psg.NewTaskLauncher(parentWave, func(ctx context.Context) error {
		subCtx, subWave := psg.NewWave(ctx)
		defer subWave.CancelAndWait()

		subSkimmer := psg.NewFnSkimmer(subWave,
			func(ctx context.Context, result bool, err error) error {
				chk.NoError(err)
				chk.True(result)
				return nil
			},
		)
		subRunner := psg.NewTaskLauncher(subWave, func(ctx context.Context) error {
			chk.PanicsWithValue("Context belongs to a child job", func() {
				_, _ = parentWave.TrySkim(ctx)
			})
			return subSkimmer.Submit(ctx, true)
		})
		chk.NoError(subRunner.Start(subCtx))
		chk.NoError(subWave.CloseAndSkimAll(subCtx))
		return skimmer.Submit(ctx, true)
	})

	chk.NoError(outerRunner.Start(ctx))
	chk.NoError(parentWave.CloseAndSkimAll(ctx))
}
