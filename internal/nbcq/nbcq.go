// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// This package contains an implementation of the Non-Blocking Concurrent Queue
// Algorithm from "Simple, Fast, and Practical Non-Blocking and Blocking
// Concurrent Queue Algorithms" by Maged M. Michael and Michael L. Scott in
// PODC96 as corrected in JPDC, 1998. The specific pseudocode followed and
// reproduced as comments below was retrieved from
// https://www.cs.rochester.edu/research/synchronization/pseudocode/queues.html
// on May 16, 2025.
//
// This implementation depends on [github.com/petenewcomb/atomic128-go] which
// provides atomic operations for pairs of 64-bit values using hardware
// acceleration if supported, [atomic.Value] if not. Hardware acceleration can
// be disabled by setting the environment variable PSGNATIVEA128 to a value
// recognized by [strconv.ParseBool] as false, and it can be required by setting
// it to a value recognized as true. If the environment variable is not set or
// set to the empty string, hardware acceleration will be used if supported by
// both atomic128-go and the platform on which PSG is running.
package nbcq

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/trace"
)

// structure pointer_t {ptr: pointer to node_t, count: unsigned integer}
type pointer[T any] struct {
	ptr   *node[T]
	count uint64
}

// structure node_t {value: data type, next: pointer_t}
type node[T any] struct {
	value atomic.Pointer[T]
	next  atomicPointer[T]
}

// Init implements omnipool.Initer to properly initialize new nodes.
func (n *node[T]) Init() {
	// atomic.Value, used in the fallback implementation of atomic128-go, must be
	// initialized with the correct type before it can be used for normal operations.
	n.next.Store(pointer[T]{})
}

// Reset implements omnipool.Resetter to properly reset the node for reuse.
func (n *node[T]) Reset() {
	n.value.Store(nil)
	// Don't reset next here as it must be specially handled for the lock-free
	// algorithm. See note at D19 in PopFront where next.count is preserved to
	// ensure other goroutines can still safely use their references to this
	// node even while it's pooled and after re-use.
}

// structure queue_t {Head: pointer_t, Tail: pointer_t}
type Queue[T any] struct {
	head      atomicPointer[T]
	tail      atomicPointer[T]
	nodePool  *omnipool.Pool[node[T]]
	valuePool *omnipool.Pool[T]
}

// initialize(Q: pointer to queue_t)
func (q *Queue[T]) Init() {
	// Get shared pools for this type
	q.nodePool = omnipool.For[node[T]]()
	q.valuePool = omnipool.For[T]()

	// node = new_node()      // Allocate a free node
	// node->next.ptr = NULL  // Make it the only node in the linked list

	// We do not pull from the pool here to ensure that once a node has been
	// used its next count will never be reset to zero. See note at D19 in
	// PopFront for more detail.
	node := &node[T]{}
	node.Init()

	// Q->Head.ptr = Q->Tail.ptr = node	 // Both Head and Tail point to it
	q.head.Store(pointer[T]{ptr: node})
	q.tail.Store(pointer[T]{ptr: node})
}

// enqueue(Q: pointer to queue_t, value: data type)
//
//nolint:gocritic // ignore commented-out (pseudo-)code
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PushBack(value T) {
	traceRegion := "nbcq.PushBack"

	// E1: node = new_node()      // Allocate a new node from the free list
	// E2: node->value = value	  // Copy enqueued value into node
	// E3: node->next.ptr = NULL  // Set next pointer of node to NULL
	node := q.nodePool.Get()
	node.value.Store(q.valuePool.Clone(value))

	// E4: loop  // Keep trying until Enqueue is done
	for {
		// E5: tail = Q->Tail         // Read Tail.ptr and Tail.count together
		tail := q.tail.Load()
		// E6: next = tail.ptr->next  // Read next ptr and count fields together
		next := tail.ptr.next.Load()
		// E7: if tail == Q->Tail     // Are tail and next consistent?
		if tail == q.tail.Load() {
			// Was Tail pointing to the last node?
			// E8: if next.ptr == NULL
			if next.ptr == nil {
				// Try to link node at the end of the linked list
				// E9: if CAS(&tail.ptr->next, next, <node, next.count+1>)
				if tail.ptr.next.CompareAndSwap(next, pointer[T]{ptr: node, count: next.count + 1}) {
					// E10: break	  // Enqueue is done.  Exit loop

					if trace.IsEnabled() {
						trace.Logf(context.Background(), traceRegion, "Queue=%p item=%d enqueued", q, tail.count)
					}

					// Instead of breaking the loop, the post-loop step is
					// moved here to avoid expanding the scope of the tail
					// variable.

					// Enqueue is done.  Try to swing Tail to the inserted node
					// E17: CAS(&Q->Tail, tail, <node, tail.count+1>)
					q.tail.CompareAndSwap(tail, pointer[T]{ptr: node, count: tail.count + 1})
					return
				} // E11: endif
			} else {
				// E12: else          // Tail was not pointing to the last node
				// Try to swing Tail to the next node
				// E13: CAS(&Q->Tail, tail, <next.ptr, tail.count+1>)
				q.tail.CompareAndSwap(tail, pointer[T]{ptr: next.ptr, count: tail.count + 1})
			} // E14: endif
		} // E15: endif
	} // E16: endloop
}

// dequeue(Q: pointer to queue_t, pvalue: pointer to data type): boolean
//
//nolint:gocritic // ignore commented-out (pseudo-)code
//nolint:contextcheck // background context used only for tracing
func (q *Queue[T]) PopFront() (T, bool) {
	traceRegion := "nbcq.PopFront"

	// D1: loop // Keep trying until Dequeue is done
	for {
		// D2: head = Q->Head         // Read Head
		head := q.head.Load()
		// D3: tail = Q->Tail         // Read Tail
		tail := q.tail.Load()
		// D4: next = head.ptr->next  // Read Head.ptr->next
		next := head.ptr.next.Load()
		// D5: if head == Q->Head     // Are head, tail, and next consistent?
		if head == q.head.Load() {
			// D6: if head.ptr == tail.ptr  // Is queue empty or Tail falling behind?
			if head.ptr == tail.ptr {
				// D7: if next.ptr == NULL  // Is queue empty?
				if next.ptr == nil {
					// D8: return FALSE     // Queue is empty, couldn't dequeue
					return *new(T), false
				} // D9: endif
				// Tail is falling behind.  Try to advance it
				// D10: CAS(&Q->Tail, tail, <next.ptr, tail.count+1>)
				q.tail.CompareAndSwap(tail, pointer[T]{ptr: next.ptr, count: tail.count + 1})
			} else {
				// D11: else                // No need to deal with Tail
				// Read value before CAS
				// Otherwise, another dequeue might free the next node
				// D12: *pvalue = next.ptr->value
				valuePointer := next.ptr.value.Load()

				// Try to swing Head to the next node
				// D13: if CAS(&Q->Head, head, <next.ptr, head.count+1>)
				if q.head.CompareAndSwap(head, pointer[T]{ptr: next.ptr, count: head.count + 1}) {
					// D14: break           // Dequeue is done.  Exit loop

					if trace.IsEnabled() {
						trace.Logf(context.Background(), traceRegion, "Queue=%p item=%d dequeued", q, head.count)
					}

					// Instead of breaking the loop, the post-loop steps are
					// moved here to avoid expanding the scope of the value
					// variable.

					// D19: free(head.ptr)  // It is safe now to free the old node

					// Clear the next pointer so the node is ready for re-use,
					// but maintain the associated count so that it will still
					// not compare equal to the dummy node's next pointer. This
					// ensures that other goroutines that still have references
					// to the node can still safely use its next pointer in
					// comparisons even while it's in the pool and even after
					// re-use, because the count will always differ.
					head.ptr.next.Store(pointer[T]{count: next.count})

					// Clear the node's value and recycle the value pointer
					// object itself to allow any held resources to be garbage
					// collected before storing this node in the pool. A value
					// pointer object and atomic.Pointer is used to avoid the
					// write getting flagged by the race detector. The original
					// algorithm doesn't require use of an atomic operation here
					// because while step D12 above might read an incorrect
					// value, it wouldn't end up using it because the CAS would
					// fail.
					head.ptr.value.Store(nil)

					// Stash the value pointer away for reuse.
					value := *valuePointer
					q.valuePool.Put(valuePointer)

					// Stash the node away for reuse.
					q.nodePool.Put(head.ptr)

					// D20: return TRUE     // Queue was not empty, dequeue succeeded
					return value, true
				} // D15: endif
			} // D16: endif
		} // D17: endif
	} // D18: endloop
}
