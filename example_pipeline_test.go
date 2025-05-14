// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"crypto/md5"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/petenewcomb/psg-go"
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

	// Create the scatter-gather job, setting up a deferred call to Cancel to
	// terminate outstanding tasks in case of error.
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	// Run digesting tasks in a Pool limited to the number of cores available to
	// the program, since it should be CPU-bound.
	digesterPool := psg.NewTaskPool(job, runtime.NumCPU())
	newDigestingTask := func(data []byte) psg.TaskFunc[[md5.Size]byte] {
		return func(ctx context.Context) ([md5.Size]byte, error) {
			return md5.Sum(data), nil
		}
	}

	// Collects the final results in m as they are completed
	m := make(map[string][md5.Size]byte)
	newDigestGather := func(path string) *psg.Gather[[md5.Size]byte] {
		return psg.NewGather(
			func(ctx context.Context, sum [md5.Size]byte, err error) error {
				m[path] = sum
				return nil
			},
		)
	}

	// Allow many file reading tasks to run concurrently since they should be
	// I/O-bound.
	readerPool := psg.NewTaskPool(job, 100)
	newReadingTask := func(path string) psg.TaskFunc[[]byte] {
		return func(ctx context.Context) ([]byte, error) {
			return os.ReadFile(path)
		}
	}

	// Creates gathers for reading tasks that launch digesting tasks.
	newReadGather := func(path string) *psg.Gather[[]byte] {
		return psg.NewGather(
			func(ctx context.Context, data []byte, err error) error {
				return newDigestGather(path).
					Scatter(ctx, digesterPool, newDigestingTask(data))
			},
		)
	}

	// Walk the tree and launch a reading task for each regular file.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return newReadGather(path).Scatter(ctx, readerPool, newReadingTask(path))
	})
	if err != nil {
		return nil, err
	}

	// Gather task results until there are no more outstanding tasks.
	if err := job.CloseAndGatherAll(ctx); err != nil {
		return nil, err
	}

	return m, nil
}
