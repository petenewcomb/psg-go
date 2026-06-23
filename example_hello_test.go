// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	"github.com/petenewcomb/streampool"
)

// "Hello world" example that uses psg to run a couple of tasks and skim their
// results.
//
//nolint:errcheck,gosec // concise example code for readme
func Example_hello() {
	ctx := context.Background()
	var wave streampool.Wave

	var results []string
	skimmer := streampool.NewFnSkimmer(
		func(ctx context.Context, result string, err error) error {
			results = append(results, result)
			return nil
		},
	)

	// Bind a string to a task that submits it to the skimmer after a short delay.
	newRunner := func(s string) streampool.TaskLauncher {
		return streampool.NewTaskLauncher(func(ctx context.Context) error {
			time.Sleep(1 * time.Millisecond)
			return skimmer.Submit(ctx, s)
		})
	}

	newRunner("Hello").In(&wave).Start(ctx)
	newRunner("world!").In(&wave).Start(ctx)

	wave.CloseAndSkimAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
