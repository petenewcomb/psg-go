// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"crypto/md5" //nolint:gosec // non-cryptographic use case
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/petenewcomb/streampool"
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
	ctx, wave := streampool.NewWave(ctx)
	defer wave.CancelAndWait()

	// Cap concurrent digesting tasks at the number of cores available
	// to the program, since they should be CPU-bound.
	digestLimit := streampool.NewSemaphore(runtime.GOMAXPROCS(-1))

	// Collects the final results in m as they are completed
	m := make(map[string][md5.Size]byte)
	newDigestSkimmer := func(path string) streampool.Skimmer[[md5.Size]byte] {
		return streampool.NewSkimmer(wave, streampool.HandlerFunc[[md5.Size]byte](
			func(ctx context.Context, sum [md5.Size]byte, err error) error {
				m[path] = sum
				return nil
			},
		))
	}

	newDigestingRunner := func(path string, data []byte) streampool.TaskLauncher {
		skimmer := newDigestSkimmer(path)
		return streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
			//nolint:gosec // non-cryptographic use case
			return skimmer.Submit(ctx, md5.Sum(data))
		}, streampool.WithLimits(digestLimit))
	}

	// Creates a skimmer for a reading task whose handler dispatches a
	// digesting task with the bytes that were read.
	newReadSkimmer := func(path string) streampool.Skimmer[[]byte] {
		return streampool.NewSkimmer(wave, streampool.HandlerFunc[[]byte](
			func(ctx context.Context, data []byte, err error) error {
				return newDigestingRunner(path, data).Start(ctx)
			},
		))
	}

	// No need for a pool to limit how many file reading tasks run concurrently
	// since they should be I/O-bound and will be subject to backpressure from
	// the digesters.
	newReadingRunner := func(path string) streampool.TaskLauncher {
		skimmer := newReadSkimmer(path)
		return streampool.NewTaskLauncher(wave, func(ctx context.Context) error {
			//nolint:gosec // path from known source
			data, err := os.ReadFile(path)
			return skimmer.SubmitResult(ctx, data, err)
		})
	}

	// Walk the tree and launch a reading task for each regular file.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return newReadingRunner(path).Start(ctx)
	})
	if err != nil {
		return nil, err
	}

	// Skim task results until there are no more outstanding tasks.
	if err := wave.CloseAndSkimAll(ctx); err != nil {
		return nil, err
	}

	return m, nil
}
