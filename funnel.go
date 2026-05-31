// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"
)

// errAccumulator is the framework's substitute Accumulator used when a user
// FunnelFactory misbehaves (returns nil or panics during construction).
// Every call simply surfaces the recorded error; nothing accumulates.
type errAccumulator[T any] struct {
	err error
}

func (c errAccumulator[T]) Accumulate(ctx context.Context, value T, err error) (time.Time, error) {
	return time.Now(), c.err
}

func (c errAccumulator[T]) Flush(ctx context.Context) error {
	return c.err
}
