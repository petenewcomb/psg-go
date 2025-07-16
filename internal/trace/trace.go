// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package trace provides a thin wrapper around runtime/trace with conditional
// tracing controlled by a global atomic pointer and configurable prefixes for
// region types and log categories. Trace instrumentation that uses this wrapper
// can be enabled or disabled via [SetPrefix], which takes its default from the
// environment variable PSGTRACEINTERNALS. If unset or set to the value "-"
// (U+002D hyphen-minus), tracing will be disabled by default. If set to any
// other value, tracing will be enabled using the value as the region type and
// log category prefix. If the value begins with "+" (U+002B plus sign), the
// leading "+" will be stripped and the remainder used as the prefix.
package trace

import (
	"context"
	"fmt"
	"os"
	"runtime/trace"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

var (
	// prefix stores the current prefix string. When nil, tracing is disabled.
	// When non-nil, tracing is enabled and the string value is used as prefix.
	prefix atomic.Pointer[string]
)

func init() {
	if envPrefix, ok := os.LookupEnv("PSGTRACEINTERNALS"); ok && envPrefix != "-" {
		if envPrefix != "" && envPrefix[0] == '+' {
			envPrefix = envPrefix[1:]
		}
		SetPrefix(&envPrefix)
	}
}

// SetPrefix sets the global prefix for region types and log categories.
// Setting a non-nil prefix enables tracing.
func SetPrefix(p *string) {
	prefix.Store(p)
}

// IsEnabled returns whether tracing is currently enabled.
func IsEnabled() bool {
	return prefix.Load() != nil
}

type Region struct {
	r *trace.Region
}

var noopRegion = Region{r: &trace.Region{}}

func (r Region) End() {
	if r == noopRegion {
		return
	}
	r.r.End()
}

// StartRegion starts a new trace region if tracing is enabled.
// The regionType is automatically prefixed with the global prefix.
func StartRegion(ctx context.Context, regionType string) Region {
	p := prefix.Load()
	if p == nil {
		return noopRegion
	}
	prefixedType := *p + regionType
	return Region{r: trace.StartRegion(ctx, prefixedType)}
}

// WithRegion executes fn within a trace region if tracing is enabled.
// The regionType is automatically prefixed with the global prefix.
func WithRegion(ctx context.Context, regionType string, fn func()) {
	p := prefix.Load()
	if p == nil {
		fn()
		return
	}
	prefixedType := *p + regionType
	trace.WithRegion(ctx, prefixedType, fn)
}

// Log adds a log event to the trace if tracing is enabled.
// The category is automatically prefixed with the global prefix.
func Log(ctx context.Context, category, message string) {
	p := prefix.Load()
	if p == nil {
		return
	}
	prefixedCategory := *p + category
	trace.Log(ctx, prefixedCategory, message)
}

// Logf adds a formatted log event to the trace if tracing is enabled.
// The category is automatically prefixed with the global prefix.
func Logf(ctx context.Context, category, format string, args ...any) {
	p := prefix.Load()
	if p == nil {
		return
	}
	prefixedCategory := *p + category
	trace.Logf(ctx, prefixedCategory, format, args...)
}

// LongLogf adds one or more log events to the trace if tracing is enabled,
// breaking the message up as needed to avoid truncation due to per-event size
// limit. The category is automatically prefixed with the global prefix.
// See [MaxEventTrailerDataSize], defined to be 1<<10
// [MaxEventTrailerDataSize]: https://cs.opensource.google/go/go/+/master:src/internal/trace/tracev2/events.go;drc=6c3b5a2798c83d583cb37dba9f39c47300d19f1f;l=588
//
//nolint:lll // long url
func LongLogf(ctx context.Context, category, header, continuationHeader, trailer, format string, args ...any) {
	if !trace.IsEnabled() {
		return
	}

	const maxChunkSize = 1 << 10

	message := fmt.Sprintf(format, args...)
	for {
		targetChunkSize := maxChunkSize - len(header) - len(trailer)
		if len(message) <= targetChunkSize {
			break
		}
		chunk := message[:targetChunkSize]

		// Prefer breaking on line boundaries, otherwise break between runes.
		lastNewlineIndex := strings.LastIndexByte(chunk, '\n')
		if lastNewlineIndex != -1 {
			chunk = message[:lastNewlineIndex]
			message = message[len(chunk)+1:]
		} else {
			l := len(chunk)
			for l > 0 && !utf8.RuneStart(chunk[l-1]) {
				l--
			}
			chunk = chunk[:l]
			message = message[l:]
		}
		Log(ctx, category, header+chunk+trailer)

		// Use the continuation header for subsequent chunks.
		header = continuationHeader
	}

	Log(ctx, category, header+message+trailer)
}

type Task struct {
	t *trace.Task
}

var noopTask = Task{t: &trace.Task{}}

func (t Task) End() {
	if t == noopTask {
		return
	}
	t.t.End()
}

// NewTask creates a new task if tracing is enabled.
// The taskType is automatically prefixed with the global prefix.
func NewTask(ctx context.Context, taskType string) (context.Context, Task) {
	p := prefix.Load()
	if p == nil {
		return ctx, noopTask
	}
	prefixedType := *p + taskType
	tCtx, t := trace.NewTask(ctx, prefixedType)
	return tCtx, Task{t: t}
}
