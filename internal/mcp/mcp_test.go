package mcp

import (
	"bytes"
	"context"
	"encoding/json"
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

// shortSocketPath returns a temp socket path short enough for the unix
// socket path limit (104 bytes on darwin).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// stubDocker starts a minimal stub Docker HTTP server on a Unix socket and
// returns a configured docker.Client and a shutdown func.
func stubDocker(t *testing.T) (*docker.Client, func()) {
	t.Helper()

	socketPath := shortSocketPath(t)
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

	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]docker.ContainerJSON{
			{
				ID:     "abc123",
				Names:  []string{"/web"},
				Image:  "nginx:latest",
				State:  "running",
				Status: "Up 5 minutes",
				Labels: map[string]string{"env": "prod"},
			},
		})
	})

	mux.HandleFunc("/v1.44/containers/abc123/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.ContainerInspect{
			ID:   "abc123",
			Name: "/web",
			State: docker.ContainerState{
				Status:  "running",
				Running: true,
			},
			Config: docker.ContainerConfig{
				Image: "nginx:latest",
				Env:   []string{"PATH=/usr/local/sbin", "HOME=/root"},
			},
			HostConfig: &docker.HostConfig{
				RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			},
			NetworkSettings: &docker.NetworkSettings{
				Networks: map[string]docker.NetworkEndpoint{
					"bridge": {IPAddress: "172.17.0.2"},
				},
			},
			Mounts: []docker.MountPoint{
				{Source: "/data", Destination: "/data", RW: true},
			},
		})
	})

	mux.HandleFunc("/v1.44/containers/abc123/logs", func(w http.ResponseWriter, r *http.Request) {
		// Write two Docker-multiplexed log frames (stdout).
		line := "hello from nginx\n"
		frame := make([]byte, 8+len(line))
		frame[0] = 1 // stdout
		frame[4] = 0
		frame[5] = 0
		frame[6] = 0
		frame[7] = byte(len(line))
		copy(frame[8:], line)
		_, _ = w.Write(frame)
	})

	mux.HandleFunc("/v1.44/containers/abc123/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docker.ContainerStatsResponse{})
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

func newTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	client, shutdown := stubDocker(t)
	collector := metrics.NewCollector("/tmp", true)
	return NewHandler(client, collector), shutdown
}

func postMCP(t *testing.T, h *Handler, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rr.Body.String())
	}
	return resp
}

// TestInitializeRoundTrip verifies the MCP initialization handshake.
func TestInitializeRoundTrip(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0.0.1"},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}

	if got, want := result["protocolVersion"], protocolVersion; got != want {
		t.Errorf("protocolVersion = %v, want %v", got, want)
	}

	caps, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities is not an object")
	}
	if _, hasTool := caps["tools"]; !hasTool {
		t.Error("capabilities.tools missing")
	}

	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("serverInfo is not an object")
	}
	if got, want := serverInfo["name"], "portwing"; got != want {
		t.Errorf("serverInfo.name = %v, want %v", got, want)
	}
}

// TestToolsListShape verifies all expected tools are present with required fields.
func TestToolsListShape(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object")
	}

	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools is not an array")
	}

	wantTools := map[string]bool{
		"list_containers":   false,
		"inspect_container": false,
		"container_logs":    false,
		"host_metrics":      false,
		"container_stats":   false,
	}

	for _, raw := range tools {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			t.Errorf("tool entry is not an object: %T", raw)
			continue
		}
		name, _ := tool["name"].(string)
		if _, expected := wantTools[name]; !expected {
			t.Errorf("unexpected tool: %q", name)
			continue
		}
		wantTools[name] = true

		if _, ok := tool["description"]; !ok {
			t.Errorf("tool %q missing description", name)
		}
		if _, ok := tool["inputSchema"]; !ok {
			t.Errorf("tool %q missing inputSchema", name)
		}
	}

	for name, found := range wantTools {
		if !found {
			t.Errorf("tool %q not found in tools/list", name)
		}
	}
}

// TestToolsCallListContainers verifies list_containers against the stub.
func TestToolsCallListContainers(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "list_containers",
			"arguments": map[string]interface{}{},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object")
	}

	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]interface{})
		if len(content) > 0 {
			msg, _ := content[0].(map[string]interface{})
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)

	var containers []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &containers); err != nil {
		t.Fatalf("unmarshal container list: %v\ntext: %s", err, text)
	}

	if len(containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(containers))
	}

	c := containers[0]
	if c["id"] != "abc123" {
		t.Errorf("id = %v, want abc123", c["id"])
	}
	if c["image"] != "nginx:latest" {
		t.Errorf("image = %v, want nginx:latest", c["image"])
	}
}

// TestUnknownMethodError verifies that unknown methods return JSON-RPC error -32601.
func TestUnknownMethodError(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "resources/list",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errMethodNotFound)
	}
}

// TestInspectContainerNoEnvLeak verifies that env values are never present in
// inspect_container output, only the count.
func TestInspectContainerNoEnvLeak(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "inspect_container",
			"arguments": map[string]interface{}{"id": "abc123"},
		},
	})

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("empty content")
	}
	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)

	// Env values from the stub are "PATH=/usr/local/sbin" and "HOME=/root".
	// Neither value should appear in the output.
	for _, forbidden := range []string{"/usr/local/sbin", "/root", "PATH=", "HOME="} {
		if strings.Contains(text, forbidden) {
			t.Errorf("env value leaked in inspect output: %q found in %s", forbidden, text)
		}
	}

	// The count (2) should be present.
	if !strings.Contains(text, "envCount") {
		t.Error("envCount missing from inspect output")
	}
}

// TestPing verifies the ping method returns an empty result object.
func TestPing(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "ping",
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// TestGetMethodNotAllowed verifies that GET requests to the MCP endpoint return 405.
func TestGetMethodNotAllowed(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	req := httptest.NewRequest(http.MethodGet, "/_portwing/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", rr.Code)
	}
}

// TestNotificationsInitialized verifies that notifications/initialized returns 202.
func TestNotificationsInitialized(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	if rr.Code != http.StatusAccepted {
		t.Errorf("status %d, want 202", rr.Code)
	}
}

// TestToolsCallContainerLogs verifies container_logs against the stub.
func TestToolsCallContainerLogs(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "container_logs",
			"arguments": map[string]interface{}{"id": "abc123", "tail": 50},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]interface{})
		if len(content) > 0 {
			msg, _ := content[0].(map[string]interface{})
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)

	var out map[string]interface{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal container_logs result: %v\ntext: %s", err, text)
	}
	if out["id"] != "abc123" {
		t.Errorf("id = %v, want abc123", out["id"])
	}
}

// TestToolsCallContainerStats verifies container_stats against the stub.
func TestToolsCallContainerStats(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "container_stats",
			"arguments": map[string]interface{}{"id": "abc123"},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]interface{})
		if len(content) > 0 {
			msg, _ := content[0].(map[string]interface{})
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)

	var out map[string]interface{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal container_stats result: %v\ntext: %s", err, text)
	}
	if out["id"] != "abc123" {
		t.Errorf("id = %v, want abc123", out["id"])
	}
}

// TestToolsCallHostMetrics verifies host_metrics returns a non-error response.
func TestToolsCallHostMetrics(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      8,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "host_metrics",
			"arguments": map[string]interface{}{},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not an object")
	}
	// host_metrics may return isError:true when /proc is unavailable (e.g. macOS CI).
	// We only assert that the response is a valid tools/call envelope.
	if _, hasContent := result["content"]; !hasContent {
		t.Error("host_metrics result missing content field")
	}
}

// TestDemuxLogs verifies that the Docker log multiplexing decoder works correctly.
func TestDemuxLogs(t *testing.T) {
	line1 := "hello stdout\n"
	line2 := "hello stderr\n"

	var buf bytes.Buffer
	writeFrame := func(streamType byte, s string) {
		hdr := make([]byte, 8)
		hdr[0] = streamType
		hdr[4] = 0
		hdr[5] = 0
		hdr[6] = 0
		hdr[7] = byte(len(s))
		buf.Write(hdr)
		buf.WriteString(s)
	}
	writeFrame(1, line1)
	writeFrame(2, line2)

	lines, err := demuxLogs(&buf)
	if err != nil {
		t.Fatalf("demuxLogs error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.HasPrefix(lines[0], "stdout:") {
		t.Errorf("line[0] = %q, want stdout: prefix", lines[0])
	}
	if !strings.HasPrefix(lines[1], "stderr:") {
		t.Errorf("line[1] = %q, want stderr: prefix", lines[1])
	}
}
