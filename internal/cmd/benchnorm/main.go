// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// benchnorm normalizes psg-go benchmark results to per-task metrics.
// It reads benchmark output and adjusts all metrics so that 1 op = 1 task.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/perf/benchfmt"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [input.txt]...\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Normalizes benchmark results to per-task metrics.\n")
		fmt.Fprintf(os.Stderr, "If no input files are specified, reads from stdin.\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	writer := benchfmt.NewWriter(os.Stdout)

	if flag.NArg() == 0 {
		if err := processFile("<stdin>", os.Stdin, writer); err != nil {
			log.Fatal(err)
		}
	} else {
		for _, filename := range flag.Args() {
			f, err := os.Open(filename)
			if err != nil {
				log.Fatal(err)
			}
			if err := processFile(filename, f, writer); err != nil {
				_ = f.Close()
				log.Fatal(err)
			}
			if err := f.Close(); err != nil {
				log.Fatal(err)
			}
		}
	}
}

func processFile(filename string, f *os.File, writer *benchfmt.Writer) error {
	reader := benchfmt.NewReader(f, filename)

	for reader.Scan() {
		rec := reader.Result()

		switch rec := rec.(type) {
		case *benchfmt.Result:
			normalized, err := normalizeResult(rec)
			if err != nil {
				return fmt.Errorf("normalizing result: %w", err)
			}
			if err := writer.Write(normalized); err != nil {
				return fmt.Errorf("writing result: %w", err)
			}

		case *benchfmt.UnitMetadata:
			// Pass through unit metadata unchanged
			if err := writer.Write(rec); err != nil {
				return fmt.Errorf("writing unit metadata: %w", err)
			}

		case *benchfmt.SyntaxError:
			// Report syntax errors
			return fmt.Errorf("syntax error at %s:%d: %v", rec.FileName, rec.Line, rec)
		}
	}

	if err := reader.Err(); err != nil {
		return fmt.Errorf("reading benchmarks: %w", err)
	}

	return nil
}

func normalizeResult(r *benchfmt.Result) (*benchfmt.Result, error) {
	// Find tasks/op value
	tasksPerOp := 0.0
	tasksFound := false

	for _, v := range r.Values {
		if v.Unit == "tasks/op" {
			tasksPerOp = v.Value
			tasksFound = true
			break
		}
	}

	if !tasksFound || tasksPerOp == 0 {
		// No tasks/op metric or zero tasks, return unchanged
		return r, nil
	}

	// Create a new result with normalized values
	normalized := &benchfmt.Result{
		Config: r.Config,
		Name:   r.Name,
		Iters:  int(float64(r.Iters) * tasksPerOp),
		Values: make([]benchfmt.Value, 0, len(r.Values)),
	}

	// Normalize all metrics
	for _, v := range r.Values {
		newValue := v

		// Scale any metric ending with /op
		if strings.HasSuffix(v.Unit, "/op") {
			newValue.Value = v.Value / tasksPerOp
		}

		normalized.Values = append(normalized.Values, newValue)
	}

	return normalized, nil
}
