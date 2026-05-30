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
	"github.com/petenewcomb/psg-go/psgfn"
)

// "Hello world" example that uses psg to run a couple of tasks and gather their
// results.
//
//nolint:errcheck,gosec // concise example code for readme
func Example_hello() {
	ctx := context.Background()
	job := psg.New(ctx)
	defer job.CancelAndWait() // hygiene

	var results []string
	gatherer := psg.NewGatherer(
		func(ctx context.Context, result string, err error) error {
			results = append(results, result)
			return nil
		},
	)

	// Bind a string to a task that submits it to the gatherer after a short delay.
	newRunner := func(s string) psg.TaskRunner0 {
		return psg.NewTaskRunner0(job, psgfn.TaskFunc0(func(ctx context.Context) error {
			time.Sleep(1 * time.Millisecond)
			return gatherer.Submit(ctx, job, s, nil)
		}))
	}

	newRunner("Hello").Start(ctx)
	newRunner("world!").Start(ctx)

	job.CloseAndGatherAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
