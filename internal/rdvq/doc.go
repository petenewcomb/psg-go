// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package rdvq provides a high-performance rendezvous queue implementation
// with sophisticated sender-receiver coordination and overflow handling.
//
// # Overview
//
// The rdvq package implements a lock-free rendezvous queue system that optimizes
// for direct handoff between senders and receivers while gracefully handling
// overflow scenarios. It provides functionality similar to buffered channels
// but with unbounded capacity and multi-tier performance optimizations.
//
// The system uses different consumer selection strategies:
// - Queue uses LIFO (stack) for worker selection, enabling natural timeout-based scaling
// - Waiters uses FIFO (queue) for notification fairness
// Items are always delivered in FIFO order; only consumer selection varies.
//
// # Primary Types
//
// The package provides these main coordination primitives:
//
//	Queue[T]     - Rendezvous queue with overflow handling and backpressure
//	Waiters      - Rendezvous-based notification coordination for waiting goroutines
//	Listeners    - Queue of notification functions waiting to be signaled
//	Notifier     - Funnels Listeners and Waiters for prioritized notification routing
//	Listener     - Reusable notification subscription for multiple Listeners
//
// The Queue is destination-owned: it pools its own outboxes and inboxes, so
// callers do not maintain any per-goroutine handles. Push and pop operations
// are plain method calls on the shared Queue.
//
// # Performance Tiers
//
// The system operates in two performance tiers, from fastest to slowest:
//
//  1. Direct handoff: a push delivers directly to a waiting receiver's inbox
//  2. Outbox buffering: the first overflow item is dropped into a pooled outbox
//     (non-blocking); once the outbox pool has no slack, further pushes block on
//     an outbox channel (backpressure)
//
// Because outboxes are owned and pooled by the destination Queue, the queue
// self-sizes its pool to the concurrency it actually experiences, providing
// excellent performance under bursty load while still applying backpressure
// when receivers can't keep up.
//
// # Typical Usage
//
//	var queue Queue[MyType]
//	queue.Init()
//
//	// Push side
//	err := queue.PushBack(ctx, value, nil)
//
//	// Pop side
//	value, err := queue.PopFront(ctx)
//
// # Race Condition Prevention
//
// The package uses a sophisticated waiter verification system to prevent race
// conditions between outbox checking and blocking operations. When receivers
// register to wait, they provide a verification function that re-checks for
// outbox items after registration but before blocking, ensuring no items are
// missed.
//
// # Thread Safety and Ownership
//
// All operations are thread-safe and lock-free. Multiple senders and receivers
// can operate concurrently without external synchronization. The implementation
// uses atomic operations and careful memory ordering to ensure correctness.
//
// The Queue and Waiters instances can be safely shared across multiple
// goroutines; they own and pool the per-operation state (outboxes, inboxes)
// internally, so callers hold no per-goroutine handles.
//
//	Listener instances are typically owned by a single entity but use internal
//	synchronization because their notify method can be called concurrently
//	from multiple goroutines when subscribed Listeners fire notifications.
package rdvq
