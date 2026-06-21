// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package leakguard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogLeakStructured(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	// Capture structured log output as JSON. The leak reporter runs on the GC
	// finalizer goroutine, so the buffer is written concurrently with the test
	// goroutine's reads below — guard it.
	var buf lockedBuffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldDefault)

	// Configure leakguard to use LogLeak with stack depth 5
	SetLeakReporter(LogLeak)
	SetStackDepth(5)

	// Create and leak a handle - line number captured for verification
	var creationLine int
	func() {
		r := &testResource{id: 99}
		_ = New[testResource, testTrait](r) // THIS LINE should appear in frame 0
		_, _, creationLine, _ = runtime.Caller(0)
		creationLine-- // We want the line before the call to runtime.Caller
		// Handle leaked - finalizer should detect it
	}()

	// Trigger finalizer
	for buf.Len() == 0 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}

	t.Logf("Structured log output:\n%s", buf.String())

	// Parse JSON output
	var logEntry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &logEntry))

	// Verify structured fields exist
	require.Equal(t, "Close() was not called on handle before finalization", logEntry["msg"])
	require.Regexp(t, `^[1-9][0-9]*$`, logEntry["handle_id"], "handle_id should be nonzero and in decimal format")
	require.Equal(t, "testResource(99)", logEntry["resource"])

	// Verify creation_stack exists and is structured
	creationStack, ok := logEntry["creation_stack"].(map[string]any)
	require.True(t, ok, "creation_stack should be an object, got: %T", logEntry["creation_stack"])
	require.GreaterOrEqual(t, len(creationStack), 2, "creation_stack should have at least 2 frames")

	// Extract frames in order
	var frames []any
	for key, value := range creationStack {
		idx, err := strconv.Atoi(key)
		require.NoError(t, err)
		if idx >= len(frames) {
			frames = slices.Grow(frames, idx+1)
			frames = frames[0 : idx+1]
		}
		frames[idx] = value
	}

	// Verify all frames have the expected structure
	for idx, frame := range frames {
		frame := frame.(map[string]any)
		require.NotEmpty(t, frame["function"])
		require.NotEmpty(t, frame["file"])
		require.Regexp(t, `^[0-9]+$`, frame["line"], "line should be in decimal format")
		require.Regexp(t, `^\+0x[0-9a-f]+$`, frame["offset"], "offset should be in +0xHEX format")
		if idx == 0 {
			// Verify frame 0 points to this test function at the New() call
			require.Equal(t, "github.com/petenewcomb/streampool/internal/leakguard.TestLogLeakStructured.func2",
				frame["function"], "frame 0 should be in TestLogLeakStructured.func2")
			require.Equal(t, "structured_log_test.go", filepath.Base(frame["file"].(string)),
				"frame 0 should be in structured_log_test.go")
			require.Equal(t, strconv.Itoa(creationLine), frame["line"], "frame 0 should be at New() call line")
		}
	}
}

// lockedBuffer is a goroutine-safe bytes.Buffer wrapper: the leak reporter
// (slog) writes from the finalizer goroutine while the test reads. Bytes
// returns a copy so callers can use it after releasing the lock.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.buf.Bytes())
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
