// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"sync"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

func TestWaiters_BasicNotification(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	// No waiters - notification should be dropped
	waiters.Notify()

	notified := make(chan bool, 1)
	waiterStarted := make(chan struct{})

	go func() {
		// Create waiter that always continues waiting
		waiter := waiters.New(func() bool { return true })

		close(waiterStarted) // Signal that waiter is created

		result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
			// Block waiting for notification - no default case
			<-ch
			return rdvq.SelectWaitSignaled // Waiter was notified
		})
		notified <- (result == rdvq.SelectWaitSignaled)
	}()

	// Wait for waiter to be created and start waiting
	<-waiterStarted
	time.Sleep(10 * time.Millisecond)

	// Send notification
	waiters.Notify()

	// Should receive notification
	select {
	case result := <-notified:
		require.True(t, result)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Notification not received")
	}
}

func TestWaiters_VerificationFunction(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	// Create waiter with verification that returns false (don't wait)
	waiter := waiters.New(func() bool { return false })

	selectCalled := false
	result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
		selectCalled = true
		return rdvq.SelectAborted
	})

	// Verification returned false, so select function should not be called
	require.False(t, selectCalled)
	require.Equal(t, rdvq.SelectAborted, result)
}

func TestWaiters_VerificationPreventsRace(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	workAvailable := false
	var mu sync.Mutex

	// Start the waiter in a goroutine
	waitResult := make(chan bool, 1)
	waiterStarted := make(chan struct{})

	go func() {
		// Create waiter that checks for work
		waiter := waiters.New(func() bool {
			mu.Lock()
			defer mu.Unlock()
			return !workAvailable // Continue waiting only if no work available
		})

		close(waiterStarted)

		result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
			// When verification succeeds, this should be called and block
			<-ch
			return rdvq.SelectWaitSignaled
		})
		waitResult <- (result == rdvq.SelectWaitSignaled)
	}()

	// Wait for waiter to start
	<-waiterStarted
	time.Sleep(10 * time.Millisecond)

	// Send notification while no work is available - should trigger select function
	waiters.Notify()

	// Waiter should receive notification and return SelectWaitSignaled
	select {
	case result := <-waitResult:
		require.True(t, result) // Should return SelectWaitSignaled since waiter was notified
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Waiter did not respond to notification")
	}
}

func TestWaiters_VerificationPreventsFalseWait(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	workAvailable := true // Work is immediately available

	// Create waiter that checks for work
	waiter := waiters.New(func() bool {
		return !workAvailable // Should return false (don't wait)
	})

	selectCalled := false
	result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
		selectCalled = true
		return rdvq.SelectAborted
	})

	// Verification should have prevented waiting
	require.False(t, selectCalled)               // Select function should not be called
	require.Equal(t, rdvq.SelectAborted, result) // Should return SelectAborted (not notified)
}

func TestWaiters_MultipleWaiters(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	numWaiters := 5
	notifications := make(chan int, numWaiters)

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		waiterID := i
		waiter := waiters.New(func() bool { return true })

		go func(id int) {
			result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
				select {
				case <-ch:
					notifications <- id
					return rdvq.SelectWaitSignaled
				case <-time.After(200 * time.Millisecond):
					return rdvq.SelectAborted
				}
			})
			if result != rdvq.SelectWaitSignaled {
				notifications <- -1 // Indicate timeout/abort
			}
		}(waiterID)
	}

	// Give waiters time to register
	time.Sleep(20 * time.Millisecond)

	// Send notifications one by one
	for i := 0; i < numWaiters; i++ {
		waiters.Notify()
	}

	// Collect notifications
	received := make(map[int]bool)
	for i := 0; i < numWaiters; i++ {
		select {
		case waiterID := <-notifications:
			require.NotEqual(t, -1, waiterID, "Waiter timed out")
			require.False(t, received[waiterID], "Waiter %d notified multiple times", waiterID)
			received[waiterID] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("Did not receive notification %d", i)
		}
	}

	// All waiters should have been notified
	require.Len(t, received, numWaiters)
}

func TestWaiters_NotifyAll(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	numWaiters := 3
	notifications := make(chan bool, numWaiters)

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		waiter := waiters.New(func() bool { return true })

		go func() {
			result := waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
				select {
				case <-ch:
					notifications <- true
					return rdvq.SelectWaitSignaled
				case <-time.After(200 * time.Millisecond):
					notifications <- false
					return rdvq.SelectAborted
				}
			})
			notifications <- (result == rdvq.SelectWaitSignaled)
		}()
	}

	// Give waiters time to register
	time.Sleep(20 * time.Millisecond)

	// Notify all at once
	waiters.NotifyAll()

	// All waiters should be notified
	for i := 0; i < numWaiters; i++ {
		select {
		case notified := <-notifications:
			require.True(t, notified, "Waiter %d was not notified", i)
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("Waiter %d did not receive notification", i)
		}
	}
}

func TestWaiters_OrphanedNotifications(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	// Create waiter but don't actually wait
	waiter := waiters.New(func() bool { return true })

	// Start waiting but abandon immediately
	go func() {
		waiter.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
			// Abandon immediately
			return rdvq.SelectAborted
		})
	}()

	// Give waiter time to register and abandon
	time.Sleep(10 * time.Millisecond)

	// Send notification - should be orphaned
	waiters.Notify()

	// Create new waiter - should handle the orphaned notification gracefully
	waiter2 := waiters.New(func() bool { return true })

	notified := make(chan bool, 1)
	go func() {
		result := waiter2.Wait(func(ch <-chan struct{}) rdvq.SelectResult {
			select {
			case <-ch:
				notified <- true
				return rdvq.SelectWaitSignaled
			case <-time.After(50 * time.Millisecond):
				notified <- false
				return rdvq.SelectAborted
			}
		})
		notified <- (result == rdvq.SelectWaitSignaled)
	}()

	// Give new waiter time to process orphaned notification
	time.Sleep(20 * time.Millisecond)

	// Send another notification for the new waiter
	waiters.Notify()

	// Should receive notification (either orphaned one or new one)
	select {
	case result := <-notified:
		require.True(t, result)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("New waiter did not receive notification")
	}
}
