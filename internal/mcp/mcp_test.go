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
	"reflect"
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
	return stubDockerWithLogs(t, mcpTestLogFrame(1, []byte("hello from nginx\n")))
}

func stubDockerWithLogs(t *testing.T, logs []byte) (*docker.Client, func()) {
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
		_, _ = w.Write(logs)
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

func mcpTestLogFrame(streamType byte, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = streamType
	frame[4] = byte(uint32(len(payload)) >> 24)
	frame[5] = byte(uint32(len(payload)) >> 16)
	frame[6] = byte(uint32(len(payload)) >> 8)
	frame[7] = byte(len(payload))
	copy(frame[8:], payload)
	return frame
}

func newTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	client, shutdown := stubDocker(t)
	collector := metrics.NewCollector("/tmp", true)
	return NewHandler(client, collector), shutdown
}

func postMCP(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
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

func postMCPRaw(h *Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(body))
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

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.0.1"},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}

	if got, want := result["protocolVersion"], protocolVersion; got != want {
		t.Errorf("protocolVersion = %v, want %v", got, want)
	}

	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities is not an object")
	}
	if _, hasTool := caps["tools"]; !hasTool {
		t.Error("capabilities.tools missing")
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
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

	rr := postMCP(t, h, map[string]any{
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

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}

	tools, ok := result["tools"].([]any)
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
		tool, ok := raw.(map[string]any)
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

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_containers",
			"arguments": map[string]any{},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}

	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]any)
		if len(content) > 0 {
			msg, _ := content[0].(map[string]any)
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)

	var containers []map[string]any
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

	rr := postMCP(t, h, map[string]any{
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

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "inspect_container",
			"arguments": map[string]any{"id": "abc123"},
		},
	})

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, _ := resp.Result.(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("empty content")
	}
	block, _ := content[0].(map[string]any)
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

	rr := postMCP(t, h, map[string]any{
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

// TestNotificationsInitialized verifies that notifications/initialized returns
// 202 with no response body.
func TestNotificationsInitialized(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	if rr.Code != http.StatusAccepted {
		t.Errorf("status %d, want 202", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rr.Body.String())
	}
}

func TestMCPRequestIDValidation(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name string
		id   string
	}{
		{name: "null", id: "null"},
		{name: "boolean", id: "true"},
		{name: "object", id: `{}`},
		{name: "array", id: `[]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postMCPRaw(h, `{"jsonrpc":"2.0","id":`+tt.id+`,"method":"ping"}`)
			resp := decodeResponse(t, rr)
			if resp.Error == nil || resp.Error.Code != errInvalidRequest {
				t.Fatalf("error = %+v, want code %d", resp.Error, errInvalidRequest)
			}
			if string(resp.ID) != "null" {
				t.Errorf("response id = %s, want null", resp.ID)
			}
		})
	}
}

func TestMCPRequestIDPreservesValidValues(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name string
		id   string
	}{
		{name: "escaped string", id: `"\u0026"`},
		{name: "large integer", id: "1e700"},
		{name: "integral decimal", id: "1.0"},
		{name: "fractional number", id: "1.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postMCPRaw(h, `{"jsonrpc":"2.0","id":`+tt.id+`,"method":"ping"}`)
			resp := decodeResponse(t, rr)
			if resp.Error != nil {
				t.Fatalf("unexpected error: %+v", resp.Error)
			}
			want, err := decodeJSONValue(json.RawMessage(tt.id))
			if err != nil {
				t.Fatalf("decode request id: %v", err)
			}
			got, err := decodeJSONValue(resp.ID)
			if err != nil {
				t.Fatalf("decode response id: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("response id = %s, want value of %s", resp.ID, tt.id)
			}
		})
	}
}

func TestMCPInvalidIDIsNullWhenVersionIsAlsoInvalid(t *testing.T) {
	h := NewHandler(nil, nil)
	rr := postMCPRaw(h, `{"jsonrpc":"1.0","id":{},"method":"ping"}`)
	resp := decodeResponse(t, rr)
	if resp.Error == nil || resp.Error.Code != errInvalidRequest {
		t.Fatalf("error = %+v, want code %d", resp.Error, errInvalidRequest)
	}
	if string(resp.ID) != "null" {
		t.Errorf("response id = %s, want null", resp.ID)
	}
}

func TestMCPRejectsMissingOrNonStringMethod(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"jsonrpc":"2.0"}`},
		{name: "null", body: `{"jsonrpc":"2.0","method":null}`},
		{name: "number", body: `{"jsonrpc":"2.0","method":7}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postMCPRaw(h, tt.body)
			resp := decodeResponse(t, rr)
			if resp.Error == nil || resp.Error.Code != errInvalidRequest {
				t.Fatalf("error = %+v, want code %d", resp.Error, errInvalidRequest)
			}
			if string(resp.ID) != "null" {
				t.Errorf("response id = %s, want null", resp.ID)
			}
		})
	}
}

func TestMCPAcceptsResponsesWithoutResponding(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name string
		body string
	}{
		{name: "result", body: `{"jsonrpc":"2.0","id":1,"result":{}}`},
		{name: "error", body: `{"jsonrpc":"2.0","id":"request-1","error":{"code":-32601,"message":"not found"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postMCPRaw(h, tt.body)
			if rr.Code != http.StatusAccepted {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusAccepted)
			}
			if rr.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", rr.Body.String())
			}
		})
	}
}

func TestMCPRejectsNonObjectParams(t *testing.T) {
	h := NewHandler(nil, nil)
	tests := []struct {
		name   string
		params string
	}{
		{name: "null", params: "null"},
		{name: "boolean", params: "true"},
		{name: "string", params: `"value"`},
		{name: "array", params: `[]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := postMCPRaw(h, `{"jsonrpc":"2.0","id":1,"method":"ping","params":`+tt.params+`}`)
			resp := decodeResponse(t, rr)
			if resp.Error == nil || resp.Error.Code != errInvalidParams {
				t.Fatalf("error = %+v, want code %d", resp.Error, errInvalidParams)
			}
		})
	}

	t.Run("notification uses HTTP rejection without response body", func(t *testing.T) {
		rr := postMCPRaw(h, `{"jsonrpc":"2.0","method":"ping","params":true}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rr.Body.String())
		}
	})
}

func TestMCPNotificationHandling(t *testing.T) {
	h := NewHandler(nil, nil)
	t.Run("request cannot use notification method", func(t *testing.T) {
		rr := postMCPRaw(h, `{"jsonrpc":"2.0","id":"request-1","method":"notifications/initialized"}`)
		resp := decodeResponse(t, rr)
		if resp.Error == nil || resp.Error.Code != errInvalidRequest {
			t.Fatalf("error = %+v, want code %d", resp.Error, errInvalidRequest)
		}
		if string(resp.ID) != `"request-1"` {
			t.Errorf("response id = %s, want %q", resp.ID, "request-1")
		}
	})

	t.Run("notification never receives response", func(t *testing.T) {
		rr := postMCPRaw(h, `{"jsonrpc":"2.0","method":"ping"}`)
		if rr.Code != http.StatusAccepted {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusAccepted)
		}
		if rr.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", rr.Body.String())
		}
	})
}

// TestToolsCallContainerLogs verifies container_logs against the stub.
func TestToolsCallContainerLogs(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "container_logs",
			"arguments": map[string]any{"id": "abc123", "tail": 50},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]any)
		if len(content) > 0 {
			msg, _ := content[0].(map[string]any)
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)

	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal container_logs result: %v\ntext: %s", err, text)
	}
	if out["id"] != "abc123" {
		t.Errorf("id = %v, want abc123", out["id"])
	}
}

func TestToolsCallContainerLogsDecodesRawAndMultiplexedStreams(t *testing.T) {
	stdout := mcpTestLogFrame(1, []byte("stdout line\n"))
	stderr := mcpTestLogFrame(2, []byte("stderr line\n"))
	tests := []struct {
		name      string
		body      []byte
		wantLines []string
	}{
		{name: "short raw TTY", body: []byte("tty\n"), wantLines: []string{"stdout: tty"}},
		{
			name:      "multiplexed stdout and stderr",
			body:      append(stdout, stderr...),
			wantLines: []string{"stdout: stdout line", "stderr: stderr line"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, shutdown := stubDockerWithLogs(t, tt.body)
			defer shutdown()
			h := NewHandler(client, nil)

			rr := postMCP(t, h, map[string]any{
				"jsonrpc": "2.0",
				"id":      6,
				"method":  "tools/call",
				"params": map[string]any{
					"name":      "container_logs",
					"arguments": map[string]any{"id": "abc123", "tail": 50},
				},
			})

			resp := decodeResponse(t, rr)
			result, ok := resp.Result.(map[string]any)
			if !ok {
				t.Fatalf("result type = %T, want object", resp.Result)
			}
			if isErr, _ := result["isError"].(bool); isErr {
				t.Fatalf("container_logs returned an error: %+v", result)
			}
			content, ok := result["content"].([]any)
			if !ok || len(content) != 1 {
				t.Fatalf("content = %+v, want one block", result["content"])
			}
			block, ok := content[0].(map[string]any)
			if !ok {
				t.Fatalf("content block type = %T, want object", content[0])
			}
			text, ok := block["text"].(string)
			if !ok {
				t.Fatalf("content text type = %T, want string", block["text"])
			}
			var output struct {
				Lines []string `json:"lines"`
			}
			if err := json.Unmarshal([]byte(text), &output); err != nil {
				t.Fatalf("decode tool output: %v", err)
			}
			if got, want := strings.Join(output.Lines, "\n"), strings.Join(tt.wantLines, "\n"); got != want {
				t.Fatalf("lines = %q, want %q", output.Lines, tt.wantLines)
			}
		})
	}
}

// TestToolsCallContainerStats verifies container_stats against the stub.
func TestToolsCallContainerStats(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "container_stats",
			"arguments": map[string]any{"id": "abc123"},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		content, _ := result["content"].([]any)
		if len(content) > 0 {
			msg, _ := content[0].(map[string]any)
			t.Fatalf("tool returned error: %v", msg["text"])
		}
		t.Fatal("tool returned error (no content)")
	}

	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content is empty")
	}

	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)

	var out map[string]any
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

	rr := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      8,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "host_metrics",
			"arguments": map[string]any{},
		},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
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

	lines, err := decodeContainerLogLines(&buf)
	if err != nil {
		t.Fatalf("decodeContainerLogLines error: %v", err)
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

func TestDecodeContainerLogLinesFlushesPartialLineWhenStreamChanges(t *testing.T) {
	t.Parallel()

	input := append(
		mcpTestLogFrame(1, []byte("partial stdout")),
		mcpTestLogFrame(2, []byte("stderr line\n"))...,
	)
	lines, err := decodeContainerLogLines(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("decodeContainerLogLines: %v", err)
	}
	want := []string{"stdout: partial stdout", "stderr: stderr line"}
	if got := strings.Join(lines, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

type fragmentedLogReader struct {
	reader *bytes.Reader
	max    int
}

func (r *fragmentedLogReader) Read(p []byte) (int, error) {
	if len(p) > r.max {
		p = p[:r.max]
	}
	return r.reader.Read(p)
}

func TestDecodeContainerLogLinesPreservesLinesAcrossReadBoundaries(t *testing.T) {
	t.Parallel()

	longLine := strings.Repeat("x", 40<<10)
	stdout := mcpTestLogFrame(1, []byte("stdout line\n"))
	stderr := mcpTestLogFrame(2, []byte("stderr line\n"))
	tests := []struct {
		name      string
		body      []byte
		fragment  int
		wantLines []string
	}{
		{
			name:      "long raw line",
			body:      []byte(longLine),
			fragment:  3,
			wantLines: []string{"stdout: " + longLine},
		},
		{
			name:      "fragmented multiplexed headers and payloads",
			body:      append(stdout, stderr...),
			fragment:  1,
			wantLines: []string{"stdout: stdout line", "stderr: stderr line"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &fragmentedLogReader{reader: bytes.NewReader(tt.body), max: tt.fragment}
			lines, err := decodeContainerLogLines(reader)
			if err != nil {
				t.Fatalf("decodeContainerLogLines: %v", err)
			}
			if got, want := strings.Join(lines, "\n"), strings.Join(tt.wantLines, "\n"); got != want {
				t.Fatalf("lines = %q, want %q", lines, tt.wantLines)
			}
		})
	}
}

// TestResponseIDMatchesRequestID verifies that every response carries the
// same id as the request that produced it, byte-for-byte, for both a
// numeric and a string id. Nothing else in this package checks id
// correlation — the fuzz property only checks jsonrpc=="2.0" — so a bug that
// swapped or dropped the id (breaking a client's ability to match responses
// to in-flight requests) could ship unnoticed.
func TestResponseIDMatchesRequestID(t *testing.T) {
	h, shutdown := newTestHandler(t)
	defer shutdown()

	for _, tc := range []struct {
		name string
		id   any
	}{
		{"numeric id", 42},
		{"string id", "req-abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := postMCP(t, h, map[string]any{
				"jsonrpc": "2.0",
				"id":      tc.id,
				"method":  "ping",
			})

			wantID, err := json.Marshal(tc.id)
			if err != nil {
				t.Fatalf("marshal want id: %v", err)
			}

			resp := decodeResponse(t, rr)
			if resp.Error != nil {
				t.Fatalf("unexpected error: %+v", resp.Error)
			}
			if !bytes.Equal(resp.ID, wantID) {
				t.Errorf("response id = %s, want %s", resp.ID, wantID)
			}
		})
	}
}
