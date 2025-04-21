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

// README code snippet
//
//nolint:errcheck,unused
func example_readme() {
	ctx := context.Background()
	pool := psg.NewPool(2)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait() // hygiene

	newTask := func(s string) psg.TaskFunc[string] {
		return func(context.Context) (string, error) {
			time.Sleep(1 * time.Millisecond)
			return s, nil
		}
	}

	var results []string
	gather := func(ctx context.Context, result string, err error) error {
		results = append(results, result)
		return nil
	}

	psg.Scatter(ctx, pool, newTask("Hello"), gather)
	psg.Scatter(ctx, pool, newTask("world!"), gather)

	job.Finish(ctx)
	fmt.Println(strings.Join(results, " "))
}
