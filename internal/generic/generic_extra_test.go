package generic

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// ---------------------------------------------------------------------------
// errorWriter — an http.ResponseWriter whose Write always returns an error.
// Used to exercise the slog.Error branch in handleContainers.
// ---------------------------------------------------------------------------

type errorWriter struct {
	header http.Header
	code   int
}

func (e *errorWriter) Header() http.Header {
	if e.header == nil {
		e.header = make(http.Header)
	}
	return e.header
}

func (e *errorWriter) Write(_ []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (e *errorWriter) WriteHeader(code int) { e.code = code }

// TestHandleContainersEncodeError exercises the slog.Error path when json.Encode fails.
func TestHandleContainersEncodeError(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	// errorWriter.Write always fails — Encode will propagate the error.
	a.handleContainers(&errorWriter{}, req) // must not panic
}

// ---------------------------------------------------------------------------
// Docker stub that returns errors for the logs endpoint.
// ---------------------------------------------------------------------------

func newTestDockerClientWithLogsError(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			http.Error(w, "container not found", http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	return client, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	}
}

// TestHandleContainerLogsDockerError exercises the Docker error path (lines 39-42).
func TestHandleContainerLogsDockerError(t *testing.T) {
	t.Parallel()

	client, shutdown := newTestDockerClientWithLogsError(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/missing/logs", nil)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "getting logs") {
		t.Fatalf("expected 'getting logs' in error body, got: %q", rec.Body.String())
	}
}

// TestHandleContainerLogsFollow exercises the follow=true path (line 47).
func TestHandleContainerLogsFollow(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs?follow=1", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	// follow=true sets Transfer-Encoding: chunked.
	if te := rec.Header().Get("Transfer-Encoding"); te != "chunked" {
		t.Fatalf("Transfer-Encoding: got %q want chunked", te)
	}
}

// TestHandleContainerLogsFollowTrue exercises follow=true via "follow=true" string.
func TestHandleContainerLogsFollowTrue(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs?follow=true", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if te := rec.Header().Get("Transfer-Encoding"); te != "chunked" {
		t.Fatalf("Transfer-Encoding: got %q want chunked", te)
	}
}

// newTestDockerClientZeroFrame creates a stub that sends a frame with frameSize=0
// followed by a real frame, exercising the `frameSize == 0` continue branch.
func newTestDockerClientZeroFrame(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		// Zero-size frame first.
		zeroHdr := make([]byte, 8)
		zeroHdr[0] = 1
		binary.BigEndian.PutUint32(zeroHdr[4:8], 0)
		_, _ = w.Write(zeroHdr)
		// Then a real frame.
		payload := []byte("real line\n")
		realHdr := make([]byte, 8)
		realHdr[0] = 1
		binary.BigEndian.PutUint32(realHdr[4:8], uint32(len(payload)))
		_, _ = w.Write(realHdr)
		_, _ = w.Write(payload)
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	return client, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	}
}

// TestHandleContainerLogsZeroFrame exercises the frameSize==0 continue branch (line 64-65)
// and also the canFlush path (line 73-75) since httptest.Recorder implements Flusher.
func TestHandleContainerLogsZeroFrame(t *testing.T) {
	t.Parallel()

	client, shutdown := newTestDockerClientZeroFrame(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "real line") {
		t.Fatalf("expected 'real line' in body, got: %q", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// SSE / EventBroadcaster — data write-error path (lines 126-129)
// ---------------------------------------------------------------------------

// failAfterNWriter is a ResponseWriter+Flusher whose Write returns an error
// after n successful writes.
type failAfterNWriter struct {
	header  http.Header
	code    int
	n       int
	written int
	buf     bytes.Buffer
}

func (f *failAfterNWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failAfterNWriter) Write(p []byte) (int, error) {
	if f.written >= f.n {
		return 0, io.ErrClosedPipe
	}
	f.written++
	return f.buf.Write(p)
}

func (f *failAfterNWriter) WriteHeader(code int) { f.code = code }

func (f *failAfterNWriter) Flush() {} // implements http.Flusher

// TestServeHTTPDataWriteError exercises the fmt.Fprintf write-error path (lines 126-129).
// The failAfterNWriter fails on the first Write call, which is the "data: ...\n\n" line.
func TestServeHTTPDataWriteError(t *testing.T) {
	t.Parallel()

	client, shutdown := newTestDockerClientWithEvents(t)
	defer shutdown()

	b := NewEventBroadcaster(client)

	// n=0 means fail on the very first Write (the "data: ..." event line).
	w := &failAfterNWriter{n: 0}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.ServeHTTP(w, req)
	}()

	select {
	case <-done:
		// ServeHTTP exited because the Write failed.
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("ServeHTTP did not exit after write error")
	}
}

// TestHandleContainerLogsNonFlusher exercises the canFlush==false path.
// A non-flusher writer still gets frames written but Flush is not called.
func TestHandleContainerLogsNonFlusher(t *testing.T) {
	t.Parallel()

	client, _, shutdown := newTestDockerClient(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs?tail=10", nil)
	req.SetPathValue("id", "container-1")
	// strictNonFlusher does not implement http.Flusher.
	w := &strictNonFlusher{}

	a.handleContainerLogs(w, req)

	// No panic, and body has the log content.
	if !strings.Contains(w.body.String(), "log line") {
		t.Fatalf("expected log content, got: %q", w.body.String())
	}
}

// newTestDockerClientTruncatedFrame creates a stub that sends a header claiming
// frameSize=100 but only writes 5 bytes, causing io.CopyN to fail (routes.go:69-71).
func newTestDockerClientTruncatedFrame(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		// Disable buffering so the header arrives before we close.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		// Send a header claiming 100 bytes but only write 5.
		hdr := make([]byte, 8)
		hdr[0] = 1
		binary.BigEndian.PutUint32(hdr[4:8], 100)
		_, _ = w.Write(hdr)
		_, _ = w.Write([]byte("short")) // only 5 of 100 bytes
		// Connection closes here, causing CopyN to return io.ErrUnexpectedEOF.
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	return client, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	}
}

// TestHandleContainerLogsCopyNError exercises the CopyN error path (routes.go:69-71).
func TestHandleContainerLogsCopyNError(t *testing.T) {
	t.Parallel()

	client, shutdown := newTestDockerClientTruncatedFrame(t)
	defer shutdown()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	// Should return without panic when CopyN fails mid-frame.
	a.handleContainerLogs(rec, req)
}

// TestHandleContainerLogsPartialHeader exercises the io.ErrUnexpectedEOF path
// in the ReadFull call (routes.go:56-60): a partial 8-byte header causes the
// normal exit without a slog.Debug log.
func TestHandleContainerLogsPartialHeader(t *testing.T) {
	t.Parallel()

	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{Version: "26.0.0", APIVersion: "1.44"})
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		// Send one valid frame.
		payload := []byte("ok\n")
		hdr := make([]byte, 8)
		hdr[0] = 1
		binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
		_, _ = w.Write(hdr)
		_, _ = w.Write(payload)
		// Then send only 3 bytes of the next 8-byte header — causes ErrUnexpectedEOF.
		_, _ = w.Write([]byte{1, 0, 0})
		// Connection closes here.
	})

	server := &http.Server{Handler: mux}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		<-serverDone
	}()

	a := New(client, "test-agent")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	// Should have the valid frame's content.
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("expected 'ok' in body, got: %q", rec.Body.String())
	}
}
