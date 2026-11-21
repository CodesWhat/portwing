// SPDX-License-Identifier: MIT

package docker

import (
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newStallLogDaemon serves /version and a container-logs endpoint that runs
// logs. handlerDone is closed once per logs request, when its handler returns.
func newStallLogDaemon(t *testing.T, requestTimeout int, logs func(w http.ResponseWriter, r *http.Request)) (*Client, chan struct{}) {
	t.Helper()
	done := make(chan struct{}, 4)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Version":"26.0.0","ApiVersion":"1.44"}`)
			return
		}
		defer func() { done <- struct{}{} }()
		logs(w, r)
	}))
	c, err := NewClient(socketPath, requestTimeout)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, done
}

// Not parallel: it counts goroutines, which only means something while no
// other test is running.
func TestGetContainerLogsNonFollowStallIsBoundedAndReleased(t *testing.T) {
	c, done := newStallLogDaemon(t, 1, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = io.WriteString(w, "some bytes\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the daemon goes quiet
	})
	baseline := runtime.NumGoroutine()

	start := time.Now()
	rc, err := c.GetContainerLogs(t.Context(), "stall", "", "", "", false, false)
	if err != nil {
		t.Fatalf("GetContainerLogs: %v", err)
	}
	data, readErr := io.ReadAll(rc)
	elapsed := time.Since(start)
	_ = rc.Close()

	if string(data) != "some bytes\n" {
		t.Fatalf("data = %q, want the bytes sent before the stall", data)
	}
	if readErr == nil {
		t.Fatal("read ended cleanly, want a mid-body error so the log is not mistaken for complete")
	}
	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("read took %v, want about the 1s idle bound", elapsed)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon handler never saw the connection close")
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines = %d, want <= %d:\n%s", got, baseline, buf[:runtime.Stack(buf, true)])
	}
}

func TestGetContainerLogsNonFollowBoundsWaitForHeaders(t *testing.T) {
	t.Parallel()

	c, _ := newStallLogDaemon(t, 1, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	start := time.Now()
	_, err := c.GetContainerLogs(t.Context(), "stall", "", "", "", false, false)
	if err == nil {
		t.Fatal("GetContainerLogs returned no error for a daemon that never answers")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %v, want about the 1s idle bound", elapsed)
	}
}

func TestGetContainerLogsNonFollowKeepsReadingWhileBytesArrive(t *testing.T) {
	t.Parallel()

	c, _ := newStallLogDaemon(t, 1, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		for range 4 {
			_, _ = io.WriteString(w, "line\n")
			w.(http.Flusher).Flush()
			time.Sleep(550 * time.Millisecond)
		}
	})
	rc, err := c.GetContainerLogs(t.Context(), "slow", "", "", "", false, false)
	if err != nil {
		t.Fatalf("GetContainerLogs: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := strings.Repeat("line\n", 4); string(data) != want {
		t.Fatalf("data = %q, want %q", data, want)
	}
}

func TestGetContainerLogsFollowIdleIsNotBounded(t *testing.T) {
	t.Parallel()

	c, _ := newStallLogDaemon(t, 1, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = io.WriteString(w, "a\n")
		w.(http.Flusher).Flush()
		time.Sleep(1500 * time.Millisecond) // idle longer than REQUEST_TIMEOUT
		_, _ = io.WriteString(w, "b\n")
	})
	rc, err := c.GetContainerLogs(t.Context(), "follow", "", "", "", true, false)
	if err != nil {
		t.Fatalf("GetContainerLogs: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "a\nb\n" {
		t.Fatalf("data = %q, want both lines across the idle gap", data)
	}
}
