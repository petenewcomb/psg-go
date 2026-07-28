// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"fmt"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"
)

// Example_resequencer scatters work that completes out of order and reassembles
// the results into submission order with a [streampool.Resequencer]. Each task
// is tagged with a sequence number; later tasks are made to finish first, yet
// the resequencer delivers them to the printer in order, so the output is
// deterministic.
//
//nolint:errcheck,gosec // concise example code
func Example_resequencer() {
	ctx := context.Background()
	wave := streampool.NewWave()

	words := []string{"order", "out", "of", "chaos"}

	// The sink: print each word in sequence order. Because the resequencer is a
	// single serial instance, the handler needs no locking.
	printer := streampool.NewFnResequencer[string](wave, 0,
		func(_ context.Context, word string, _ error) error {
			fmt.Println(word)
			return nil
		})

	// Workers finish in reverse order (word 3 first, word 0 last), but each
	// submits at its original index, so the printer still emits them in order.
	worker := streampool.NewLauncher[int](streampool.HandlerFunc[int](
		func(ctx context.Context, i int, _ error) error {
			time.Sleep(time.Duration(len(words)-i) * time.Millisecond)
			return printer.Submit(ctx, uint64(i), words[i])
		}))

	for i := range words {
		worker.In(wave).Submit(ctx, i)
	}

	wave.CloseAndSkimAll(ctx)

	// Output:
	// order
	// out
	// of
	// chaos
}
