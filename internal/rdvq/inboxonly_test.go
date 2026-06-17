// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

//nolint:thelper // these are sub-test functions, not test helpers
package rdvq

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// testInboxOnlyQueue is an interface for testing both queue types
type testInboxOnlyQueue[T any] interface {
	Init()
	TryPushBack(value T) bool
	PopFront(ctx context.Context, inbox *inbox[T], processFn ProcessValueFunc[T]) error
	PopFrontFunc(inbox *inbox[T], processOrphanFn ProcessValueFunc[T], selectFn inboxOnlyPopSelectFunc[T]) bool
}

// inboxOnlyQueueTestCases returns test cases for both queue implementations
func inboxOnlyQueueTestCases[T any]() []struct {
	name      string
	makeQueue func() testInboxOnlyQueue[T]
} {
	return []struct {
		name      string
		makeQueue func() testInboxOnlyQueue[T]
	}{
		{
			name: "InboxQueueQueue",
			makeQueue: func() testInboxOnlyQueue[T] {
				q := &inboxQueueQueue[T]{}
				return q
			},
		},
		{
			name: "InboxStackQueue",
			makeQueue: func() testInboxOnlyQueue[T] {
				q := &inboxStackQueue[T]{}
				return q
			},
		},
	}
}

func testBasicFunctionality(t *testing.T, q testInboxOnlyQueue[int]) {
	ctx := context.Background()

	// TryPushBack should fail when no receivers are waiting
	success := q.TryPushBack(42)
	assert.False(t, success, "TryPushBack should fail with no waiting receivers")

	// Start a receiver
	receivedCh := make(chan int)
	go func() {
		var inbox inbox[int]
		err := q.PopFront(ctx, &inbox, func(value int) {
			receivedCh <- value
		})
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should succeed now
	success = q.TryPushBack(42)
	assert.True(t, success, "TryPushBack should succeed with waiting receiver")

	// Verify the value was received
	select {
	case val := <-receivedCh:
		assert.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

func testTryPushBackMultipleReceivers(t *testing.T, q testInboxOnlyQueue[int]) {
	ctx := context.Background()

	const numReceivers = 5
	received := make(chan int, numReceivers)

	// Start multiple receivers
	var wg sync.WaitGroup
	for i := 0; i < numReceivers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inbox inbox[int]
			err := q.PopFront(ctx, &inbox, func(value int) {
				received <- value
			})
			assert.NoError(t, err)
		}()
	}

	// Give receivers time to register
	time.Sleep(10 * time.Millisecond)

	// Send values - each should succeed
	for i := 1; i <= numReceivers; i++ {
		success := q.TryPushBack(i)
		assert.True(t, success, "TryPushBack should succeed for value %d", i)
	}

	// Wait for all receivers to finish
	wg.Wait()

	// Verify all values were received
	close(received)
	receivedValues := make([]int, 0, len(received))
	for val := range received {
		receivedValues = append(receivedValues, val)
	}
	assert.Len(t, receivedValues, numReceivers)
	// Values might be received in any order due to goroutine scheduling
	assert.ElementsMatch(t, []int{1, 2, 3, 4, 5}, receivedValues)
}

func testAbandonedReceiver(t *testing.T, q testInboxOnlyQueue[int]) {
	// Start a receiver that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		var inbox inbox[int]
		err := q.PopFront(ctx, &inbox, func(value int) {
			t.Error("Should not receive value when cancelled")
		})
		assert.Error(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Cancel the receiver
	cancel()

	// Give time for cancellation to take effect
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should now fail since the receiver abandoned
	success := q.TryPushBack(42)
	assert.False(t, success, "TryPushBack should fail with abandoned receiver")
}

func testPopFrontFunc(t *testing.T, q testInboxOnlyQueue[int]) {
	// Test custom select function that always times out
	var ib inbox[int]
	var orphanValues []int
	q.PopFrontFunc(&ib, func(value int) {
		orphanValues = append(orphanValues, value)
	}, func(*inbox[int]) {
		// Always return aborted (timeout immediately)
		// Don't call ib.emptied() to simulate abort
	})

	// Should not have received any orphan values since no sender
	assert.Empty(t, orphanValues)

	// Now test with a sender that sends to the dedicated channel
	go func() {
		time.Sleep(5 * time.Millisecond)
		q.TryPushBack(99)
	}()

	// Use PopFrontFunc with a select that should timeout
	orphanValues = nil
	q.PopFrontFunc(&ib, func(value int) {
		orphanValues = append(orphanValues, value)
	}, func(*inbox[int]) {
		time.Sleep(10 * time.Millisecond) // Let the sender send first
		// Don't call ib.emptied() to simulate timeout/abandonment
	})

	// Should have received the orphaned value
	assert.Len(t, orphanValues, 1)
	assert.Equal(t, 99, orphanValues[0])
}

func testStress(t *testing.T, q testInboxOnlyQueue[int]) {

	numPushers := runtime.GOMAXPROCS(-1)
	numPoppers := runtime.GOMAXPROCS(-1)
	duration := 30 * time.Second

	if testing.Short() {
		duration = 1 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		tryPushed atomic.Int64
		refused   atomic.Int64
		popped    atomic.Int64
		abandoned atomic.Int64
	)

	pushErrCh := make(chan error, 1)
	popErrCh := make(chan error, 1)

	popOps := []func(context.Context, *inbox[int]){
		func(ctx context.Context, inbox *inbox[int]) {
			// Normal popper
			err := q.PopFront(ctx, inbox, func(value int) {
				popped.Add(1)
			})
			switch {
			case err == nil:
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			default:
				select {
				case popErrCh <- err:
				default:
				}
			}
		},
		func(ctx context.Context, inbox *inbox[int]) {
			// Abandoning popper
			shortCtx, shortCancel := context.WithTimeout(ctx, 1*time.Nanosecond)
			err := q.PopFront(shortCtx, inbox, func(value int) {
				popped.Add(1)
			})
			switch {
			case err == nil:
			case errors.Is(err, context.DeadlineExceeded) && shortCtx.Err() != nil:
				abandoned.Add(1)
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			default:
				select {
				case popErrCh <- err:
				default:
				}
			}
			shortCancel()
		},
	}

	var wg sync.WaitGroup

	for range numPoppers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a new context to reduce contention
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			var inbox inbox[int]
			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				popOps[rand.IntN(len(popOps))](ctx, &inbox)
			}
		}()
	}

	for range numPushers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a new context to reduce contention
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			for ctx.Err() == nil {
				if !q.TryPushBack(int(tryPushed.Add(1))) {
					refused.Add(1)
				}
			}
		}()
	}

	// Let it run for a while
	time.Sleep(duration)
	cancel()
	wg.Wait()

	t.Logf("TryPushed: %d, Refused: %d, Popped: %d, Abandoned: %d",
		tryPushed.Load(), refused.Load(), popped.Load(), abandoned.Load())

	// Error but context not cancelled - unexpected
	select {
	case err := <-pushErrCh:
		assert.NoError(t, err, "Unexpected error from PushBack")
	default:
	}

	select {
	case err := <-popErrCh:
		assert.NoError(t, err, "Unexpected error from PopFront")
	default:
	}

	actuallyPushed := tryPushed.Load() - refused.Load()
	actuallyPopped := popped.Load()
	assert.Equal(t, actuallyPushed, actuallyPopped)
}

func TestInboxOnly(t *testing.T) {
	for _, tc := range inboxOnlyQueueTestCases[int]() {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.makeQueue()
			q.Init()

			t.Run("BasicFunctionality", func(t *testing.T) {
				testBasicFunctionality(t, q)
			})

			t.Run("TryPushBackMultipleReceivers", func(t *testing.T) {
				testTryPushBackMultipleReceivers(t, q)
			})

			t.Run("AbandonedReceiver", func(t *testing.T) {
				testAbandonedReceiver(t, q)
			})

			t.Run("PopFrontFunc", func(t *testing.T) {
				testPopFrontFunc(t, q)
			})

			t.Run("Stress", func(t *testing.T) {
				testStress(t, q)
			})
		})
	}
}
