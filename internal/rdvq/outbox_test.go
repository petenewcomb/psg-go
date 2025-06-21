// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"context"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

func TestOutbox_ZeroValue(t *testing.T) {
	var outbox rdvq.Outbox[int]
	var pool rdvq.Pool[int]

	// Zero value should be empty
	require.True(t, outbox.IsEmpty(&pool))

	// Wait on empty outbox should return immediately
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := outbox.Wait(ctx, &pool)
	require.NoError(t, err)
}

func TestOutbox_StateTransitions(t *testing.T) {
	var q rdvq.Required[int]
	var pool rdvq.Pool[int]
	q.Init(&pool)

	var outbox rdvq.Outbox[int]

	// Initially empty
	require.True(t, outbox.IsEmpty(&pool))

	// Add item via TryPushBack when no receivers are waiting
	success := q.TryPushBack(&pool, &outbox, 42)
	require.True(t, success) // Should go to outbox

	// Should now be full
	require.False(t, outbox.IsEmpty(&pool))

	// Drain the outbox via TryPopFront
	value, ok := q.TryPopFront(&pool)
	require.True(t, ok)
	require.Equal(t, 42, value)

	// Should be empty again
	require.True(t, outbox.IsEmpty(&pool))
}

func TestOutbox_WaitFunc(t *testing.T) {
	var q rdvq.Required[int]
	var pool rdvq.Pool[int]
	q.Init(&pool)

	var outbox rdvq.Outbox[int]

	// WaitFunc on empty outbox should not call selectFn
	called := false
	outbox.WaitFunc(&pool, func(ch chan<- int) rdvq.SelectResult {
		called = true
		return rdvq.SelectAborted
	})
	require.False(t, called)

	// Add item to outbox via TryPushBack
	success := q.TryPushBack(&pool, &outbox, 42)
	require.True(t, success)
	require.False(t, outbox.IsEmpty(&pool))

	// WaitFunc should call selectFn with the channel
	selectCalled := false
	var providedCh chan<- int
	outbox.WaitFunc(&pool, func(ch chan<- int) rdvq.SelectResult {
		selectCalled = true
		providedCh = ch

		// The channel already has an item (42), so this zero value send should fail
		select {
		case ch <- 0: // Try to send zero value
			return rdvq.SelectOutboxFilled
		default:
			return rdvq.SelectAborted // Channel is full, can't send
		}
	})

	require.True(t, selectCalled)
	require.NotNil(t, providedCh)

	// Outbox should still be full since selectFn returned false
	require.False(t, outbox.IsEmpty(&pool))

	// Now drain the outbox and test WaitFunc again
	value, ok := q.TryPopFront(&pool)
	require.True(t, ok)
	require.Equal(t, 42, value)
	require.True(t, outbox.IsEmpty(&pool))
}

func TestOutbox_WaitWithContext(t *testing.T) {
	var q rdvq.Required[int]
	var pool rdvq.Pool[int]
	q.Init(&pool)

	var outbox rdvq.Outbox[int]

	// Add item to outbox so Wait will block
	success := q.TryPushBack(&pool, &outbox, 42)
	require.True(t, success)
	require.False(t, outbox.IsEmpty(&pool))

	// Wait with cancelled context should return error
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := outbox.Wait(ctx, &pool)
	require.Error(t, err)
	require.Equal(t, context.Canceled, err)

	// Outbox should still contain the item
	require.False(t, outbox.IsEmpty(&pool))

	// Wait with timeout should return error
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = outbox.Wait(ctx, &pool)
	require.Error(t, err)
	require.Equal(t, context.DeadlineExceeded, err)
}

func TestOutbox_ConcurrentAccess(t *testing.T) {
	var outbox rdvq.Outbox[int]
	var pool rdvq.Pool[int]

	// Test that multiple goroutines can safely check IsEmpty
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			// All should see empty outbox
			empty := outbox.IsEmpty(&pool)
			done <- empty
		}()
	}

	// Collect results
	for i := 0; i < numGoroutines; i++ {
		select {
		case empty := <-done:
			require.True(t, empty)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Concurrent IsEmpty test timed out")
		}
	}
}

func TestOutbox_ChannelRecycling(t *testing.T) {
	var q rdvq.Required[int]
	var pool rdvq.Pool[int]
	q.Init(&pool)

	var outbox rdvq.Outbox[int]

	// Add and remove item several times to test channel recycling
	for i := 0; i < 5; i++ {
		// Add item via TryPushBack
		success := q.TryPushBack(&pool, &outbox, i)
		require.True(t, success)
		require.False(t, outbox.IsEmpty(&pool))

		// Remove item via TryPopFront
		value, ok := q.TryPopFront(&pool)
		require.True(t, ok)
		require.Equal(t, i, value)
		require.True(t, outbox.IsEmpty(&pool))
	}

	// Verify that channels are being recycled by checking that we don't
	// run out of memory with repeated allocations
	// (This is more of a smoke test than a rigorous check)
}
