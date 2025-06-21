// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

func TestOptional_BasicFunctionality(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)
	ctx := context.Background()

	// TryPopFront should not receive anything when queue is empty
	var received []int
	q.TryPopFront(ctx, p, func(value int) {
		received = append(received, value)
	})
	require.Empty(t, received)

	// TryPushBack should fail when no receivers are waiting
	success := q.TryPushBack(p, 42)
	require.False(t, success, "TryPushBack should fail with no waiting receivers")

	// Start a receiver
	receivedCh := make(chan int)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			receivedCh <- value
		})
		require.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should succeed now
	success = q.TryPushBack(p, 42)
	require.True(t, success, "TryPushBack should succeed with waiting receiver")

	// Verify the value was received
	select {
	case val := <-receivedCh:
		require.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

func TestOptional_TryPushBackMultipleReceivers(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)
	ctx := context.Background()

	const numReceivers = 5
	received := make(chan int, numReceivers)

	// Start multiple receivers
	var wg sync.WaitGroup
	for i := 0; i < numReceivers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := q.PopFront(ctx, p, func(value int) {
				received <- value
			})
			require.NoError(t, err)
		}()
	}

	// Give receivers time to register
	time.Sleep(10 * time.Millisecond)

	// Send values - each should succeed
	for i := 1; i <= numReceivers; i++ {
		success := q.TryPushBack(p, i)
		require.True(t, success, "TryPushBack should succeed for value %d", i)
	}

	// Wait for all receivers to finish
	wg.Wait()

	// Verify all values were received
	close(received)
	var receivedValues []int
	for val := range received {
		receivedValues = append(receivedValues, val)
	}
	require.Len(t, receivedValues, numReceivers)
	// Values might be received in any order due to goroutine scheduling
	require.ElementsMatch(t, []int{1, 2, 3, 4, 5}, receivedValues)
}

func TestOptional_AbandonedReceiver(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)

	// Start a receiver that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			t.Error("Should not receive value when cancelled")
		})
		require.Error(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Cancel the receiver
	cancel()

	// Give time for cancellation to take effect
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should now fail since the receiver abandoned
	success := q.TryPushBack(p, 42)
	require.False(t, success, "TryPushBack should fail with abandoned receiver")
}

func TestOptional_PopFrontFunc(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)

	// Test custom select function that always times out
	var orphanValues []int
	q.PopFrontFunc(p, func(value int) {
		orphanValues = append(orphanValues, value)
	}, func(ch <-chan int) rdvq.SelectResult {
		// Always return aborted (timeout immediately)
		return rdvq.SelectAborted
	})

	// Should not have received any orphan values since no sender
	require.Empty(t, orphanValues)

	// Now test with a sender that sends to the dedicated channel
	go func() {
		time.Sleep(5 * time.Millisecond)
		q.TryPushBack(p, 99)
	}()

	// Use PopFrontFunc with a select that should timeout
	orphanValues = nil
	q.PopFrontFunc(p, func(value int) {
		orphanValues = append(orphanValues, value)
	}, func(ch <-chan int) rdvq.SelectResult {
		time.Sleep(10 * time.Millisecond) // Let the sender send first
		// Return aborted to simulate timeout/abandonment
		return rdvq.SelectAborted
	})

	// Should have received the orphaned value
	require.Len(t, orphanValues, 1)
	require.Equal(t, 99, orphanValues[0])
}

func TestOptional_ConcurrentSendersAndReceivers(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)
	ctx := context.Background()

	const (
		numPairs = 20
		duration = 100 * time.Millisecond
	)

	received := make(chan int, numPairs*10) // Buffer for received values
	var wg sync.WaitGroup

	// Start receiver-sender pairs
	for i := 0; i < numPairs; i++ {
		wg.Add(2)

		// Receiver
		go func(id int) {
			defer wg.Done()
			err := q.PopFront(ctx, p, func(value int) {
				received <- value
			})
			require.NoError(t, err)
		}(i)

		time.Sleep(20 * time.Millisecond) // Make sure receiver is ready

		// Sender - wait a bit then send
		go func(id int) {
			defer wg.Done()
			time.Sleep(time.Duration(id) * time.Millisecond) // Stagger sends
			success := q.TryPushBack(p, 1000+id)
			require.True(t, success, "Send should succeed when receiver is waiting")
		}(i)
	}

	// Wait for all pairs to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good, all completed
	case <-time.After(5 * time.Second):
		t.Fatal("Test did not complete in time")
	}

	// Count received values
	close(received)
	var receivedValues []int
	for val := range received {
		receivedValues = append(receivedValues, val)
	}

	// Should have received exactly numPairs values
	require.Len(t, receivedValues, numPairs)

	// All values should be unique and in the expected range
	valueSet := make(map[int]bool)
	for _, val := range receivedValues {
		require.False(t, valueSet[val], "Duplicate value received: %d", val)
		require.GreaterOrEqual(t, val, 1000)
		require.Less(t, val, 1000+numPairs)
		valueSet[val] = true
	}
}
