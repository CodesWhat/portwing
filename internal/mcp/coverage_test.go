package mcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/metrics"
)

// ---- ServeHTTP non-POST/non-GET (405) -----------------------------------

func TestServeHTTP_PutMethodNotAllowed(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	req := httptest.NewRequest(http.MethodPut, "/_portwing/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: want 405, got %d", rr.Code)
	}
}

func TestServeHTTP_DeleteMethodNotAllowed(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	req := httptest.NewRequest(http.MethodDelete, "/_portwing/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: want 405, got %d", rr.Code)
	}
}

// ---- ServeHTTP body read error -------------------------------------------

// errReader is an io.Reader that always returns an error.
type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

func TestServeHTTP_BodyReadError(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp",
		errReader{err: io.ErrUnexpectedEOF})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Should return 200 with a JSON-RPC error (internal error).
	if rr.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rr.Code)
	}
	var resp rpcResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error in response")
	}
	if resp.Error.Code != errInternalError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errInternalError)
	}
}

// ---- tools/call with invalid params (string instead of object) ----------

func TestHandleToolsCall_InvalidParamsType(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	// Send params as a JSON string — json.Unmarshal into the params struct fails.
	// Use raw JSON to avoid map[string]any encoding it as null.
	b := []byte(`{"jsonrpc":"2.0","id":20,"method":"tools/call","params":"not-an-object"}`)
	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	resp := decodeResponse(t, rr)
	if resp.Error == nil {
		t.Fatal("expected error for non-object params")
	}
	if resp.Error.Code != errInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errInvalidParams)
	}
}

// ---- Null-docker-client paths for each tool ----------------------------

// newNilDockerHandler returns a handler with a nil docker client (but real collector).
func newNilDockerHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{docker: nil, collector: metrics.NewCollector("/tmp", true)}
}

func TestToolListContainers_NilDocker(t *testing.T) {
	h := newNilDockerHandler(t)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      30,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_containers", "arguments": map[string]any{}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for nil docker client")
	}
	text := extractTextContent(t, result)
	if !strings.Contains(text, "docker client not available") {
		t.Errorf("expected 'docker client not available', got: %s", text)
	}
}

func TestToolInspectContainer_NilDocker(t *testing.T) {
	h := newNilDockerHandler(t)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      31,
		"method":  "tools/call",
		"params":  map[string]any{"name": "inspect_container", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for nil docker client")
	}
}

func TestToolContainerLogs_NilDocker(t *testing.T) {
	h := newNilDockerHandler(t)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      32,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for nil docker client")
	}
}

func TestToolContainerStats_NilDocker(t *testing.T) {
	h := newNilDockerHandler(t)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      33,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_stats", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for nil docker client")
	}
}

func TestToolHostMetrics_NilCollector(t *testing.T) {
	h := &Handler{docker: nil, collector: nil}

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      34,
		"method":  "tools/call",
		"params":  map[string]any{"name": "host_metrics", "arguments": map[string]any{}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for nil collector")
	}
	text := extractTextContent(t, result)
	if !strings.Contains(text, "metrics collector not available") {
		t.Errorf("expected 'metrics collector not available', got: %s", text)
	}
}

// ---- Tool error paths via stub returning errors --------------------------

// errorStubDocker starts a stub Docker server that returns HTTP errors for
// container operations.
func errorStubDocker(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	// All container operations return 500.
	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	mux.HandleFunc("/v1.44/containers/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = listener.Close()
		<-done
	}
	return client, shutdown
}

func newErrorHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	client, shutdown := errorStubDocker(t)
	collector := metrics.NewCollector("/tmp", true)
	return NewHandler(client, collector), shutdown
}

func TestToolListContainers_DockerError(t *testing.T) {
	h, shutdown := newErrorHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      40,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_containers", "arguments": map[string]any{}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true on docker error")
	}
}

func TestToolInspectContainer_DockerError(t *testing.T) {
	h, shutdown := newErrorHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      41,
		"method":  "tools/call",
		"params":  map[string]any{"name": "inspect_container", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true on docker error")
	}
}

func TestToolContainerLogs_DockerError(t *testing.T) {
	h, shutdown := newErrorHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true on docker error")
	}
}

func TestToolContainerStats_DockerError(t *testing.T) {
	h, shutdown := newErrorHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      43,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_stats", "arguments": map[string]any{"id": "abc"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true on docker error")
	}
}

// ---- tools/call with missing/empty id fields ----------------------------

func TestToolInspectContainer_EmptyID(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	// Empty id field.
	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      50,
		"method":  "tools/call",
		"params":  map[string]any{"name": "inspect_container", "arguments": map[string]any{"id": ""}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for empty id")
	}
	text := extractTextContent(t, result)
	if !strings.Contains(text, "id is required") {
		t.Errorf("expected 'id is required', got: %s", text)
	}
}

func TestToolContainerLogs_EmptyID(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      51,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": ""}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for empty id")
	}
}

func TestToolContainerStats_EmptyID(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      52,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_stats", "arguments": map[string]any{"id": ""}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true for empty id")
	}
}

// ---- container_logs tail clamping ---------------------------------------

func TestToolContainerLogs_DefaultTail(t *testing.T) {
	// tail == 0 → default to 100.
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      53,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "abc123"}},
	})

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestToolContainerLogs_TailOver500(t *testing.T) {
	// tail > 500 → capped to 500.
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      54,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "abc123", "tail": 1000}},
	})

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---- container_stats with non-empty Networks ----------------------------

// statsWithNetworkStub starts a stub that returns a ContainerStatsResponse
// with populated Networks.
func statsWithNetworkStub(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	mux.HandleFunc("/v1.44/containers/net-test/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Use raw JSON to populate the anonymous Networks struct.
		payload := `{"networks":{"eth0":{"rx_bytes":1234,"tx_bytes":5678},"lo":{"rx_bytes":100,"tx_bytes":200}}}`
		_, _ = w.Write([]byte(payload))
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = listener.Close()
		<-done
	}
	return client, shutdown
}

func TestToolContainerStats_WithNetworks(t *testing.T) {
	client, shutdown := statsWithNetworkStub(t)
	defer shutdown()
	collector := metrics.NewCollector("/tmp", true)
	h := NewHandler(client, collector)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      60,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_stats", "arguments": map[string]any{"id": "net-test"}},
	})

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := extractToolResult(t, resp)
	text := extractTextContent(t, result)
	if !strings.Contains(text, "rxBytes") && !strings.Contains(text, "networks") {
		t.Errorf("expected network data in response, got: %s", text)
	}
}

// ---- demuxLogs edge cases ------------------------------------------------

func TestDemuxLogs_ZeroSizeFrame(t *testing.T) {
	// A frame with size=0 should be skipped (continue).
	var buf bytes.Buffer

	// Write a zero-size frame.
	hdr := make([]byte, 8)
	hdr[0] = 1 // stdout
	hdr[4] = 0
	hdr[5] = 0
	hdr[6] = 0
	hdr[7] = 0 // size = 0
	buf.Write(hdr)

	// Write a normal frame after.
	line := "after-zero\n"
	hdr2 := make([]byte, 8)
	hdr2[0] = 1
	binary.BigEndian.PutUint32(hdr2[4:8], uint32(len(line)))
	buf.Write(hdr2)
	buf.WriteString(line)

	lines, err := demuxLogs(&buf)
	if err != nil {
		t.Fatalf("demuxLogs error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (zero-size skipped), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "after-zero") {
		t.Errorf("unexpected line content: %q", lines[0])
	}
}

func TestDemuxLogs_NonEOFReadError(t *testing.T) {
	// An error reading the header (not EOF) should return an error.
	reader := &failAfterNBytesReader{
		data: []byte("only7by"), // only 7 bytes, not enough for 8-byte header
		err:  io.ErrNoProgress,
	}

	_, err := demuxLogs(reader)
	if err == nil {
		t.Fatal("expected error for failed header read")
	}
}

// failAfterNBytesReader returns data then a specific error.
type failAfterNBytesReader struct {
	data []byte
	pos  int
	err  error
}

func (r *failAfterNBytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestDemuxLogs_OversizedFrame(t *testing.T) {
	// A frame larger than maxLogFrameSize should be skipped.
	var buf bytes.Buffer

	// Write oversized frame header (size > 256KiB).
	oversizeBytes := uint32(maxLogFrameSize + 1)
	hdr := make([]byte, 8)
	hdr[0] = 1
	binary.BigEndian.PutUint32(hdr[4:8], oversizeBytes)
	buf.Write(hdr)

	// Write the actual payload (pretend it's the oversized frame data).
	oversizeData := make([]byte, oversizeBytes)
	buf.Write(oversizeData)

	// Write a normal frame after the oversized one.
	line := "after-oversize\n"
	hdr2 := make([]byte, 8)
	hdr2[0] = 1
	binary.BigEndian.PutUint32(hdr2[4:8], uint32(len(line)))
	buf.Write(hdr2)
	buf.WriteString(line)

	lines, err := demuxLogs(&buf)
	if err != nil {
		t.Fatalf("demuxLogs error for oversized frame: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after oversized frame skip, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "after-oversize") {
		t.Errorf("unexpected line content: %q", lines[0])
	}
}

func TestDemuxLogs_OversizedFrameCopyError(t *testing.T) {
	// An oversized frame where the skip itself fails.
	var buf bytes.Buffer

	// Write oversized frame header but not enough bytes to skip.
	oversizeBytes := uint32(maxLogFrameSize + 1)
	hdr := make([]byte, 8)
	hdr[0] = 1
	binary.BigEndian.PutUint32(hdr[4:8], oversizeBytes)
	buf.Write(hdr)
	// Write only 10 bytes of data — not enough to skip the full frame.
	buf.Write(make([]byte, 10))

	_, err := demuxLogs(&buf)
	if err == nil {
		t.Fatal("expected error when oversized frame data is truncated")
	}
	if !strings.Contains(err.Error(), "skipping oversized log frame") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDemuxLogs_PayloadReadError(t *testing.T) {
	// Payload read failure: header says N bytes but only have fewer.
	var buf bytes.Buffer

	// Write header claiming 100 bytes.
	hdr := make([]byte, 8)
	hdr[0] = 1
	binary.BigEndian.PutUint32(hdr[4:8], 100)
	buf.Write(hdr)
	// Write only 50 bytes.
	buf.Write(make([]byte, 50))

	_, err := demuxLogs(&buf)
	if err == nil {
		t.Fatal("expected error when payload is truncated")
	}
}

// ---- container_logs with demuxLogs error --------------------------------

// demuxErrorStub returns a Docker stub that sends a broken log stream.
func demuxErrorStub(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.VersionResponse{
			Version:    "26.0.0",
			APIVersion: "1.44",
		})
	})
	mux.HandleFunc("/v1.44/containers/broken/logs", func(w http.ResponseWriter, r *http.Request) {
		// Write a header claiming 100 bytes but then close — causes io.ReadFull error
		// in demuxLogs for the payload read.
		hdr := make([]byte, 8)
		hdr[0] = 1
		binary.BigEndian.PutUint32(hdr[4:8], 100)
		_, _ = w.Write(hdr)
		// Write only 10 bytes then end response.
		_, _ = w.Write(make([]byte, 10))
	})

	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(listener)
	}()

	client, err := docker.NewClient(socketPath, 2)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = listener.Close()
		<-done
	}
	return client, shutdown
}

func TestToolContainerLogs_DemuxError(t *testing.T) {
	client, shutdown := demuxErrorStub(t)
	defer shutdown()
	collector := metrics.NewCollector("/tmp", true)
	h := NewHandler(client, collector)

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      70,
		"method":  "tools/call",
		"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "broken"}},
	})

	result := extractToolResult(t, decodeResponse(t, rr))
	if !result["isError"].(bool) {
		t.Error("expected isError=true when demux fails")
	}
}

// ---- unknown tool name --------------------------------------------------

func TestHandleToolsCall_UnknownTool(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      80,
		"method":  "tools/call",
		"params":  map[string]any{"name": "unknown_tool", "arguments": map[string]any{}},
	})

	resp := decodeResponse(t, rr)
	// unknown tool returns a JSON-RPC error (errInvalidParams), not a tool-level isError.
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for unknown tool name")
	}
	if resp.Error.Code != errInvalidParams {
		t.Errorf("error code = %d, want %d (-32602)", resp.Error.Code, errInvalidParams)
	}
	if !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Errorf("expected 'unknown tool' in error message, got: %s", resp.Error.Message)
	}
}

// ---- JSONRPC version check ----------------------------------------------

func TestServeHTTP_BadJSONRPCVersion(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "1.0",
		"id":      90,
		"method":  "ping",
	})

	resp := decodeResponse(t, rr)
	if resp.Error == nil {
		t.Fatal("expected error for jsonrpc != 2.0")
	}
	if resp.Error.Code != errInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errInvalidRequest)
	}
}

// ---- helpers ------------------------------------------------------------

func extractToolResult(t *testing.T, resp rpcResponse) map[string]any {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}
	return result
}

func extractTextContent(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content is empty or not an array")
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] is not an object")
	}
	text, _ := block["text"].(string)
	return text
}
