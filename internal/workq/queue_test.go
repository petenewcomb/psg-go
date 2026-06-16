// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

// testExecEnv is a minimal ExecEnv over real rdvq primitives, enough to drive a
// Worker[E] in tests. The funnel/task engine's real E (integrationExEnv-based)
// adds the group/queue stacks; the Worker only needs Sender/Receiver/Waiter.
type testExecEnv struct {
	sender   rdvq.Sender
	receiver rdvq.Receiver
	waiter   rdvq.Waiter
}

func (e *testExecEnv) Sender() *rdvq.Sender     { return &e.sender }
func (e *testExecEnv) Receiver() *rdvq.Receiver { return &e.receiver }
func (e *testExecEnv) Waiter() *rdvq.Waiter     { return &e.waiter }
func (e *testExecEnv) Release()                 { e.sender.Release(); e.receiver.Release() }

// TestQueue_Post_BuffersAndSignalsDemand pins the handoff's no-taker path: with
// no receiver, Post buffers the work in the sender's outbox, fires the demand
// signal (so a worker is requested), calls ex.Starting(), and the item is then
// retrievable from the incoming handoff.
func TestQueue_Post_BuffersAndSignalsDemand(t *testing.T) {
	chk := require.New(t)

	var q Queue
	demand := 0
	q.Init(func() { demand++ })

	var sender rdvq.Sender
	defer sender.Release()

	started := false
	ex := Execution{Starting: func() { started = true }} // AddToListeners nil → cannot wait

	work := newWorkItem(func(context.Context, Execution) error { return nil })

	posted, err := q.Post(context.Background(), ex, &sender, false, work, nil)
	chk.NoError(err)
	chk.True(posted)
	chk.True(started)
	chk.Equal(1, demand, "buffering with no receiver must fire the demand signal")

	got, ok := q.incoming.TryPopFront()
	chk.True(ok)
	chk.Equal(Work(work), got)
}

// TestQueue_Post_DirectHandoffToWaitingReceiver pins the rendezvous: a parked
// receiver gets the posted work. Whether Post hands off directly or buffers
// (timing-dependent), the receiver must observe the item.
func TestQueue_Post_DirectHandoffToWaitingReceiver(t *testing.T) {
	chk := require.New(t)

	var q Queue
	q.Init(nil)

	var sender rdvq.Sender
	defer sender.Release()

	work := newWorkItem(func(context.Context, Execution) error { return nil })

	type result struct {
		w   Work
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var receiver rdvq.Receiver
		defer receiver.Release()
		w, err := q.incoming.PopFront(context.Background(), &receiver)
		ch <- result{w, err}
	}()

	ex := Execution{Starting: func() {}}
	posted, err := q.Post(context.Background(), ex, &sender, false, work, nil)
	chk.NoError(err)
	chk.True(posted)

	r := <-ch
	chk.NoError(r.err)
	chk.Equal(Work(work), r.w)
}

// TestWorker_DriveOne_ExecutesPostedWork drives Worker[E] over a Queue
// end-to-end: a value is Posted to the incoming handoff, then a Worker.DriveOne
// pulls it through the priority engine and executes it. Validates the Queue+
// Worker pair (checkpoints 1+2) before the funnel cutover exercises them.
func TestWorker_DriveOne_ExecutesPostedWork(t *testing.T) {
	chk := require.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var q Queue
	q.Init(nil)

	env := &testExecEnv{}
	defer env.Release()
	w := NewWorker[*testExecEnv](&q, env, ctx)
	defer w.Release()

	executed := false
	work := newWorkItem(func(_ context.Context, ex Execution) error {
		ex.Starting()
		executed = true
		return nil
	})

	var prod rdvq.Sender
	defer prod.Release()
	posted, err := q.Post(ctx, Execution{Starting: func() {}}, &prod, false, work, nil)
	chk.NoError(err)
	chk.True(posted)

	one, err := w.DriveOne(ctx)
	chk.NoError(err)
	chk.True(one, "DriveOne should report one item executed")
	chk.True(executed, "the posted work should have run")
}
