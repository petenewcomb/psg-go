// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/assert"
)

func TestOptional_BasicFunctionality(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)
	ctx := context.Background()

	// TryPopFront should not receive anything when queue is empty
	var received []int
	q.TryPopFront(p, func(value int) {
		received = append(received, value)
	})
	assert.Empty(t, received)

	// TryPushBack should fail when no receivers are waiting
	success := q.TryPushBack(p, 42)
	assert.False(t, success, "TryPushBack should fail with no waiting receivers")

	// Start a receiver
	receivedCh := make(chan int)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			receivedCh <- value
		})
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should succeed now
	success = q.TryPushBack(p, 42)
	assert.True(t, success, "TryPushBack should succeed with waiting receiver")

	// Verify the value was received
	select {
	case val := <-receivedCh:
		assert.Equal(t, 42, val)
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
			assert.NoError(t, err)
		}()
	}

	// Give receivers time to register
	time.Sleep(10 * time.Millisecond)

	// Send values - each should succeed
	for i := 1; i <= numReceivers; i++ {
		success := q.TryPushBack(p, i)
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

func TestOptional_AbandonedReceiver(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)

	// Start a receiver that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
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
	success := q.TryPushBack(p, 42)
	assert.False(t, success, "TryPushBack should fail with abandoned receiver")
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
	assert.Empty(t, orphanValues)

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
	assert.Len(t, orphanValues, 1)
	assert.Equal(t, 99, orphanValues[0])
}

func TestOptional_Stress(t *testing.T) {
	var q rdvq.Optional[int]
	q.Init(p)

	numPushers := runtime.GOMAXPROCS(-1)
	numPoppers := runtime.GOMAXPROCS(-1)
	duration := 10 * time.Second

	if testing.Short() {
		duration = 1 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		tryPushed atomic.Int64
		refused   atomic.Int64
		canceled  atomic.Int64
		popped    atomic.Int64
		tryPopped atomic.Int64
		abandoned atomic.Int64
	)

	pushErrCh := make(chan error, 1)
	popErrCh := make(chan error, 1)

	popOps := []func(context.Context){
		func(ctx context.Context) {
			// Normal popper
			err := q.PopFront(ctx, p, func(value int) {
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
		func(ctx context.Context) {
			// Trying popper
			q.TryPopFront(p, func(value int) {
				tryPopped.Add(1)
			})
		},
		func(ctx context.Context) {
			// Abandoning popper
			shortCtx, shortCancel := context.WithTimeout(ctx, 1*time.Nanosecond)
			err := q.PopFront(shortCtx, p, func(value int) {
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

			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				popOps[rand.IntN(len(popOps))](ctx)
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
				if !q.TryPushBack(p, int(tryPushed.Add(1))) {
					refused.Add(1)
				}
			}
		}()
	}

	// Let it run for a while
	time.Sleep(duration)
	cancel()
	wg.Wait()

	// Drain any remaining values
	var remaining int64
	for {
		ok := false
		q.TryPopFront(p, func(int) {
			ok = true
			remaining++
		})
		if !ok {
			break
		}
	}

	t.Logf("TryPushed: %d, Refused: %d, Canceled: %d, Popped: %d, TryPopped: %d, Abandoned: %d, Remaining: %d",
		tryPushed.Load(), refused.Load(), canceled.Load(), popped.Load(), tryPopped.Load(), abandoned.Load(), remaining)

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

	actuallyPushed := tryPushed.Load() - refused.Load() - canceled.Load()
	actuallyPopped := popped.Load() + tryPopped.Load() + remaining
	assert.Equal(t, actuallyPushed, actuallyPopped)
}
