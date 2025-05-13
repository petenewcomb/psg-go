// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	// Superfluous alias needed to work around
	// https://github.com/golang/go/issues/12794
	psg "github.com/petenewcomb/psg-go"
)

// "Hello world" example that uses psg to run a couple of tasks and gather their
// results.
//
//nolint:errcheck
func Example_hello() {
	ctx := context.Background()
	pool := psg.NewTaskPool(2)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait() // hygiene

	// Binds a string to a task function that returns the string after a short delay.
	newTask := func(s string) psg.TaskFunc[string] {
		return func(context.Context) (string, error) {
			time.Sleep(1 * time.Millisecond)
			return s, nil
		}
	}

	var results []string
	gather := psg.NewGather(
		func(ctx context.Context, result string, err error) error {
			results = append(results, result)
			return nil
		},
	)

	gather.Scatter(ctx, pool, newTask("Hello"))
	gather.Scatter(ctx, pool, newTask("world!"))

	job.CloseAndGatherAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
