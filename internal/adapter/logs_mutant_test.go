package adapter

// logs_mutant_test.go adds tests that target Gremlins mutants surviving in
// logs.go.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// lockedLogSink is a mutex-guarded slog sink. The handler under test writes
// from the test goroutine, but the default logger is process-wide, so a fake
// daemon's serving goroutine left over from an earlier test can still log
// while this one reads the buffer back.
type lockedLogSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *lockedLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *lockedLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureDebugLogs swaps the default logger for one that records every level
// into the returned sink, and restores the previous logger when the test ends.
// Callers must not run in parallel: the default logger is shared.
func captureDebugLogs(t *testing.T) *lockedLogSink {
	t.Helper()

	sink := &lockedLogSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

// TestServeContainerLogsReportsOnlyAStreamThatEndedInError pins both sides of
// the `err != nil` guard around ServeContainerLogs' "log stream ended" debug
// line, killing the CONDITIONALS_NEGATION mutant at logs.go:96:9. The line is
// the only trace a stream that died mid-flight leaves, since the response
// status is already on the wire: negated, a failed stream goes unrecorded and
// every clean one is reported as ended with a nil error.
func TestServeContainerLogsReportsOnlyAStreamThatEndedInError(t *testing.T) {
	const streamEnded = "log stream ended"

	t.Run("clean end of stream is not reported", func(t *testing.T) {
		client, _, shutdown := newLogDaemonClient(t, http.StatusOK, dockerLogFrame(1, []byte("out\n")))
		defer shutdown()
		logs := captureDebugLogs(t)

		rec := httptest.NewRecorder()
		ServeContainerLogs(rec, logRequest("/logs"), ContainerLogOptions{Client: client})

		if got := rec.Body.String(); got != "out\n" {
			t.Fatalf("body = %q, want %q", got, "out\n")
		}
		if got := logs.String(); strings.Contains(got, streamEnded) {
			t.Fatalf("a stream that ended cleanly was reported as failed: %s", got)
		}
	})

	t.Run("failed write is reported with its error", func(t *testing.T) {
		client, _, shutdown := newLogDaemonClient(t, http.StatusOK, dockerLogFrame(1, []byte("out\n")))
		defer shutdown()
		logs := captureDebugLogs(t)

		w := &failingLogWriter{}
		ServeContainerLogs(w, logRequest("/logs"), ContainerLogOptions{Client: client})

		if w.attempts != 1 {
			t.Fatalf("write attempts = %d, want 1", w.attempts)
		}
		got := logs.String()
		if !strings.Contains(got, streamEnded) {
			t.Fatalf("a stream that failed mid-write left no %q record: %s", streamEnded, got)
		}
		if !strings.Contains(got, "closed pipe") {
			t.Fatalf("the %q record does not carry the write error: %s", streamEnded, got)
		}
	})
}
