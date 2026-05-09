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
//	Notifier     - Combines Listeners and Waiters for prioritized notification routing
//
// Supporting types for queue operations:
//
//	Outbox[T]    - Per-sender buffer for overflow handling
//	Inbox[T]     - Per-receiver message buffer
//	Sender       - Manages outboxes across multiple queues for a single goroutine
//	Waiter       - Coordination primitive for blocking/notification
//	Receiver     - Combines Inbox and Waiter for queue operations
//	Listener     - Reusable notification subscription for multiple Listeners
//
// Note: Sender and Waiter instances are typically managed per-goroutine,
// with each goroutine maintaining its own instances for the queues it interacts with.
//
// # Performance Tiers
//
// The system operates in two performance tiers, from fastest to slowest:
//
//  1. Direct handoff: Sender delivers directly to waiting receiver's dedicated inbox
//  2. Outbox buffering: First overflow item per sender goes to outbox (non-blocking),
//     subsequent items block on the outbox channel directly (per-sender backpressure)
//
// This design ensures that the first overflow item from each sender never blocks,
// providing excellent performance under bursty load patterns while maintaining
// per-sender backpressure when receivers can't keep up.
//
// # Typical Usage
//
//	var queue Queue[MyType]
//	queue.Init()
//
//	// Sender side
//	var sender Sender
//	err := queue.PushBack(ctx, &sender, value, nil)
//
//	// Receiver side
//	var receiver Receiver
//	value, err := queue.PopFront(ctx, &receiver)
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
// However, certain types have ownership requirements for correct usage:
//
//	Sender instances must be dedicated to a single goroutine. Each Sender
//	manages outboxes across multiple Queue instances for that goroutine.
//	Sharing a Sender between goroutines will cause data races.
//
//	Receiver instances must be dedicated to a single goroutine. Each Receiver
//	manages inboxes across multiple Queue instances for that goroutine.
//	Sharing a Receiver between goroutines will cause data races.
//
//	Waiter instances must be dedicated to a single goroutine. Each Waiter
//	manages wait state across multiple Waiters instances for that goroutine.
//	Sharing a Waiter between goroutines will cause data races.
//
//	Listener instances are typically owned by a single entity but use internal
//	synchronization because their notify method can be called concurrently
//	from multiple goroutines when subscribed Listeners fire notifications.
//
// These ownership requirements ensure optimal performance and correctness.
// The Queue and Waiters instances themselves can be safely shared across
// multiple goroutines.
package rdvq
