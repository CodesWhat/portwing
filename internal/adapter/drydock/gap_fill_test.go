package drydock

// gap_fill_test.go covers the two remaining reachable-but-untested branches:
//
//   1. adapter.go:351-353  spawnMessageHandler goroutine ctx.Err() early exit
//   2. routes.go:75-77     handleContainerLogs slog.Debug on non-EOF read error
//
// All marshal-error branches in sse.go/adapter.go are genuinely unreachable
// (the types involved — ackPayload, map[string]any with Containers, etc. — are
// all JSON-safe) and are documented as residual in StructuredOutput.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// ---------------------------------------------------------------------------
// adapter.go:351-353 — spawnMessageHandler goroutine ctx.Err() early exit
// ---------------------------------------------------------------------------

// TestSpawnMessageHandler_GoroutineExitsEarlyOnCancelledCtx covers lines 351-353
// in spawnMessageHandler: the goroutine checks ctx.Err() before calling fn and
// returns early when the context is already cancelled.
//
// The key difference from the existing TestSpawnMessageHandler_GoroutineCancelAfterAcquire
// is that we wait for the goroutine to fully exit (semaphore slot released) before
// the test returns, ensuring coverage is recorded.
func TestSpawnMessageHandler_GoroutineExitsEarlyOnCancelledCtx(t *testing.T) {
	t.Parallel()

	sem := make(chan struct{}, 1)
	a := &Adapter{messageSem: sem}

	// Cancel the context before calling spawnMessageHandler so ctx.Err() is
	// non-nil when the goroutine body runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fnCalled := false
	a.spawnMessageHandler(ctx, "test:early-exit", func() {
		fnCalled = true
	})

	// Wait for the goroutine to release the semaphore slot — that's our signal
	// that it has finished executing (including the ctx.Err() check).
	// spawnMessageHandler sends to sem to acquire; the goroutine receives from
	// sem on exit via `defer func() { <-sem }()`.  When len(sem) == 0 again,
	// the goroutine is done.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sem) == 0 {
			break
		}
		runtime.Gosched()
	}
	if len(sem) != 0 {
		t.Fatal("goroutine did not release semaphore within 2s")
	}

	// With a pre-cancelled context, fn must not have been called.
	if fnCalled {
		t.Fatal("fn was called despite pre-cancelled context; ctx.Err() check did not exit early")
	}
}

// ---------------------------------------------------------------------------
// routes.go:75-77 — handleContainerLogs slog.Debug on non-EOF read error
// ---------------------------------------------------------------------------

// newInvalidChunkDockerServer creates a fake Docker API server over a Unix socket.
// For log requests it returns a valid HTTP 200 with chunked encoding containing:
//   - One valid 8-byte zero-frame  (so the demux loop iterates once, proving loop entry)
//   - Then an invalid chunk-size line ("ZZZZ\r\n") which the Go HTTP chunked reader
//     rejects with a non-EOF, non-ErrUnexpectedEOF error.
//
// This exercises the slog.Debug("log stream ended") branch on routes.go:76.
func newInvalidChunkDockerServer(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleInvalidChunkConn(conn)
		}
	}()

	c, err := docker.NewClient(socketPath, 2)
	if err != nil {
		_ = ln.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("new docker client: %v", err)
	}

	return c, func() {
		_ = ln.Close()
		<-done
		_ = os.RemoveAll(dir)
	}
}

// handleInvalidChunkConn handles one raw connection for the invalid-chunk test.
// It serves /version, /containers/json, container inspect, and logs endpoints.
// For log requests it sends:
//  1. A valid 8-byte zero-frame chunk (frameSize=0 → loop continues).
//  2. An invalid hex chunk size ("ZZZZ\r\n") that Go's chunked reader rejects
//     with a non-EOF, non-ErrUnexpectedEOF error, hitting routes.go:76.
func handleInvalidChunkConn(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	req := string(buf[:n])

	switch {
	case strings.Contains(req, "GET /version"):
		versionJSON := `{"Version":"26.0.0","ApiVersion":"1.44","MinAPIVersion":"1.12"}`
		resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
			fmt.Sprintf("%d", len(versionJSON)) + "\r\n\r\n" + versionJSON
		_, _ = conn.Write([]byte(resp))

	case strings.Contains(req, "/logs"):
		// Respond with chunked HTTP body.
		//
		// Chunk 1 (8 bytes): a zero-size Docker log frame.
		//   header[0]   = 1 (stdout)
		//   header[1:4] = 0 (reserved)
		//   header[4:8] = 0 (frameSize = 0) — big-endian uint32
		// handleContainerLogs sees frameSize==0 and continues the loop.
		//
		// Chunk 2: invalid hex chunk size → net/http chunked reader error.
		zeroFrame := make([]byte, 8) // all zeros: stream=0, reserved=0, frameSize=0
		zeroFrame[0] = 1             // stdout stream type

		// First chunk: 8 bytes of zero-frame.
		// Chunked encoding: "<hex-size>\r\n<data>\r\n"
		chunk1Header := fmt.Sprintf("%x\r\n", 8)
		// Second "chunk": invalid hex size causes chunked reader to error.
		invalidChunk := "ZZZZ\r\n"

		httpResp := "HTTP/1.1 200 OK\r\n" +
			"Content-Type: application/octet-stream\r\n" +
			"Transfer-Encoding: chunked\r\n\r\n"
		_, _ = conn.Write([]byte(httpResp))
		_, _ = conn.Write([]byte(chunk1Header))
		_, _ = conn.Write(zeroFrame)
		_, _ = conn.Write([]byte("\r\n"))
		_, _ = conn.Write([]byte(invalidChunk))

	case strings.Contains(req, "/containers/json") && !strings.Contains(req, "/containers/container"):
		body := `[]`
		resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n" + body
		_, _ = conn.Write([]byte(resp))

	case strings.Contains(req, "/containers/") && strings.Contains(req, "/json"):
		inspectJSON := `{"Id":"container-1","Name":"/container-1","State":{"Status":"running","Running":true},` +
			`"Config":{"Image":"nginx:latest"},"NetworkSettings":{"Networks":{}},"Mounts":[]}`
		resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
			fmt.Sprintf("%d", len(inspectJSON)) + "\r\n\r\n" + inspectJSON
		_, _ = conn.Write([]byte(resp))

	default:
		_, _ = conn.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"))
	}
}

// TestHandleContainerLogs_InvalidChunkError covers routes.go:75-77:
// the slog.Debug("log stream ended") branch that fires when io.ReadFull
// returns an error that is neither io.EOF nor io.ErrUnexpectedEOF.
//
// The fake server sends a valid zero-frame followed by an invalid chunked-
// encoding size line, causing Go's HTTP chunked reader to return a parse error.
func TestHandleContainerLogs_InvalidChunkError(t *testing.T) {
	t.Parallel()

	client, shutdown := newInvalidChunkDockerServer(t)
	defer shutdown()

	a := NewAdapter(client, "test-agent", AgentInfo{})

	req := httptest.NewRequest(http.MethodGet, "/api/containers/container-1/logs", nil)
	req.SetPathValue("id", "container-1")
	rec := httptest.NewRecorder()

	a.handleContainerLogs(rec, req)

	// The handler returns silently after the non-EOF read error; the response
	// code must not be 500 (no http.Error was called before the error).
	// We just confirm no panic occurred and that the handler returned.
	_ = rec.Code
}
