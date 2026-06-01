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

// "Hello world" example that uses psg to run a couple of tasks and skim their
// results.
//
//nolint:errcheck,gosec // concise example code for readme
func Example_hello() {
	ctx, wave := psg.NewWave(context.Background())
	defer wave.CancelAndWait() // hygiene

	var results []string
	skimmer := psg.NewSkimmer(wave, psgfn.HandlerFunc[string](
		func(ctx context.Context, result string, err error) error {
			results = append(results, result)
			return nil
		},
	))

	// Bind a string to a task that submits it to the skimmer after a short delay.
	newRunner := func(s string) psg.Launcher0 {
		return psg.NewLauncher0(wave, psgfn.TaskFunc0(func(ctx context.Context) error {
			time.Sleep(1 * time.Millisecond)
			return skimmer.Submit(ctx, s)
		}))
	}

	newRunner("Hello").Start(ctx)
	newRunner("world!").Start(ctx)

	wave.CloseAndSkimAll(ctx)
	fmt.Println(strings.Join(results, " "))
}
