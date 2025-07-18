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
// This implementation depends on Go's [atomic.Value] which provides atomic
// operations for arbitrary values. It would be good to use hardware-based
// 128-bit atomic operations instead, but they are not yet supported by Go (see
// https://github.com/golang/go/issues/61236). Other than the fact that
// atomic.Value is itself non-trival and includes a loop that has potential for
// live-locking, the major downside to its use is the unavoidable heap
// allocations of the (pointer, counter) structures used by the algorithm to
// solve the "ABA" problem. Pooling and reuse of these structures would break
// the invariants of atomic.Value and cause races within this code because both
// expect the contents of the structure to be immutable as long as a reference
// is held -- something that only the garbage collector can guarantee. Alternate
// solutions to the ABA problem (e.g., hazard pointers) are more complex and
// less compatible with Go.
package nbcq

import (
	"context"
	"sync"
	"sync/atomic"

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
	next  atomic.Value
}

// structure queue_t {Head: pointer_t, Tail: pointer_t}
type Queue[T any] struct {
	head atomic.Value
	tail atomic.Value
}

// initialize(Q: pointer to queue_t)
func (q *Queue[T]) Init(p *Pool[T]) {
	// node = new_node()      // Allocate a free node
	// node->next.ptr = NULL  // Make it the only node in the linked list
	node := p.getNode()

	// Q->Head.ptr = Q->Tail.ptr = node	 // Both Head and Tail point to it
	q.head.Store(pointer[T]{ptr: node})
	q.tail.Store(pointer[T]{ptr: node})
}

// enqueue(Q: pointer to queue_t, value: data type)
//
//nolint:gocritic // ignore commented-out (pseudo-)code
func (q *Queue[T]) PushBack(p *Pool[T], value T) {
	traceRegion := "nbcq.PushBack"

	// E1: node = new_node()      // Allocate a new node from the free list
	// E2: node->value = value	  // Copy enqueued value into node
	// E3: node->next.ptr = NULL  // Set next pointer of node to NULL
	node := p.getNode()
	node.value.Store(p.getValue(value))

	// E4: loop  // Keep trying until Enqueue is done
	for {
		// E5: tail = Q->Tail         // Read Tail.ptr and Tail.count together
		tailAny := q.tail.Load()
		tail := tailAny.(pointer[T])
		// E6: next = tail.ptr->next  // Read next ptr and count fields together
		nextAny := tail.ptr.next.Load()
		next := nextAny.(pointer[T])
		// E7: if tail == Q->Tail     // Are tail and next consistent?
		if tailAny == q.tail.Load().(pointer[T]) {
			// Was Tail pointing to the last node?
			// E8: if next.ptr == NULL
			if next.ptr == nil {
				// Try to link node at the end of the linked list
				// E9: if CAS(&tail.ptr->next, next, <node, next.count+1>)
				if tail.ptr.next.CompareAndSwap(nextAny, pointer[T]{ptr: node, count: next.count + 1}) {
					// E10: break	  // Enqueue is done.  Exit loop

					if trace.IsEnabled() {
						trace.Logf(context.Background(), traceRegion, "Queue=%p item=%d enqueued", q, tail.count)
					}

					// Instead of breaking the loop, the post-loop step is
					// moved here to avoid expanding the scope of the tail
					// variable.

					// Enqueue is done.  Try to swing Tail to the inserted node
					// E17: CAS(&Q->Tail, tail, <node, tail.count+1>)
					q.tail.CompareAndSwap(tailAny, pointer[T]{ptr: node, count: tail.count + 1})
					return
				} // E11: endif
			} else {
				// E12: else          // Tail was not pointing to the last node
				// Try to swing Tail to the next node
				// E13: CAS(&Q->Tail, tail, <next.ptr, tail.count+1>)
				q.tail.CompareAndSwap(tailAny, pointer[T]{ptr: next.ptr, count: tail.count + 1})
			} // E14: endif
		} // E15: endif
	} // E16: endloop
}

// dequeue(Q: pointer to queue_t, pvalue: pointer to data type): boolean
//
//nolint:gocritic // ignore commented-out (pseudo-)code
func (q *Queue[T]) PopFront(p *Pool[T]) (T, bool) {
	traceRegion := "nbcq.PopFront"

	// D1: loop // Keep trying until Dequeue is done
	for {
		// D2: head = Q->Head         // Read Head
		headAny := q.head.Load()
		head := headAny.(pointer[T])
		// D3: tail = Q->Tail         // Read Tail
		tailAny := q.tail.Load()
		tail := tailAny.(pointer[T])
		// D4: next = head.ptr->next  // Read Head.ptr->next
		next := head.ptr.next.Load().(pointer[T])
		// D5: if head == Q->Head     // Are head, tail, and next consistent?
		if headAny == q.head.Load().(pointer[T]) {
			// D6: if head.ptr == tail.ptr  // Is queue empty or Tail falling behind?
			if head.ptr == tail.ptr {
				// D7: if next.ptr == NULL  // Is queue empty?
				if next.ptr == nil {
					// D8: return FALSE     // Queue is empty, couldn't dequeue
					return *new(T), false
				} // D9: endif
				// Tail is falling behind.  Try to advance it
				// D10: CAS(&Q->Tail, tail, <next.ptr, tail.count+1>)
				q.tail.CompareAndSwap(tailAny, pointer[T]{ptr: next.ptr, count: tail.count + 1})
			} else {
				// D11: else                // No need to deal with Tail
				// Read value before CAS
				// Otherwise, another dequeue might free the next node
				// D12: *pvalue = next.ptr->value
				valuePointer := next.ptr.value.Load()
				// Try to swing Head to the next node
				// D13: if CAS(&Q->Head, head, <next.ptr, head.count+1>)
				if q.head.CompareAndSwap(headAny, pointer[T]{ptr: next.ptr, count: head.count + 1}) {
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
					p.putValue(valuePointer)

					// Stash the node away for reuse.
					p.putNode(head.ptr)

					// D20: return TRUE     // Queue was not empty, dequeue succeeded
					return value, true
				} // D15: endif
			} // D16: endif
		} // D17: endif
	} // D18: endloop
}

type Pool[T any] struct {
	nodes  sync.Pool
	values sync.Pool
}

func (p *Pool[T]) getNode() *node[T] {
	n, _ := p.nodes.Get().(*node[T])
	if n == nil {
		n = &node[T]{}
		// Since the zero value of atomic.Value is different than the zero value
		// of pointer[T], we must explicitly store a value to allow CAS
		// operations that expect the old value to be a zero pointer[T].
		n.next.Store(pointer[T]{})
	}
	return n
}

func (p *Pool[T]) putNode(n *node[T]) {
	p.nodes.Put(n)
}

func (p *Pool[T]) getValue(v T) *T {
	vp, _ := p.values.Get().(*T)
	if vp == nil {
		vp = new(T)
	}
	*vp = v
	return vp
}

func (p *Pool[T]) putValue(vp *T) {
	// Clear the value before pooling to allow garbage collection of anything it
	// might reference
	*vp = *new(T)
	p.values.Put(vp)
}
