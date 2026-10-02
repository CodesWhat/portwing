package generic

// routes_mutant_test.go adds tests that target Gremlins mutants surviving in
// routes.go.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// lockedLogSink is a mutex-guarded slog sink. The default logger is
// process-wide, so a goroutine left over from an earlier test can still log
// while the test that swapped the logger reads the buffer back.
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

// captureLogs swaps the default logger for one that records into the returned
// sink, and restores the previous logger when the test ends. Callers must not
// run in parallel: the default logger is shared.
func captureLogs(t *testing.T) *lockedLogSink {
	t.Helper()

	sink := &lockedLogSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

// TestHandleContainersReportsOnlyAFailedEncode pins both sides of the
// `err != nil` guard around handleContainers' "failed to encode containers
// response" error line, killing the CONDITIONALS_NEGATION mutant at
// routes.go:14:55. The status line is already written by the time the encode
// can fail, so the log is the only trace of a response that never arrived:
// negated, a failed response goes unrecorded and every successful one is
// logged as an error. The existing test drives the failing writer but asserts
// only that the handler does not panic.
func TestHandleContainersReportsOnlyAFailedEncode(t *testing.T) {
	const encodeFailed = "failed to encode containers response"

	t.Run("successful response is not reported", func(t *testing.T) {
		client, _, shutdown := newTestDockerClient(t)
		defer shutdown()
		a := New(client, "test-agent")
		logs := captureLogs(t)

		rec := httptest.NewRecorder()
		a.handleContainers(rec, httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := logs.String(); strings.Contains(got, encodeFailed) {
			t.Fatalf("a response that encoded cleanly was reported as failed: %s", got)
		}
	})

	t.Run("failed write is reported with its error", func(t *testing.T) {
		client, _, shutdown := newTestDockerClient(t)
		defer shutdown()
		a := New(client, "test-agent")
		logs := captureLogs(t)

		a.handleContainers(&errorWriter{}, httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil))

		got := logs.String()
		if !strings.Contains(got, encodeFailed) {
			t.Fatalf("a response that failed to write left no %q record: %s", encodeFailed, got)
		}
		if !strings.Contains(got, "closed pipe") {
			t.Fatalf("the %q record does not carry the write error: %s", encodeFailed, got)
		}
	})
}
