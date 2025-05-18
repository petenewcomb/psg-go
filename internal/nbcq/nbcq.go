// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// This package contains an implementation of the Non-Blocking Concurrent Queue
// Algorithm from "Simple, Fast, and Practical Non-Blocking and Blocking
// Concurrent Queue Algorithms" by Maged M. Michael and Michael L. Scott in
// PODC96 as corrected in JPDC, 1998. The specific pseudocode followed and
// reproduced as comments below was retrieved from
// https://www.cs.rochester.edu/research/synchronization/pseudocode/queues.html
// on May 16, 2025.
package nbcq

import (
	"sync"
	"sync/atomic"
)

// structure pointer_t {ptr: pointer to node_t, count: unsigned integer}
type pointer[T any] struct {
	ptr   *node[T]
	count uint64
}

// structure node_t {value: data type, next: pointer_t}
type node[T any] struct {
	value atomic.Value
	next  atomicPointer[T]
}

// structure queue_t {Head: pointer_t, Tail: pointer_t}
type Queue[T any] struct {
	head atomicPointer[T]
	tail atomicPointer[T]
}

// initialize(Q: pointer to queue_t)
func (q *Queue[T]) Init(p *NodePool[T]) {
	// node = new_node()      // Allocate a free node
	// node->next.ptr = NULL  // Make it the only node in the linked list
	node := p.get()

	// Q->Head.ptr = Q->Tail.ptr = node	 // Both Head and Tail point to it
	q.head.Store(pointer[T]{ptr: node})
	q.tail.Store(pointer[T]{ptr: node})
}

// enqueue(Q: pointer to queue_t, value: data type)
func (q *Queue[T]) PushBack(p *NodePool[T], value T) {
	// E1: node = new_node()      // Allocate a new node from the free list
	// E2: node->value = value	  // Copy enqueued value into node
	// E3: node->next.ptr = NULL  // Set next pointer of node to NULL
	node := p.get()
	node.value.Store(value)

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
func (q *Queue[T]) PopFront(p *NodePool[T]) (T, bool) {
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
				value := next.ptr.value.Load().(T)
				// Try to swing Head to the next node
				// D13: if CAS(&Q->Head, head, <next.ptr, head.count+1>)
				if q.head.CompareAndSwap(head, pointer[T]{ptr: next.ptr, count: head.count + 1}) {
					// D14: break           // Dequeue is done.  Exit loop

					// Instead of breaking the loop, the post-loop steps are
					// moved here to avoid expanding the scope of the value
					// variable.

					// D19: free(head.ptr)  // It is safe now to free the old node

					// Overwrite with zero value to allow any held resources to
					// be garbage collected before storing this node in the
					// pool. This uses an atomic.Value to avoid the write
					// getting flagged by the race detector. The reference
					// algorithm doesn't require use of an atomic operation here
					// because while step D12 above might read an incorrect
					// value, it wouldn't end up using it because the CAS would
					// fail.
					head.ptr.value.Store(*new(T))

					// Clear the next pointer so the node is ready for re-use,
					// but maintain the associated count so that it will still
					// not compare equal to the dummy node's next pointer. This
					// ensures that other goroutines that still have references
					// to the can still safely use this node's next pointer in
					// comparisons even while it's in the pool and even after
					// re-use, because the count will always differ.
					head.ptr.next.Store(pointer[T]{count: next.count})

					// Stash the node away for reuse.
					p.put(head.ptr)

					// D20: return TRUE     // Queue was not empty, dequeue succeeded
					return value, true
				} // D15: endif
			} // D16: endif
		} // D17: endif
	} // D18: endloop
}

type NodePool[T any] struct {
	inner sync.Pool
}

func (p *NodePool[T]) get() *node[T] {
	n, _ := p.inner.Get().(*node[T])
	if n == nil {
		n = &node[T]{}
		// Since the zero value of atomic.Value is different than the zero value
		// of pointer[T], we must explicitly store a value to allow CAS
		// operations that expect the old value to be a zero pointer[T].
		n.next.Store(pointer[T]{})
	}
	return n
}

func (p *NodePool[T]) put(n *node[T]) {
	p.inner.Put(n)
}

// This implementation depends on Go's implementation of atomic operations for
// arbitrary values, which is itself non-trival and includes a loop that has
// potential for live-locking.
type atomicPointer[T any] atomic.Value

func (p *atomicPointer[T]) Load() pointer[T] {
	return (*atomic.Value)(p).Load().(pointer[T])
}

func (p *atomicPointer[T]) Store(v pointer[T]) {
	(*atomic.Value)(p).Store(v)
}

func (p *atomicPointer[T]) CompareAndSwap(old, new pointer[T]) bool {
	return (*atomic.Value)(p).CompareAndSwap(old, new)
}
