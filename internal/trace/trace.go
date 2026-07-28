// Package trace wraps runtime/trace with an on/off switch and a region-type
// filter, both controlled by the environment variable PSGTRACEINTERNALS. If
// unset or set to the value "-" (U+002D hyphen-minus), tracing is disabled. If
// set to the empty string, every region and log category is traced. Any other
// value is a comma-separated list of name prefixes, and only regions and
// categories matching one of them are traced (e.g.
// "permits,heldPermit,rdvq.Notifier"). If the value begins with "+" (U+002B
// plus sign), the leading "+" is stripped and the remainder used as the list.
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

// filterList is the parsed enablement state: nil disables tracing entirely; a
// pointer to an empty slice traces everything; a pointer to a non-empty slice
// traces only names matching one of its prefixes.
var filterList atomic.Pointer[[]string]

func init() {
	if env, ok := os.LookupEnv("PSGTRACEINTERNALS"); ok && env != "-" {
		if env != "" && env[0] == '+' {
			env = env[1:]
		}
		SetFilter(&env)
	}
}

// SetFilter sets the global filter from a comma-separated prefix list ("" =
// trace everything). Setting a non-nil filter enables tracing; nil disables it.
func SetFilter(spec *string) {
	if spec == nil {
		filterList.Store(nil)
		return
	}
	var prefixes []string
	for p := range strings.SplitSeq(*spec, ",") {
		if p = strings.TrimSpace(p); p != "" {
			prefixes = append(prefixes, p)
		}
	}
	if prefixes == nil {
		prefixes = []string{}
	}
	filterList.Store(&prefixes)
}

// IsEnabled returns whether tracing is currently enabled at all. A name may
// still be excluded by the filter; call sites use IsEnabled only to skip
// argument construction on the fully-disabled fast path.
func IsEnabled() bool {
	return filterList.Load() != nil
}

// enabledFor reports whether the given region type or log category passes the
// filter.
func enabledFor(name string) bool {
	prefixes := filterList.Load()
	if prefixes == nil {
		return false
	}
	if len(*prefixes) == 0 {
		return true
	}
	for _, p := range *prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
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

// StartRegion starts a new trace region if tracing is enabled for regionType.
func StartRegion(ctx context.Context, regionType string) Region {
	if !enabledFor(regionType) {
		return noopRegion
	}
	return Region{r: trace.StartRegion(ctx, regionType)}
}

// WithRegion executes fn within a trace region if tracing is enabled for
// regionType.
func WithRegion(ctx context.Context, regionType string, fn func()) {
	if !enabledFor(regionType) {
		fn()
		return
	}
	trace.WithRegion(ctx, regionType, fn)
}

// Log adds a log event to the trace if tracing is enabled for category.
func Log(ctx context.Context, category, message string) {
	if !enabledFor(category) {
		return
	}
	trace.Log(ctx, category, message)
}

// Logf adds a formatted log event to the trace if tracing is enabled for
// category.
func Logf(ctx context.Context, category, format string, args ...any) {
	if !enabledFor(category) {
		return
	}
	trace.Logf(ctx, category, format, args...)
}

// LongLogf adds one or more log events to the trace if tracing is enabled for
// category, breaking the message up as needed to avoid truncation due to
// per-event size limit.
// See [MaxEventTrailerDataSize], defined to be 1<<10
// [MaxEventTrailerDataSize]: https://cs.opensource.google/go/go/+/master:src/internal/trace/tracev2/events.go;drc=6c3b5a2798c83d583cb37dba9f39c47300d19f1f;l=588
//
//nolint:lll // long url
func LongLogf(ctx context.Context, category, header, continuationHeader, trailer, format string, args ...any) {
	if !trace.IsEnabled() || !enabledFor(category) {
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

// NewTask creates a new task if tracing is enabled for taskType.
func NewTask(ctx context.Context, taskType string) (context.Context, Task) {
	if !enabledFor(taskType) {
		return ctx, noopTask
	}
	tCtx, t := trace.NewTask(ctx, taskType)
	return tCtx, Task{t: t}
}
