// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"crypto/md5" //nolint:gosec // non-cryptographic use case
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// Pipeline demonstrates the use of multiple psg pools to re-implement the
// MD5All function in [errgroup's pipeline example], which itself is a
// re-implementation of the MD5All function described in [Go Concurrency
// Patterns: Pipelines and cancellation].
//
// [errgroup's pipeline example]: https://pkg.go.dev/golang.org/x/sync@v0.13.0/errgroup#example-Group-Pipeline
// [Go Concurrency Patterns: Pipelines and cancellation]: https://blog.golang.org/pipelines
func Example_pipeline() {
	m, err := MD5All(context.Background(), ".")
	if err != nil {
		log.Fatal(err)
	}

	for k, sum := range m {
		fmt.Printf("%s:\t%x\n", k, sum)
	}
}

// MD5All reads all the files in the file tree rooted at root and returns a map
// from file path to the MD5 sum of the file's contents. If the directory walk
// fails or any read operation fails, MD5All returns an error.
func MD5All(ctx context.Context, root string) (map[string][md5.Size]byte, error) {

	// Create the scatter-gather wave, setting up a deferred call to
	// Cancel to terminate outstanding tasks in case of error.
	ctx, wave := psg.NewWave(ctx)
	defer wave.CancelAndWait()

	// Cap concurrent digesting tasks at the number of cores available
	// to the program, since they should be CPU-bound.
	digestLimit := psg.NewSemaphore(runtime.GOMAXPROCS(-1))

	// Collects the final results in m as they are completed
	m := make(map[string][md5.Size]byte)
	newDigestGatherer := func(path string) psg.Gatherer[[md5.Size]byte] {
		return psg.NewGatherer(psgfn.HandlerFunc[[md5.Size]byte](
			func(ctx context.Context, sum [md5.Size]byte, err error) error {
				m[path] = sum
				return nil
			},
		))
	}

	newDigestingRunner := func(path string, data []byte) psg.TaskRunner0 {
		gatherer := newDigestGatherer(path)
		return psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			//nolint:gosec // non-cryptographic use case
			return gatherer.Submit(ctx, wave, md5.Sum(data))
		}), psg.WithLimits(digestLimit))
	}

	// Creates a gatherer for a reading task whose handler dispatches a
	// digesting task with the bytes that were read.
	newReadGatherer := func(path string) psg.Gatherer[[]byte] {
		return psg.NewGatherer(psgfn.HandlerFunc[[]byte](
			func(ctx context.Context, data []byte, err error) error {
				return newDigestingRunner(path, data).Start(ctx, wave)
			},
		))
	}

	// No need for a pool to limit how many file reading tasks run concurrently
	// since they should be I/O-bound and will be subject to backpressure from
	// the digesters.
	newReadingRunner := func(path string) psg.TaskRunner0 {
		gatherer := newReadGatherer(path)
		return psg.NewTaskRunner0(psgfn.TaskFunc0(func(ctx context.Context) error {
			//nolint:gosec // path from known source
			data, err := os.ReadFile(path)
			return gatherer.SubmitErr(ctx, wave, data, err)
		}))
	}

	// Walk the tree and launch a reading task for each regular file.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return newReadingRunner(path).Start(ctx, wave)
	})
	if err != nil {
		return nil, err
	}

	// Gather task results until there are no more outstanding tasks.
	if err := wave.CloseAndGatherAll(ctx); err != nil {
		return nil, err
	}

	return m, nil
}
