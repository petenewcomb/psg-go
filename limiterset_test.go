// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestMultiLimiter_JointAdmissionHoldsBoth proves an op bound to two limiters acquires
// BOTH jointly: while a body bound to A+B runs (holding one permit from each cap-1
// limiter), an op needing only A cannot dispatch AND an op needing only B cannot dispatch.
// If the joint body held just one of them, the other's TrySubmit would succeed — so both
// failing is the joint-admission proof.
func TestMultiLimiter_JointAdmissionHoldsBoth(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	wave := streampool.NewWave()

	a := streampool.NewSemaphore(1)
	b := streampool.NewSemaphore(1)

	started := make(chan struct{})
	release := make(chan struct{})
	both := streampool.NewTaskLauncher(func(context.Context) error {
		close(started)
		<-release
		return nil
	}).WithLimits(a, b).In(wave)
	onlyA := streampool.NewTaskLauncher(func(context.Context) error { return nil }).WithLimits(a).In(wave)
	onlyB := streampool.NewTaskLauncher(func(context.Context) error { return nil }).WithLimits(b).In(wave)

	chk.NoError(both.Start(ctx))
	<-started // the joint body now holds a permit from A and from B

	soon := time.Now().Add(50 * time.Millisecond)
	okA, err := onlyA.TryStart(ctx, soon)
	chk.NoError(err)
	chk.False(okA, "A is held by the joint body")
	okB, err := onlyB.TryStart(ctx, soon)
	chk.NoError(err)
	chk.False(okB, "B is held by the joint body — so the joint body holds BOTH")

	close(release)
	chk.NoError(wave.CloseAndSkimAll(ctx))
}
