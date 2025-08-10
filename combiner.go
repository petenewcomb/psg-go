// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"
)

type errCombiner[I, O any] struct {
	err error
}

func (c errCombiner[I, O]) Combine(ctx context.Context, input I, inputErr error) (time.Time, error) {
	return time.Now(), c.err
}

func (c errCombiner[I, O]) Flush(ctx context.Context) (O, error) {
	return *new(O), c.err
}
