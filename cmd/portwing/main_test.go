package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// shortTempDir returns a short temp directory suitable for unix socket paths.
// macOS limits unix socket paths to 104 bytes.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pwtest*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startMockDocker launches a minimal Docker-API HTTP server on a unix socket
// and returns the socket path. The server handles just /version (needed for
// NewClient negotiation and GetVersion).
func startMockDocker(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	versionPrefix := regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := versionPrefix.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/version", "/_ping":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Version":    "24.0.0-mock",
				"ApiVersion": "1.44",
			})
		default:
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return sockPath
}

// newDockerClientForTest creates a docker.Client pointed at a dummy socket path.
// The constructors (generic.New / drydock.NewAdapter) only store the client —
// they never actually connect — so the socket doesn't have to exist.
func newDockerClientForTest(t *testing.T) *docker.Client {
	t.Helper()
	// Point at a nonexistent socket; NewClient only dials lazily.
	c, err := docker.NewClient("/tmp/portwing-test-nonexistent.sock", 5)
	if err != nil {
		t.Fatalf("docker.NewClient: %v", err)
	}
	return c
}

// setenv sets an env var for the duration of the test and restores the
// original value (or unsets) via t.Cleanup.
func setenv(t *testing.T, key, value string) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("Setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, orig) //nolint:errcheck
		} else {
			os.Unsetenv(key) //nolint:errcheck
		}
	})
}

func unsetenv(t *testing.T, key string) {
	t.Helper()
	orig, had := os.LookupEnv(key)
	os.Unsetenv(key) //nolint:errcheck
	t.Cleanup(func() {
		if had {
			os.Setenv(key, orig) //nolint:errcheck
		}
	})
}

// writeTempFile writes content to a new temp file with the given permissions
// and returns the path.
func writeTempFile(t *testing.T, dir, name string, content []byte, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

// ---------------------------------------------------------------------------
// modeString
// ---------------------------------------------------------------------------

func TestModeString_Standard(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{} // no DrydockURL → standard
	if got := modeString(cfg); got != "standard" {
		t.Fatalf("want standard, got %q", got)
	}
}

func TestModeString_Edge(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DrydockURL: "wss://example.com",
		Token:      "secret",
	}
	if got := modeString(cfg); got != "edge" {
		t.Fatalf("want edge, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// selectAdapter
// ---------------------------------------------------------------------------

func TestSelectAdapter_Generic(t *testing.T) {
	t.Parallel()
	c := newDockerClientForTest(t)
	cfg := &config.Config{Adapter: "generic", AgentName: "test"}
	a := selectAdapter(cfg, c)
	if a.Name() != "generic" {
		t.Fatalf("want generic, got %q", a.Name())
	}
}

func TestSelectAdapter_Drydock(t *testing.T) {
	t.Parallel()
	c := newDockerClientForTest(t)
	cfg := &config.Config{Adapter: "drydock", AgentName: "test"}
	a := selectAdapter(cfg, c)
	if a.Name() != "drydock" {
		t.Fatalf("want drydock, got %q", a.Name())
	}
}

func TestSelectAdapter_UnknownFallsBackToDrydock(t *testing.T) {
	t.Parallel()
	c := newDockerClientForTest(t)
	cfg := &config.Config{Adapter: "unknown-xyz", AgentName: "test"}
	a := selectAdapter(cfg, c)
	if a.Name() != "drydock" {
		t.Fatalf("want drydock fallback, got %q", a.Name())
	}
}

// ---------------------------------------------------------------------------
// runHashToken
// ---------------------------------------------------------------------------

func TestRunHashToken_ValidToken(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("mysecrettoken\n")
	code := runHashToken(stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	out := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(out, "$argon2id$") {
		t.Fatalf("expected argon2id PHC, got %q", out)
	}
}

func TestRunHashToken_EmptyToken(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("   \n")
	code := runHashToken(stdin, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for empty token, got %d", code)
	}
	if !strings.Contains(stderr.String(), "empty") {
		t.Fatalf("expected 'empty' in stderr, got %q", stderr.String())
	}
}

func TestRunHashToken_NoInput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("") // EOF immediately
	code := runHashToken(stdin, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for no input, got %d", code)
	}
	if !strings.Contains(stderr.String(), "no input provided") {
		t.Fatalf("expected 'no input provided' in stderr, got %q", stderr.String())
	}
}

func TestRunHashToken_ScanError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	// errorReader returns an error on Read to exercise the scanner error path.
	stdin := &errorReader{}
	code := runHashToken(stdin, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for scan error, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error reading input") {
		t.Fatalf("expected 'error reading input' in stderr, got %q", stderr.String())
	}
}

// errorReader always returns an error from Read.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, fmt.Errorf("simulated read error")
}

// ---------------------------------------------------------------------------
// runKeygen
// ---------------------------------------------------------------------------

func TestRunKeygen_NewKeypair(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runKeygen([]string{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "BEGIN PRIVATE KEY") {
		t.Fatalf("expected PEM private key on stdout, got: %s", outStr)
	}
	if !strings.Contains(outStr, "ed25519 ") {
		t.Fatalf("expected authorized_keys line on stdout, got: %s", outStr)
	}
}

func TestRunKeygen_NewKeypairWithComment(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runKeygen([]string{"-comment", "myhost"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "myhost") {
		t.Fatalf("expected comment in authorized_keys line, got: %s", stdout.String())
	}
}

func TestRunKeygen_PubFrom_Valid(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)

	// First generate a key to get a PEM file.
	var keyOut bytes.Buffer
	code := runKeygen([]string{}, &keyOut, io.Discard)
	if code != 0 {
		t.Fatalf("keygen failed: %d", code)
	}
	// Extract just the PEM block from the combined stdout (PEM + authkey line).
	pemData := extractPEM(keyOut.String())
	if len(pemData) == 0 {
		t.Fatalf("no PEM found in keygen output: %s", keyOut.String())
	}

	keyFile := writeTempFile(t, dir, "key.pem", []byte(pemData), 0o600)

	var stdout, stderr bytes.Buffer
	code = runKeygen([]string{"-pub-from", keyFile, "-comment", "re-derived"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("pub-from failed: %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ed25519 ") {
		t.Fatalf("expected authorized_keys line, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "re-derived") {
		t.Fatalf("expected comment in output, got: %s", stdout.String())
	}
}

func TestRunKeygen_PubFrom_NonexistentFile(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runKeygen([]string{"-pub-from", "/nonexistent/key.pem"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for missing file, got %d", code)
	}
	if !strings.Contains(stderr.String(), "keygen:") {
		t.Fatalf("expected error in stderr, got: %q", stderr.String())
	}
}

func TestRunKeygen_PubFrom_InvalidPEM(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	badFile := writeTempFile(t, dir, "bad.pem", []byte("not a pem file\n"), 0o600)

	var stdout, stderr bytes.Buffer
	code := runKeygen([]string{"-pub-from", badFile}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for invalid PEM, got %d", code)
	}
}

func TestRunKeygen_BadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runKeygen([]string{"-no-such-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for bad flag, got %d", code)
	}
}

// extractPEM pulls the first PEM block (-----BEGIN ... END-----) from s.
func extractPEM(s string) string {
	begin := strings.Index(s, "-----BEGIN")
	if begin < 0 {
		return ""
	}
	end := strings.Index(s[begin:], "-----END")
	if end < 0 {
		return ""
	}
	// Find the actual end of the END line.
	endLine := strings.Index(s[begin+end:], "\n")
	if endLine < 0 {
		return s[begin:]
	}
	return s[begin : begin+end+endLine+1]
}

// ---------------------------------------------------------------------------
// run — config load failure
// ---------------------------------------------------------------------------

func TestRun_ConfigLoadFailure_TokenAndHash(t *testing.T) {
	// Setting both TOKEN and TOKEN_HASH causes config.Load() to return an error.
	setenv(t, "TOKEN", "abc")
	setenv(t, "TOKEN_HASH", "$argon2id$v=19$m=19456,t=2,p=1$fakesalt$fakehash")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for config error, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// run — docker connect failure
// ---------------------------------------------------------------------------

func TestRun_DockerConnectFailure(t *testing.T) {
	// Point DOCKER_SOCKET at a path that definitely doesn't exist so
	// GetVersion fails after NewClient succeeds (NewClient only dials lazily).
	setenv(t, "DOCKER_SOCKET", "/nonexistent/portwing-test-no-docker.sock")
	// Clear any env vars that would cause config.Load() to fail first.
	unsetenv(t, "TOKEN")
	unsetenv(t, "TOKEN_HASH")
	unsetenv(t, "DRYDOCK_URL")
	unsetenv(t, "TOKEN_FILE")
	unsetenv(t, "DD_AGENT_SECRET_FILE")
	unsetenv(t, "TOKEN_HASH_FILE")
	unsetenv(t, "ENROLLMENT_TOKEN_FILE")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for docker connect failure, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// run — subcommand dispatch through run()
// ---------------------------------------------------------------------------

func TestRun_HashTokenDispatch(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing", "hash-token"}, strings.NewReader("mytoken\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "$argon2id$") {
		t.Fatalf("expected argon2id output, got: %s", stdout.String())
	}
}

func TestRun_KeygenDispatch(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing", "keygen"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "BEGIN PRIVATE KEY") {
		t.Fatalf("expected PEM on stdout, got: %s", stdout.String())
	}
}

func TestRun_VersionDispatchBeforeConfig(t *testing.T) {
	// Deliberately make the server configuration invalid. The version
	// subcommand must be self-contained so package-install smoke tests work on
	// hosts with no Docker socket or Portwing configuration.
	setenv(t, "TOKEN", "plaintext")
	setenv(t, "TOKEN_HASH", "cannot-be-set-with-token")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing", "version"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
	}
	if got, want := stdout.String(), protocol.AgentVersion+"\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// run — --help / -h / --version / -V / unrecognized flags
// ---------------------------------------------------------------------------

func TestRun_HelpFlag_ExitsZeroWithoutDockerConnection(t *testing.T) {
	// Setting both TOKEN and TOKEN_HASH makes config.Load() fail, and there's
	// no docker socket to connect to. If --help fell through to the agent
	// startup path (as it did before this fix), run() would return 1 here
	// instead of 0, proving --help short-circuits before config/docker.
	setenv(t, "TOKEN", "abc")
	setenv(t, "TOKEN_HASH", "$argon2id$v=19$m=19456,t=2,p=1$fakesalt$fakehash")
	setenv(t, "DOCKER_SOCKET", "/nonexistent/portwing-test-no-docker.sock")

	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"portwing", flag}, strings.NewReader(""), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("want exit 0, got %d; stderr: %s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage: portwing") {
				t.Fatalf("expected usage on stdout, got: %s", stdout.String())
			}
			if !strings.Contains(stdout.String(), "hash-token") || !strings.Contains(stdout.String(), "keygen") {
				t.Fatalf("expected usage to list subcommands, got: %s", stdout.String())
			}
			if stderr.String() != "" {
				t.Fatalf("expected no stderr output, got: %s", stderr.String())
			}
		})
	}
}

func TestRun_VersionFlag_MatchesVersionSubcommand(t *testing.T) {
	t.Parallel()

	var wantOut bytes.Buffer
	var wantErr bytes.Buffer
	wantCode := run([]string{"portwing", "version"}, strings.NewReader(""), &wantOut, &wantErr)

	for _, flag := range []string{"--version", "-V"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run([]string{"portwing", flag}, strings.NewReader(""), &stdout, &stderr)
			if code != wantCode {
				t.Fatalf("exit code = %d, want %d", code, wantCode)
			}
			if stdout.String() != wantOut.String() {
				t.Fatalf("stdout = %q, want %q", stdout.String(), wantOut.String())
			}
		})
	}
}

func TestRun_UnknownFlag_ExitsNonZero(t *testing.T) {
	// Same invalid config + missing docker socket as the --help test above,
	// so a code path that fell through to agent startup would also return
	// non-zero here, but for the wrong reason (config error, not a usage
	// error) and without ever printing usage.
	setenv(t, "TOKEN", "abc")
	setenv(t, "TOKEN_HASH", "$argon2id$v=19$m=19456,t=2,p=1$fakesalt$fakehash")
	setenv(t, "DOCKER_SOCKET", "/nonexistent/portwing-test-no-docker.sock")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing", "--bogus"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("want non-zero exit for unrecognized flag, got 0")
	}
	if !strings.Contains(stderr.String(), "--bogus") {
		t.Fatalf("expected stderr to name the bad flag, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage: portwing") {
		t.Fatalf("expected usage on stderr, got: %s", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("expected no stdout output, got: %s", stdout.String())
	}
}

// ---------------------------------------------------------------------------
// run — server.NewServer failure (bad TOKEN_HASH with valid docker)
// ---------------------------------------------------------------------------

func TestRun_NewServerFailure_BadTokenHash(t *testing.T) {
	sockPath := startMockDocker(t)

	setenv(t, "DOCKER_SOCKET", sockPath)
	// A syntactically invalid PHC string — ParsePHC rejects it, causing NewServer to fail.
	setenv(t, "TOKEN_HASH", "not-a-valid-phc-string")
	unsetenv(t, "TOKEN")
	unsetenv(t, "DRYDOCK_URL")
	unsetenv(t, "TOKEN_FILE")
	unsetenv(t, "DD_AGENT_SECRET_FILE")
	unsetenv(t, "TOKEN_HASH_FILE")
	unsetenv(t, "ENROLLMENT_TOKEN_FILE")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for bad TOKEN_HASH, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// run — ListenAndServe failure (invalid bind address)
// ---------------------------------------------------------------------------

func TestRun_ListenAndServeFailure(t *testing.T) {
	sockPath := startMockDocker(t)

	setenv(t, "DOCKER_SOCKET", sockPath)
	// An invalid bind address causes ListenAndServe to return a real error (not ErrServerClosed).
	setenv(t, "BIND_ADDRESS", "invalid-host-that-cannot-bind")
	setenv(t, "PORT", "99999") // also invalid port for good measure
	setenv(t, "TOKEN", "test-token")
	unsetenv(t, "TOKEN_HASH")
	unsetenv(t, "DRYDOCK_URL")
	unsetenv(t, "TOKEN_FILE")
	unsetenv(t, "DD_AGENT_SECRET_FILE")
	unsetenv(t, "TOKEN_HASH_FILE")
	unsetenv(t, "ENROLLMENT_TOKEN_FILE")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for invalid bind, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// edge mode helpers
// ---------------------------------------------------------------------------

// generatePrivKeyFile generates an Ed25519 private key and writes it to
// dir/key.pem with 0600 perms. Returns the file path.
func generatePrivKeyFile(t *testing.T, dir string) string {
	t.Helper()
	privPEM, _, err := auth.GenerateKeyPair("")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, privPEM, 0o600); err != nil {
		t.Fatalf("WriteFile key.pem: %v", err)
	}
	return path
}

// clearEdgeEnv unsets env vars that affect edge mode / auth config so
// config.Load() doesn't fail before we reach the code under test.
func clearEdgeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TOKEN", "TOKEN_HASH", "TOKEN_FILE", "DD_AGENT_SECRET",
		"DD_AGENT_SECRET_FILE", "TOKEN_HASH_FILE", "ENROLLMENT_TOKEN_FILE",
		"AUTHORIZED_KEYS", "AUTHORIZED_KEYS_FILE",
	} {
		unsetenv(t, k)
	}
	// These tests point DRYDOCK_URL at a local, unencrypted test double (a dead
	// address or a plain-HTTP fake server), never a real controller, so opt
	// into the plaintext scheme the same way a trusted local test setup would.
	setenv(t, "ALLOW_INSECURE_EDGE_URL", "true")
}

// startFakeWS404Server starts an HTTP server on a random TCP port that
// responds 404 to all requests. Returns "http://host:port".
// gorilla/websocket sees 404 on the upgrade attempt and returns ErrBadHandshake,
// which the edge client maps to errFatal so Run exits without retrying.
func startFakeWS404Server(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

// ---------------------------------------------------------------------------
// run — edge mode: audit.New failure
// ---------------------------------------------------------------------------

func TestRun_EdgeMode_AuditNewFailure(t *testing.T) {
	sockPath := startMockDocker(t)
	dir := shortTempDir(t)
	keyPath := generatePrivKeyFile(t, dir)

	setenv(t, "DOCKER_SOCKET", sockPath)
	setenv(t, "DRYDOCK_URL", "ws://127.0.0.1:19999") // dead, won't be reached
	setenv(t, "PRIVATE_KEY_FILE", keyPath)
	// Point AUDIT_LOG at a path whose parent doesn't exist so audit.New fails.
	setenv(t, "AUDIT_LOG", "/nonexistent-dir-pwtest/audit.log")
	setenv(t, "PORT", "0")
	clearEdgeEnv(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for audit.New failure, got %d (stderr: %s)", code, stderr.String())
	}
}

// ---------------------------------------------------------------------------
// run — edge mode: fatal WS error → edge error branch (run returns 1)
// ---------------------------------------------------------------------------

func TestRun_EdgeMode_FatalWSError(t *testing.T) {
	// startFakeWS404Server returns 404 on the WebSocket upgrade, which gorilla
	// maps to ErrBadHandshake. The edge client wraps this as errFatal, so
	// edgeClient.Run() returns a non-nil error while ctx.Err()==nil → run()
	// returns 1 (the error branch at main.go lines ~98-100).
	sockPath := startMockDocker(t)
	dir := shortTempDir(t)
	keyPath := generatePrivKeyFile(t, dir)
	wsBase := startFakeWS404Server(t)

	setenv(t, "DOCKER_SOCKET", sockPath)
	setenv(t, "DRYDOCK_URL", wsBase)
	setenv(t, "PRIVATE_KEY_FILE", keyPath)
	setenv(t, "PORT", "0")
	setenv(t, "TLS_SKIP_VERIFY", "true")
	clearEdgeEnv(t)
	// Disable audit log so audit.New doesn't fail.
	setenv(t, "AUDIT_LOG", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for fatal WS 404, got %d (stderr: %s)", code, stderr.String())
	}
}

// ---------------------------------------------------------------------------
// run — edge mode: SIGTERM → graceful shutdown (run returns 0)
// ---------------------------------------------------------------------------

func TestRun_EdgeMode_GracefulShutdown(t *testing.T) {
	// Edge client connects to a dead WS address, enters reconnect loop.
	// We send SIGTERM quickly → ctx cancelled → Run returns ctx.Err() →
	// condition (err != nil && ctx.Err() == nil) is false → run() returns 0.
	// Set RECONNECT_DELAY=0 so jitteredDuration is tiny and the loop is fast.
	sockPath := startMockDocker(t)
	dir := shortTempDir(t)
	keyPath := generatePrivKeyFile(t, dir)

	setenv(t, "DOCKER_SOCKET", sockPath)
	// Use a non-routable address so the dial fails immediately without waiting.
	setenv(t, "DRYDOCK_URL", "ws://127.0.0.1:19998")
	setenv(t, "PRIVATE_KEY_FILE", keyPath)
	setenv(t, "PORT", "0")
	setenv(t, "RECONNECT_DELAY", "0")
	setenv(t, "MAX_RECONNECT_DELAY", "0")
	clearEdgeEnv(t)
	setenv(t, "AUDIT_LOG", "")

	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
		done <- code
	}()

	// Allow the edge client to start its health server and attempt one connect
	// before we cancel via SIGTERM.
	time.Sleep(150 * time.Millisecond)
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("want exit 0 after graceful SIGTERM, got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit within 5s after SIGTERM")
	}
}

// ---------------------------------------------------------------------------
// run — standard mode happy path via mock docker
// ---------------------------------------------------------------------------

func TestRun_StandardMode_MockDocker(t *testing.T) {
	sockPath := startMockDocker(t)

	setenv(t, "DOCKER_SOCKET", sockPath)
	setenv(t, "PORT", "0") // bind to a random port so no real port is consumed
	setenv(t, "TOKEN", "test-token")
	unsetenv(t, "TOKEN_HASH")
	unsetenv(t, "DRYDOCK_URL")
	unsetenv(t, "TOKEN_FILE")
	unsetenv(t, "DD_AGENT_SECRET_FILE")
	unsetenv(t, "TOKEN_HASH_FILE")
	unsetenv(t, "ENROLLMENT_TOKEN_FILE")
	setenv(t, "ADAPTER", "drydock")

	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
		done <- code
	}()

	// Give the server a moment to start, then send SIGTERM to shut it down.
	time.Sleep(200 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	_ = p.Signal(syscall.SIGTERM)

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit within 5s after SIGTERM")
	}
}

// ---------------------------------------------------------------------------
// run — standard mode: SIGTERM must drain an in-flight handler, not cut it
// ---------------------------------------------------------------------------

// startMockDockerSlowPing is startMockDocker plus a /_ping handler that
// signals pingStarted before sleeping for delay. That lets a test send
// SIGTERM while a request is genuinely in flight inside the handler, instead
// of guessing with a fixed sleep.
func startMockDockerSlowPing(t *testing.T, pingStarted chan<- struct{}, delay time.Duration) string {
	t.Helper()
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	versionPrefix := regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := versionPrefix.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Version":    "24.0.0-mock",
				"ApiVersion": "1.44",
			})
		case "/_ping":
			select {
			case pingStarted <- struct{}{}:
			default:
			}
			time.Sleep(delay)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return sockPath
}

// freeTCPPort asks the OS for an ephemeral port, then releases it so the
// server under test can bind it by number (needed because PORT=0 doesn't
// tell the test which port the OS picked).
func freeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}

// TestRun_StandardMode_DrainsInFlightRequestOnShutdown is a regression test
// for run() returning as soon as ListenAndServe unblocks (which happens the
// instant Shutdown closes the listener) instead of waiting for Shutdown
// itself to finish draining in-flight handlers. If run() returns early,
// main()'s os.Exit would race ahead of a still-running handler (e.g. a
// compose deploy) and cut it off.
//
// It sends SIGTERM only once a real request is confirmed in flight inside a
// slow handler, then asserts both that the request completes successfully
// and that run() does not return until after it does.
func TestRun_StandardMode_DrainsInFlightRequestOnShutdown(t *testing.T) {
	const pingDelay = 400 * time.Millisecond

	pingStarted := make(chan struct{}, 1)
	sockPath := startMockDockerSlowPing(t, pingStarted, pingDelay)
	port := freeTCPPort(t)

	setenv(t, "DOCKER_SOCKET", sockPath)
	setenv(t, "BIND_ADDRESS", "127.0.0.1")
	setenv(t, "PORT", port)
	setenv(t, "TOKEN", "test-token")
	unsetenv(t, "TOKEN_HASH")
	unsetenv(t, "DRYDOCK_URL")
	unsetenv(t, "TOKEN_FILE")
	unsetenv(t, "DD_AGENT_SECRET_FILE")
	unsetenv(t, "TOKEN_HASH_FILE")
	unsetenv(t, "ENROLLMENT_TOKEN_FILE")
	setenv(t, "ADAPTER", "drydock")

	base := "http://127.0.0.1:" + port
	httpClient := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	t.Cleanup(httpClient.CloseIdleConnections)

	// runResult carries the return time captured inside the goroutine, at the
	// moment run() actually returns — NOT the time the test later happens to
	// receive from the channel. runDone is buffered, so a receive-time
	// timestamp would just measure when the test got around to reading it,
	// silently defeating the regression check below.
	type runResult struct {
		code       int
		returnedAt time.Time
	}
	runDone := make(chan runResult, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := run([]string{"portwing"}, strings.NewReader(""), &stdout, &stderr)
		runDone <- runResult{code: code, returnedAt: time.Now()}
	}()

	// Wait for the server to accept connections (unauthenticated liveness
	// endpoint, doesn't touch docker).
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := httpClient.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become reachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// /_portwing/health requires no auth and calls docker Ping, which the
	// mock daemon delays by pingDelay — our in-flight handler.
	type reqResult struct {
		status int
		err    error
		done   time.Time
	}
	reqDone := make(chan reqResult, 1)
	go func() {
		resp, err := httpClient.Get(base + "/_portwing/health")
		res := reqResult{err: err, done: time.Now()}
		if err == nil {
			res.status = resp.StatusCode
			resp.Body.Close()
		}
		reqDone <- res
	}()

	// Send SIGTERM only once the handler is confirmed to be inside the slow
	// docker call, so the shutdown genuinely races an in-flight request.
	select {
	case <-pingStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never reached the docker ping")
	}
	sigSentAt := time.Now()
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}

	var result reqResult
	select {
	case result = <-reqDone:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request never completed")
	}
	if result.err != nil {
		t.Fatalf("in-flight request was cut off by shutdown: %v", result.err)
	}
	if result.status != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", result.status)
	}
	if result.done.Sub(sigSentAt) < pingDelay/2 {
		t.Fatalf("in-flight request completed suspiciously fast after SIGTERM (%v); the mock's delay may not have been exercised", result.done.Sub(sigSentAt))
	}

	var runRes runResult
	select {
	case runRes = <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit within 5s after SIGTERM")
	}
	if runRes.code != 0 {
		t.Fatalf("run returned %d, want 0", runRes.code)
	}
	// The crux of the regression test: run() must not return until after the
	// in-flight handler actually finished draining. Reverting the fix makes
	// run() return as soon as ListenAndServe unblocks (right after Shutdown
	// closes the listener), which happens well before the handler above
	// finishes sleeping — this assertion catches that.
	if runRes.returnedAt.Before(result.done) {
		t.Fatalf("run() returned at %v, before the in-flight request finished at %v", runRes.returnedAt, result.done)
	}
}
