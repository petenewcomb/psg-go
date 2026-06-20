// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/assert"
)

// scheduledWorkItem is a test [ScheduledWork] built on the embeddable
// [ScheduledWorkItem] base, adding only an Execute that records that it ran.
type scheduledWorkItem struct {
	ScheduledWorkItem
	executed bool
}

func newScheduledWorkItem() *scheduledWorkItem {
	wi := &scheduledWorkItem{}
	wi.Init(NewGroupID())
	return wi
}

func (wi *scheduledWorkItem) Execute(_ context.Context, ex Execution) error {
	ex.Starting()
	wi.executed = true
	return nil
}

// blockingAddWork is a minimal [AddWorkFunc] for tests: it adds no work
// of its own and, on the blocking path, parks on the queue's waiters
// until notified, the queue's deadline timer (timedCh) fires, or the
// context is cancelled. Watching timedCh is what lets a pending
// scheduled-work deadline wake the parked worker.
func blockingAddWork(
	ctx context.Context,
	_ QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
	timedCh <-chan time.Time,
) (RenotifyFunc, error) {
	if waiters == nil {
		// Non-blocking probe: no work to contribute.
		return nil, nil
	}
	var err error
	rf := waiters.WaitFunc(confirmWaitFn,
		func(waitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
			select {
			case rf := <-waitCh:
				return rf
			case <-timedCh:
				return nil
			case <-ctx.Done():
				err = ctx.Err()
				return nil
			}
		})
	return rf, err
}

// endOfWorkAddWork is an [AddWorkFunc] that never has work, signalling
// end-of-work so ExecuteOne returns promptly instead of blocking.
func endOfWorkAddWork(
	context.Context, QueueWorkFunc, *rdvq.Waiters, func() bool, <-chan time.Time,
) (RenotifyFunc, error) {
	return nil, ErrEndOfWork
}

func TestAccepted_Schedule_ImmediateDue(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	q := Accepted{}
	q.Init(nil)

	w := newScheduledWorkItem()
	// Already-due deadline: drainTimed should move it to fresh and it
	// should execute without the queue ever blocking on addWorkFn.
	q.Schedule(w, time.Now().Add(-time.Millisecond))

	err := q.ExecuteOne(ctx, endOfWorkAddWork, nil)
	chk.NoError(err)
	chk.True(w.executed, "due scheduled work should have executed")
}

func TestAccepted_Remove_CancelsScheduled(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	q := Accepted{}
	q.Init(nil)

	w := newScheduledWorkItem()
	q.Schedule(w, time.Now().Add(time.Hour))
	q.Remove(w)

	// Nothing is due and the work was removed, so ExecuteOne should reach
	// end-of-work without executing it.
	err := q.ExecuteOne(ctx, endOfWorkAddWork, nil)
	chk.ErrorIs(err, ErrEndOfWork)
	chk.False(w.executed, "removed scheduled work must not execute")
}

func TestAccepted_Schedule_ReschedulesInPlace(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	q := Accepted{}
	q.Init(nil)

	w := newScheduledWorkItem()
	// Schedule far out, then reschedule (same work) to an already-due
	// deadline; the second Schedule must replace the first, not duplicate.
	q.Schedule(w, time.Now().Add(time.Hour))
	q.Schedule(w, time.Now().Add(-time.Millisecond))

	err := q.ExecuteOne(ctx, endOfWorkAddWork, nil)
	chk.NoError(err)
	chk.True(w.executed, "rescheduled-to-due work should execute")
}

func TestAccepted_FutureDeadline_WakesParkedWorker(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	q := Accepted{}
	q.Init(nil)

	w := newScheduledWorkItem()
	const delay = 30 * time.Millisecond
	q.Schedule(w, time.Now().Add(delay))

	// With a blocking addWorkFn, the only thing that can wake the parked
	// worker is the queue's internal deadline timer firing at ~delay.
	start := time.Now()
	err := q.ExecuteOne(ctx, blockingAddWork, nil)
	elapsed := time.Since(start)

	chk.NoError(err)
	chk.True(w.executed, "future scheduled work should execute once its deadline arrives")
	chk.GreaterOrEqual(elapsed, delay, "should not have executed before the deadline")
}
