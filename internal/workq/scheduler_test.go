// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestScheduler_Post_ExecutesWork drives the whole intake path: Post parks on the
// unbuffered incoming Handoff, block-as-demand spawns a scheduler worker, the worker
// pulls the work and admits/executes it via ExecuteOne. No worker exists at the start,
// so this also exercises demand-driven spawn.
func TestScheduler_Post_ExecutesWork(t *testing.T) {
	chk := require.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := NewScheduler()
	s.Acquire()
	defer s.Wait() // join workers after Release
	defer s.Release()

	done := make(chan struct{})
	work := newWorkItem(func(_ context.Context, ex Execution) error {
		ex.Starting()
		close(done)
		return nil
	})

	chk.NoError(s.Post(ctx, work))

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("posted work was not executed")
	}
}

// TestScheduler_Post_Multiple confirms several posted items all run (the worker loops
// Wait→Work over successive ExecuteOne calls, and demand spawns as needed).
func TestScheduler_Post_Multiple(t *testing.T) {
	chk := require.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := NewScheduler()
	s.Acquire()
	defer s.Wait()
	defer s.Release()

	const n = 50
	var ran atomic.Int64
	allDone := make(chan struct{})
	for i := 0; i < n; i++ {
		work := newWorkItem(func(_ context.Context, ex Execution) error {
			ex.Starting()
			if ran.Add(1) == n {
				close(allDone)
			}
			return nil
		})
		chk.NoError(s.Post(ctx, work))
	}

	select {
	case <-allDone:
	case <-ctx.Done():
		t.Fatalf("only %d of %d posted items ran", ran.Load(), n)
	}
}
