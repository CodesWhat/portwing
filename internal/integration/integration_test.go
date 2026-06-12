//go:build integration

// Package integration runs the Lookout server against the runner's real
// Docker daemon. Each test starts a Lookout server on a random port with
// TOKEN auth pointing at /var/run/docker.sock (or the socket specified by
// LOOKOUT_TEST_DOCKER_SOCKET). Tests verify: health endpoints, auth
// enforcement, container list API, Prometheus metrics, MCP protocol, and
// the SSE events stream. A real alpine container is started and cleaned up
// so list assertions are non-trivial.
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	testToken  = "integration-test-token-abc123"
	startupMax = 10 * time.Second
	testImage  = "alpine:3.20"
)

// startServer launches lookout as a subprocess and returns its base URL.
// It blocks until the health endpoint returns 200 or the deadline is hit.
func startServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	dockerSocket := os.Getenv("LOOKOUT_TEST_DOCKER_SOCKET")
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}

	// Use os.MkdirTemp("", "lk") to stay within darwin's 104-byte unix socket path limit.
	tmpDir, err := os.MkdirTemp("", "lk")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	// Pick a random free TCP port via net.Listen so we don't need to know it
	// ahead of time — pass 0 and read the actual address back.
	// However, for simplicity here we use a fixed high port and rely on the CI
	// runner having it free.
	port := "19301"

	// Build the binary first (go build ./cmd/lookout -o <tmpdir>/lookout).
	binPath := tmpDir + "/lookout"
	build := exec.Command("go", "build", "-o", binPath, "./cmd/lookout")
	build.Dir = moduleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	env := append(os.Environ(),
		"TOKEN="+testToken,
		"PORT="+port,
		"BIND_ADDRESS=127.0.0.1",
		"DOCKER_SOCKET="+dockerSocket,
		"ADAPTER=drydock",
		"LOG_LEVEL=error",      // keep integration test output quiet
		"SKIP_DF_COLLECTION=1", // /proc/df not available in CI
		"DD_POLL_INTERVAL=1",   // refresh inventory every 1s so a freshly started container appears promptly (default is 300s)
	)

	cmd := exec.Command(binPath)
	cmd.Env = env
	cmd.Dir = tmpDir

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting lookout: %v", err)
	}

	base := "http://127.0.0.1:" + port

	// Wait for the server to become healthy.
	deadline := time.Now().Add(startupMax)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/_lookout/health") //nolint:noctx
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	return base, func() {
		cmd.Process.Kill()   //nolint:errcheck
		cmd.Wait()           //nolint:errcheck
		os.RemoveAll(tmpDir) //nolint:errcheck
	}
}

// moduleRoot walks up from this file's directory until it finds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

// startAlpineContainer pulls alpine (already pulled by CI step) and runs a
// sleep container so the container list is non-trivial. Returns the container ID.
func startAlpineContainer(t *testing.T) (id string, cleanupFn func()) {
	t.Helper()
	cmd := exec.Command("docker", "run", "-d", "--name", "lookout-integ-test", testImage, "sleep", "300")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	id = strings.TrimSpace(string(out))
	return id, func() {
		exec.Command("docker", "rm", "-f", id).Run() //nolint:errcheck
	}
}

// get performs an authenticated GET request.
func get(t *testing.T, base, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatalf("NewRequest GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// TestMain runs integration tests only when docker is available.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "docker not found in PATH; skipping integration tests")
		os.Exit(0)
	}
	socket := os.Getenv("LOOKOUT_TEST_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	if _, err := os.Stat(socket); err != nil {
		fmt.Fprintf(os.Stderr, "Docker socket %s not found; skipping integration tests\n", socket)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHealthEndpoint(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	resp, err := http.Get(base + "/_lookout/health") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status: got %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health response: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("health status field: got %q, want \"healthy\"", body["status"])
	}
}

func TestAuth401WithoutToken(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	// /api/containers requires auth.
	resp, err := http.Get(base + "/api/containers") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/containers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated request: got %d, want 401", resp.StatusCode)
	}
}

func TestContainerListWithAuth(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	// Start a real container so the list is non-trivial.
	ctrID, cleanupCtr := startAlpineContainer(t)
	defer cleanupCtr()

	// The drydock adapter serves /api/containers from a cached snapshot that a
	// background poller refreshes; the container we just started only becomes
	// visible after the next poll cycle. Poll with a deadline rather than relying
	// on a fixed delay, whose adequacy varies with load on CI runners.
	deadline := time.Now().Add(startupMax)
	var containers []map[string]interface{}
	var found bool
	for time.Now().Before(deadline) {
		resp := get(t, base, "/api/containers")
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET /api/containers: got %d, want 200\nbody: %s", resp.StatusCode, body)
		}
		containers = nil
		err := json.NewDecoder(resp.Body).Decode(&containers)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decoding containers: %v", err)
		}
		for _, c := range containers {
			if id, _ := c["id"].(string); id == ctrID {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// We must at least see the container we just started.
	if !found {
		t.Errorf("container %s not found in /api/containers within %s (saw %d containers)",
			ctrID[:min(12, len(ctrID))], startupMax, len(containers))
	}
}

func TestMetricsContainsBuildInfo(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	resp := get(t, base, "/_lookout/metrics")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /_lookout/metrics: got %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading metrics body: %v", err)
	}

	if !bytes.Contains(body, []byte("lookout_build_info")) {
		t.Errorf("metrics body does not contain lookout_build_info\nbody excerpt: %.500s", body)
	}
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	// initialize request.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`
	req, err := http.NewRequest(http.MethodPost, base+"/_lookout/mcp", strings.NewReader(initBody))
	if err != nil {
		t.Fatalf("NewRequest MCP: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /_lookout/mcp initialize: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("MCP initialize: got %d, want 200\nbody: %s", resp.StatusCode, body)
	}

	var initResp map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		t.Fatalf("decoding MCP initialize response: %v", err)
	}
	if string(initResp["jsonrpc"]) != `"2.0"` {
		t.Errorf("MCP initialize: jsonrpc = %s, want \"2.0\"", initResp["jsonrpc"])
	}

	// tools/list request.
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	req2, err := http.NewRequest(http.MethodPost, base+"/_lookout/mcp", strings.NewReader(listBody))
	if err != nil {
		t.Fatalf("NewRequest MCP tools/list: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+testToken)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST /_lookout/mcp tools/list: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("MCP tools/list: got %d, want 200\nbody: %s", resp2.StatusCode, body)
	}

	var listResp struct {
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&listResp); err != nil {
		t.Fatalf("decoding MCP tools/list response: %v", err)
	}
	if listResp.JSONRPC != "2.0" {
		t.Errorf("tools/list: jsonrpc = %q, want \"2.0\"", listResp.JSONRPC)
	}
	if len(listResp.Result.Tools) == 0 {
		t.Error("tools/list: expected at least one tool, got 0")
	}
}

func TestSSEEventsFirstEventIsAck(t *testing.T) {
	base, cleanup := startServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest SSE: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/events: got %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type: got %q, want text/event-stream", ct)
	}

	// The drydock SSE protocol frames every event as `data: <json>` with no
	// `event:` line; the discriminator is the JSON payload's "type" field
	// (so EventSource clients read JSON.parse(e.data).type). Accumulate each
	// event's data payload and inspect its type. Read until we see dd:ack and
	// dd:watcher-snapshot, or the context times out.
	scanner := bufio.NewScanner(resp.Body)
	// Watcher-snapshot payloads carry the full container inventory and can
	// exceed the scanner's default 64 KiB token cap on busy hosts.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var gotAck, gotSnapshot bool
	var data strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" && data.Len() > 0 {
			// Blank line = end of event: parse the accumulated data payload.
			var evt struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(data.String())), &evt); err == nil {
				switch evt.Type {
				case "dd:ack":
					gotAck = true
				case "dd:watcher-snapshot":
					gotSnapshot = true
				}
			}
			data.Reset()
		}
		if gotAck && gotSnapshot {
			break
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		t.Errorf("SSE scanner: %v", err)
	}

	if !gotAck {
		t.Error("SSE stream: did not receive dd:ack event")
	}
	if !gotSnapshot {
		t.Error("SSE stream: did not receive dd:watcher-snapshot event")
	}
}
