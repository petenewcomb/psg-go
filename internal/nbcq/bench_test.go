// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package nbcq

import (
	"testing"
)

func BenchmarkQueue(b *testing.B) {
	var q Queue[int]
	q.Init()

	b.Run("PushPop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			q.PushBack(i)
			if _, ok := q.PopFront(); !ok {
				b.Fatal("PopFront failed")
			}
		}
	})
}
