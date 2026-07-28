// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHandoff_BasicRendezvous: a value PushBack'd is received by a PopFront.
func TestHandoff_BasicRendezvous(t *testing.T) {
	chk := require.New(t)
	var h Handoff[int]
	h.Init()

	done := make(chan error, 1)
	go func() { done <- h.PushBack(context.Background(), 42) }()

	got, err := h.PopFront(context.Background())
	chk.NoError(err)
	chk.Equal(42, got)
	chk.NoError(<-done, "PushBack returns once handed off")
}

// TestHandoff_SenderBlocksThenDelivers: PushBack does not return until a receiver appears
// — the unbuffered (no drop-and-go) property.
func TestHandoff_SenderBlocksThenDelivers(t *testing.T) {
	chk := require.New(t)
	var h Handoff[int]
	h.Init()

	done := make(chan error, 1)
	go func() { done <- h.PushBack(context.Background(), 7) }()

	select {
	case <-done:
		t.Fatal("PushBack returned with no waiting receiver")
	case <-time.After(30 * time.Millisecond):
	}

	got, err := h.PopFront(context.Background())
	chk.NoError(err)
	chk.Equal(7, got)
	chk.NoError(<-done)
}

// TestHandoff_PushBackCtxCancel: a parked sender unblocks with ctx.Err() on cancel.
func TestHandoff_PushBackCtxCancel(t *testing.T) {
	chk := require.New(t)
	var h Handoff[int]
	h.Init()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.PushBack(ctx, 1) }()

	time.Sleep(30 * time.Millisecond) // let it park
	cancel()
	chk.ErrorIs(<-done, context.Canceled)
}

// TestHandoff_PopFrontCtxCancel: a waiting receiver unblocks with ctx.Err() on cancel.
func TestHandoff_PopFrontCtxCancel(t *testing.T) {
	chk := require.New(t)
	var h Handoff[int]
	h.Init()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.PopFront(ctx)
		done <- err
	}()

	time.Sleep(30 * time.Millisecond) // let it park
	cancel()
	chk.ErrorIs(<-done, context.Canceled)
}

// TestHandoff_ConcurrentExactlyOnce: under many concurrent senders and receivers every
// value is delivered exactly once — no lost wakeup (a parked sender never stranded while
// a receiver waits) and no duplicate. Run with -race for the memory-ordering check.
func TestHandoff_ConcurrentExactlyOnce(t *testing.T) {
	chk := require.New(t)
	var h Handoff[int]
	h.Init()

	const senders, receivers, perSender = 24, 8, 64
	const total = senders * perSender

	seen := make([]atomic.Int32, total)
	var received atomic.Int64
	var sendFail atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())

	var rwg sync.WaitGroup
	for range receivers {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				v, err := h.PopFront(ctx)
				if err != nil {
					return // ctx cancelled — no more work
				}
				seen[v].Add(1)
				received.Add(1)
			}
		}()
	}

	var swg sync.WaitGroup
	for s := range senders {
		swg.Add(1)
		go func(base int) {
			defer swg.Done()
			for i := range perSender {
				if err := h.PushBack(ctx, base+i); err != nil {
					sendFail.Add(1)
				}
			}
		}(s * perSender)
	}
	swg.Wait() // every value handed off

	// Wait for every handed-off value to be processed (bounded, so a lost value fails
	// rather than hangs the suite).
	deadline := time.Now().Add(5 * time.Second)
	for received.Load() < total {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d values received — a wakeup was lost", received.Load(), total)
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	rwg.Wait()

	chk.Zero(sendFail.Load(), "no PushBack should fail before cancellation")
	for v := range total {
		chk.Equal(int32(1), seen[v].Load(), "value %d delivered exactly once", v)
	}
}
