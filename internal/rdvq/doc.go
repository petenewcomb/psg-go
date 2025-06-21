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
// # Architecture
//
// The package implements a layered architecture:
//
//	Optional[T]  - Base layer: direct sender-receiver rendezvous
//	Required[T]  - Extended layer: adds outboxes with per-sender backpressure
//	Waiters      - Notification system: prevents race conditions in overflow handling
//	Outbox[T]    - Per-sender buffer: provides "drop-and-go" semantics
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
// # SelectResult API Pattern
//
// The package uses a consistent SelectResult enumeration for all select operations,
// providing clear indication of what happened in each select statement:
//
//	SelectAborted       - Operation was cancelled/interrupted
//	SelectInboxEmptied  - Inbox channel was successfully read from
//	SelectOutboxFilled  - Outbox channel was successfully written to
//	SelectWaitSignaled  - Wait channel was signaled
//
// This pattern replaces inconsistent boolean returns and makes select operation
// outcomes explicit and type-safe.
//
// # Typical Usage
//
//	var pool Pool[MyType]
//	var queue Required[MyType]
//	queue.Init(&pool)
//
//	// Sender side
//	var outbox Outbox[MyType]
//	err := queue.PushBack(ctx, &pool, &outbox, value)
//	err = outbox.Wait(ctx, &pool) // Ensure outbox is drained
//
//	// Receiver side
//	err := queue.PopFront(ctx, &pool, func(value MyType) {
//		// Process value
//	})
//
// # Race Condition Prevention
//
// The package uses a sophisticated waiter verification system to prevent race
// conditions between outbox checking and blocking operations. When receivers
// register to wait, they provide a verification function that re-checks for
// outbox items after registration but before blocking, ensuring no items are
// missed.
//
// # Thread Safety
//
// All operations are thread-safe and lock-free. Multiple senders and receivers
// can operate concurrently without external synchronization. The implementation
// uses atomic operations and careful memory ordering to ensure correctness.
package rdvq
