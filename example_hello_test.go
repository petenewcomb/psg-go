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
	job := psg.NewJob(ctx)
	defer job.CancelAndWait() // hygiene

	// Binds a string to a task function that returns the string after a short delay.
	newTaskFn := func(s string) psg.TaskFunc[string] {
		return func(context.Context) (string, error) {
			time.Sleep(1 * time.Millisecond)
			return s, nil
		}
	}

	var results []string
	gatherOp := psg.NewGatherOp(
		func(ctx context.Context, result string, err error) error {
			results = append(results, result)
			return nil
		},
	)

	gatherOp.Scatter(ctx, job, newTaskFn("Hello"))
	gatherOp.Scatter(ctx, job, newTaskFn("world!"))

	job.CloseAndGatherAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
