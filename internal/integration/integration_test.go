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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return startServerWithEnv(t, nil, testToken)
}

// startServerWithEnv launches lookout with extra env vars appended and a
// specific bearer token. Pass extraEnv as nil and token as testToken for the
// standard TOKEN-auth harness; pass token as "" to start without TOKEN (e.g.
// for Ed25519-only auth via an AUTHORIZED_KEYS entry in extraEnv). An ephemeral
// port is obtained via net.Listen(:0) to avoid port conflicts.
func startServerWithEnv(t *testing.T, extraEnv []string, token string) (baseURL string, cleanup func()) {
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

	// Obtain an ephemeral port by binding on :0 and reading the resolved
	// address, then close the listener so lookout can bind the same port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen :0: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	// Build the binary first (go build ./cmd/lookout -o <tmpdir>/lookout).
	binPath := tmpDir + "/lookout"
	build := exec.Command("go", "build", "-o", binPath, "./cmd/lookout")
	build.Dir = moduleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	env := append(os.Environ(),
		"PORT="+port,
		"BIND_ADDRESS=127.0.0.1",
		"DOCKER_SOCKET="+dockerSocket,
		"ADAPTER=drydock",
		"LOG_LEVEL=error",      // keep integration test output quiet
		"SKIP_DF_COLLECTION=1", // /proc/df not available in CI
		"DD_POLL_INTERVAL=1",   // refresh inventory every 1s so a freshly started container appears promptly (default is 300s)
	)
	if token != "" {
		env = append(env, "TOKEN="+token)
	}
	env = append(env, extraEnv...)

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

// TestEd25519Auth verifies that lookout enforces Ed25519 signature auth on a
// protected endpoint when started with AUTHORIZED_KEYS and no TOKEN: an
// unsigned request is rejected with 401, and a properly signed request is
// accepted with 200. It targets /_lookout/info (an auth-gated endpoint) rather
// than /_lookout/health (which is intentionally unauthenticated), so the
// signature path is actually exercised.
func TestEd25519Auth(t *testing.T) {
	// Generate a fresh Ed25519 keypair for this test.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}

	// Write the public key to a temp authorized_keys file (mode 0600 so the
	// world-readable check in parseAuthorizedKeys passes). Format matches
	// parseKeyLine: "ed25519 <base64-std-pubkey> <comment>".
	keysDir, err := os.MkdirTemp("", "lk-keys")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(keysDir)

	b64 := base64.StdEncoding.EncodeToString(pub)
	keysPath := filepath.Join(keysDir, "authorized_keys")
	if err := os.WriteFile(keysPath, []byte("ed25519 "+b64+" integ-test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile authorized_keys: %v", err)
	}

	// Start lookout with AUTHORIZED_KEYS set and no TOKEN (Ed25519-only auth).
	base, cleanup := startServerWithEnv(t,
		[]string{"AUTHORIZED_KEYS=" + keysPath},
		"", // no bearer token
	)
	defer cleanup()

	const target = "/_lookout/info" // auth-gated, unlike /_lookout/health

	// Negative control: an unsigned request must be rejected with 401. This
	// proves the endpoint is genuinely gated, so the positive case is meaningful.
	unsigned, err := http.NewRequest(http.MethodGet, base+target, nil)
	if err != nil {
		t.Fatalf("NewRequest (unsigned): %v", err)
	}
	unResp, err := http.DefaultClient.Do(unsigned)
	if err != nil {
		t.Fatalf("unsigned GET %s: %v", target, err)
	}
	unResp.Body.Close()
	if unResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned request: got %d, want 401 (endpoint must be auth-gated)", unResp.StatusCode)
	}

	// Positive: a correctly signed request must be accepted with 200.
	req, err := http.NewRequest(http.MethodGet, base+target, nil)
	if err != nil {
		t.Fatalf("NewRequest (signed): %v", err)
	}

	tsUnix := time.Now().Unix()
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	nonce := hex.EncodeToString(nonceBytes) // 32 hex characters

	// Canonical message: METHOD\nPATH\nbody-sha256-hex\nunix-timestamp\nnonce.
	emptyBodyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	msg := []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%s",
		req.Method, req.URL.Path, emptyBodyHash, tsUnix, nonce))
	sig := ed25519.Sign(priv, msg)

	// Key ID: hex(SHA-256(pubkey)[:8]), matching auth.deriveKeyID.
	h := sha256.Sum256(pub)
	keyID := hex.EncodeToString(h[:8])

	req.Header.Set("X-Lookout-Key-ID", keyID)
	req.Header.Set("X-Lookout-Timestamp", strconv.FormatInt(tsUnix, 10))
	req.Header.Set("X-Lookout-Nonce", nonce)
	req.Header.Set("X-Lookout-Signature", base64.RawURLEncoding.EncodeToString(sig))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signed GET %s: %v", target, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Ed25519 auth: got %d, want 200\nreason: %s\nbody: %s",
			resp.StatusCode, resp.Header.Get("X-Lookout-Reason"), body)
	}
}
