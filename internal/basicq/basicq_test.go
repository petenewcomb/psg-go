// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package basicq_test

import (
	"testing"

	"github.com/petenewcomb/psg-go/internal/basicq"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// Add a basic functional test to verify operations directly
func TestQueueBasicFunctionality(t *testing.T) {
	q := basicq.Queue[int]{}

	// Test empty queue
	require.Equal(t, 0, q.Len())
	_, ok := q.PopFront()
	require.False(t, ok)

	// Test adding and removing elements
	q.PushBack(1)
	q.PushBack(2)
	q.PushBack(3)

	require.Equal(t, 3, q.Len())

	val, ok := q.PopFront()
	require.True(t, ok)
	require.Equal(t, 1, val)

	val, ok = q.PopFront()
	require.True(t, ok)
	require.Equal(t, 2, val)

	val, ok = q.PopFront()
	require.True(t, ok)
	require.Equal(t, 3, val)

	require.Equal(t, 0, q.Len())
}

// TestQueueWithRapid uses rapid state machine testing to verify queue correctness
func TestQueueWithRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// The system under test
		q := basicq.Queue[int]{}

		// The model (reference implementation)
		var model []int

		t.Repeat(map[string]func(*rapid.T){
			// PushBack operation
			"pushBack": func(t *rapid.T) {
				// Generate a random value to push
				val := rapid.Int().Draw(t, "value")

				// Update actual implementation
				q.PushBack(val)

				// Update model
				model = append(model, val)

				// Verify length matches after operation
				require.Equal(t, len(model), q.Len(), "Length mismatch after PushBack")
			},

			// PopFront operation
			"popFront": func(t *rapid.T) {
				// Skip if empty - nothing to pop
				if len(model) == 0 {
					t.Skip("Queue is empty, nothing to pop")
				}

				// Get expected value from model
				expected := model[0]
				model = model[1:]

				// Get actual value from queue
				val, ok := q.PopFront()

				// Verify the operation succeeded
				require.True(t, ok, "PopFront failed on non-empty queue")
				require.Equal(t, expected, val, "PopFront returned wrong value")

				// Verify length matches after operation
				require.Equal(t, len(model), q.Len(), "Length mismatch after PopFront")
			},

			// Check invariants between actions
			"": func(t *rapid.T) {
				// Verify length matches the model
				require.Equal(t, len(model), q.Len(), "Length mismatch in invariant check")

				// If model is empty, verify queue behaves as empty
				if len(model) == 0 {
					_, ok := q.PopFront()
					require.False(t, ok, "PopFront should fail on empty queue")
				}
			},
		})
	})
}
