// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/petenewcomb/psg-go"
)

func newTask(s string) psg.TaskFunc[string] {
	return func(context.Context) (string, error) {
		time.Sleep(1 * time.Millisecond)
		return s, nil
	}
}

// "Hello world" example that uses psg to run a couple of tasks and gather their
// results.
//
//nolint:errcheck
func Example_hello() {
	ctx := context.Background()
	pool := psg.NewPool(2)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	var results []string
	gather := func(ctx context.Context, result string, err error) error {
		results = append(results, result)
		return nil
	}

	psg.Scatter(ctx, pool, newTask("Hello"), gather)
	psg.Scatter(ctx, pool, newTask("world!"), gather)

	job.CloseAndGatherAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
