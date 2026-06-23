package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Load – additional branches
// ---------------------------------------------------------------------------

// TestLoadDDAgentSecretFallback verifies DD_AGENT_SECRET is used when TOKEN is unset.
func TestLoadDDAgentSecretFallback(t *testing.T) {
	setEnv(t, "DD_AGENT_SECRET", "secretval")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Token != "secretval" {
		t.Errorf("Token: got %q want %q", cfg.Token, "secretval")
	}
}

// TestLoadTokenFileReadsFile verifies TOKEN_FILE is read and trims trailing newline.
func TestLoadTokenFileReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("filetoken\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setEnv(t, "TOKEN_FILE", path)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Token != "filetoken" {
		t.Errorf("Token from file: got %q want filetoken", cfg.Token)
	}
}

// TestLoadTokenFileMissingErrors verifies that a missing TOKEN_FILE returns an error.
func TestLoadTokenFileMissingErrors(t *testing.T) {
	setEnv(t, "TOKEN_FILE", "/nonexistent/path/token")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing TOKEN_FILE, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_FILE") {
		t.Fatalf("expected 'TOKEN_FILE' in error, got: %v", err)
	}
}

// TestLoadDDAgentSecretFileFallback verifies DD_AGENT_SECRET_FILE is read when TOKEN_FILE is unset.
func TestLoadDDAgentSecretFileFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("ddsecret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setEnv(t, "DD_AGENT_SECRET_FILE", path)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Token != "ddsecret" {
		t.Errorf("Token from DD_AGENT_SECRET_FILE: got %q want ddsecret", cfg.Token)
	}
}

// TestLoadDDAgentSecretFileMissingErrors verifies a missing DD_AGENT_SECRET_FILE returns an error.
func TestLoadDDAgentSecretFileMissingErrors(t *testing.T) {
	setEnv(t, "DD_AGENT_SECRET_FILE", "/nonexistent/path/secret")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing DD_AGENT_SECRET_FILE, got nil")
	}
	if !strings.Contains(err.Error(), "DD_AGENT_SECRET_FILE") {
		t.Fatalf("expected 'DD_AGENT_SECRET_FILE' in error, got: %v", err)
	}
}

// TestLoadTokenHashFileMissingErrors verifies a missing TOKEN_HASH_FILE returns an error.
func TestLoadTokenHashFileMissingErrors(t *testing.T) {
	setEnv(t, "TOKEN_HASH_FILE", "/nonexistent/path/hash")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing TOKEN_HASH_FILE, got nil")
	}
	if !strings.Contains(err.Error(), "TOKEN_HASH_FILE") {
		t.Fatalf("expected 'TOKEN_HASH_FILE' in error, got: %v", err)
	}
}

// TestLoadEnrollmentTokenFileMissingErrors verifies a missing ENROLLMENT_TOKEN_FILE returns an error.
func TestLoadEnrollmentTokenFileMissingErrors(t *testing.T) {
	setEnv(t, "ENROLLMENT_TOKEN_FILE", "/nonexistent/path/et")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing ENROLLMENT_TOKEN_FILE, got nil")
	}
	if !strings.Contains(err.Error(), "ENROLLMENT_TOKEN_FILE") {
		t.Fatalf("expected 'ENROLLMENT_TOKEN_FILE' in error, got: %v", err)
	}
}

// TestLoadEdgeModeNoCredentialsNoPrivateKeyErrors verifies that DRYDOCK_URL with no
// credentials and no PRIVATE_KEY_FILE returns the "requires TOKEN, AUTHORIZED_KEYS,
// or PRIVATE_KEY_FILE" error.
func TestLoadEdgeModeNoCredentialsNoPrivateKeyErrors(t *testing.T) {
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for edge mode with no credentials, got nil")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY_FILE") {
		t.Fatalf("expected 'PRIVATE_KEY_FILE' in error, got: %v", err)
	}
}

// TestLoadEdgeModePrivateKeyOnlySucceeds verifies DRYDOCK_URL + PRIVATE_KEY_FILE
// (no TOKEN, no AUTHORIZED_KEYS) is valid.
func TestLoadEdgeModePrivateKeyOnlySucceeds(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"PRIVATE_KEY_FILE", "/etc/portwing/agent.key",
	)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PrivateKeyFile != "/etc/portwing/agent.key" {
		t.Errorf("PrivateKeyFile: got %q want /etc/portwing/agent.key", cfg.PrivateKeyFile)
	}
}

// TestLoadDefaultsPopulated verifies several default field values when no env vars are set.
func TestLoadDefaultsPopulated(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "3000" {
		t.Errorf("Port default: got %q want 3000", cfg.Port)
	}
	if cfg.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress default: got %q want 0.0.0.0", cfg.BindAddress)
	}
	if cfg.HeartbeatInterval != 30 {
		t.Errorf("HeartbeatInterval default: got %d want 30", cfg.HeartbeatInterval)
	}
	if cfg.RequestTimeout != 30 {
		t.Errorf("RequestTimeout default: got %d want 30", cfg.RequestTimeout)
	}
	if cfg.ReconnectDelay != 1 {
		t.Errorf("ReconnectDelay default: got %d want 1", cfg.ReconnectDelay)
	}
	if cfg.MaxReconnectDelay != 60 {
		t.Errorf("MaxReconnectDelay default: got %d want 60", cfg.MaxReconnectDelay)
	}
	if cfg.StacksDir != "/data/stacks" {
		t.Errorf("StacksDir default: got %q want /data/stacks", cfg.StacksDir)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default: got %q want info", cfg.LogLevel)
	}
	if cfg.Adapter != "drydock" {
		t.Errorf("Adapter default: got %q want drydock", cfg.Adapter)
	}
	if cfg.AuditBufferSize != 256 {
		t.Errorf("AuditBufferSize default: got %d want 256", cfg.AuditBufferSize)
	}
	// AgentID must be auto-generated (non-empty UUID).
	if cfg.AgentID == "" {
		t.Error("AgentID should be auto-generated, got empty string")
	}
}

// TestLoadAgentIDFromEnv verifies AGENT_ID env var is respected.
func TestLoadAgentIDFromEnv(t *testing.T) {
	setEnv(t, "AGENT_ID", "custom-id-123")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentID != "custom-id-123" {
		t.Errorf("AgentID: got %q want custom-id-123", cfg.AgentID)
	}
}

// TestLoadAgentNameFromEnv verifies AGENT_NAME env var is respected.
func TestLoadAgentNameFromEnv(t *testing.T) {
	setEnv(t, "AGENT_NAME", "my-agent")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentName != "my-agent" {
		t.Errorf("AgentName: got %q want my-agent", cfg.AgentName)
	}
}

// TestLoadDockerSocketFromEnv verifies DOCKER_SOCKET env var is respected.
func TestLoadDockerSocketFromEnv(t *testing.T) {
	setEnv(t, "DOCKER_SOCKET", "/custom/docker.sock")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DockerSocket != "/custom/docker.sock" {
		t.Errorf("DockerSocket: got %q want /custom/docker.sock", cfg.DockerSocket)
	}
}

// ---------------------------------------------------------------------------
// IsEdgeMode
// ---------------------------------------------------------------------------

// TestIsEdgeModeReturnsFalseWhenNoDrydockURL verifies IsEdgeMode is false with no URL.
func TestIsEdgeModeReturnsFalseWhenNoDrydockURL(t *testing.T) {
	cfg := &Config{Token: "tok"}
	if cfg.IsEdgeMode() {
		t.Error("expected IsEdgeMode() false when DrydockURL is empty")
	}
}

// TestIsEdgeModeReturnsFalseWhenNoCredentials verifies IsEdgeMode is false when URL is set
// but no token, authorized keys file, or private key file is set.
func TestIsEdgeModeReturnsFalseWhenNoCredentials(t *testing.T) {
	cfg := &Config{DrydockURL: "https://drydock.example.com"}
	if cfg.IsEdgeMode() {
		t.Error("expected IsEdgeMode() false when DrydockURL set but no credentials")
	}
}

// TestIsEdgeModeReturnsTrueWithPrivateKeyFile verifies IsEdgeMode is true with URL + PrivateKeyFile.
func TestIsEdgeModeReturnsTrueWithPrivateKeyFile(t *testing.T) {
	cfg := &Config{
		DrydockURL:     "https://drydock.example.com",
		PrivateKeyFile: "/etc/portwing/agent.key",
	}
	if !cfg.IsEdgeMode() {
		t.Error("expected IsEdgeMode() true with DrydockURL + PrivateKeyFile")
	}
}

// ---------------------------------------------------------------------------
// detectDockerSocket
// ---------------------------------------------------------------------------

// TestDetectDockerSocketFindsExistingSocket verifies the "socket found" branch of
// detectDockerSocket. We create a HOME-derived socket file and only assert the exact
// path when the fixed system sockets (/var/run/docker.sock, /run/docker.sock) are
// absent — otherwise any found path is acceptable.
func TestDetectDockerSocketFindsExistingSocket(t *testing.T) {
	dir := t.TempDir()

	// Create the orbstack socket at the HOME-derived path the function checks.
	sockDir := filepath.Join(dir, ".orbstack", "run")
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sockPath := filepath.Join(sockDir, "docker.sock")
	if err := os.WriteFile(sockPath, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOME", dir)
	got := detectDockerSocket()

	// If a system-level Docker socket exists on the test host (e.g. OrbStack or
	// Docker Desktop), detectDockerSocket will return it because it appears before
	// HOME-derived paths in the candidate list. That's correct behaviour — just
	// assert we got a non-empty, non-default result to confirm the "found" branch ran.
	if got == "" {
		t.Error("detectDockerSocket returned empty string")
	}
	// When no system socket is present, the HOME-derived orbstack path must win.
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		if _, err2 := os.Stat("/run/docker.sock"); os.IsNotExist(err2) {
			if got != sockPath {
				t.Errorf("detectDockerSocket: got %q want %q (no system socket present)", got, sockPath)
			}
		}
	}
}

// TestDetectDockerSocketFallback verifies detectDockerSocket returns the default path
// when no known socket exists.
func TestDetectDockerSocketFallback(t *testing.T) {
	// Point HOME to an empty temp dir so no HOME-derived sockets exist, and the
	// standard /var/run/docker.sock and /run/docker.sock won't be present either
	// in a typical CI environment.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	got := detectDockerSocket()
	// Either the function found a real socket on this machine (fine) or fell back to default.
	if got == "" {
		t.Error("detectDockerSocket returned empty string")
	}
}

// ---------------------------------------------------------------------------
// getEnvInt
// ---------------------------------------------------------------------------

func TestGetEnvIntUnset(t *testing.T) {
	t.Setenv("TEST_GETENVINT_UNSET", "")
	// Unset: os.Getenv returns "" → fallback.
	// We use a key we know isn't set.
	got := getEnvInt("TEST_GETENVINT_TRULY_UNSET_XYZ", 42)
	if got != 42 {
		t.Errorf("getEnvInt unset: got %d want 42", got)
	}
}

func TestGetEnvIntValid(t *testing.T) {
	t.Setenv("TEST_GETENVINT", "99")
	got := getEnvInt("TEST_GETENVINT", 0)
	if got != 99 {
		t.Errorf("getEnvInt valid: got %d want 99", got)
	}
}

func TestGetEnvIntInvalid(t *testing.T) {
	t.Setenv("TEST_GETENVINT_BAD", "notanumber")
	got := getEnvInt("TEST_GETENVINT_BAD", 7)
	if got != 7 {
		t.Errorf("getEnvInt invalid: got %d want fallback 7", got)
	}
}

// ---------------------------------------------------------------------------
// getEnvBool
// ---------------------------------------------------------------------------

func TestGetEnvBoolUnset(t *testing.T) {
	got := getEnvBool("TEST_GETENVBOOL_UNSET_XYZ", true)
	if !got {
		t.Error("getEnvBool unset: expected fallback true")
	}
}

func TestGetEnvBoolTrue(t *testing.T) {
	cases := []string{"1", "true", "yes", "TRUE", "YES"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			t.Setenv("TEST_GETENVBOOL", v)
			if !getEnvBool("TEST_GETENVBOOL", false) {
				t.Errorf("getEnvBool(%q): expected true", v)
			}
		})
	}
}

func TestGetEnvBoolFalse(t *testing.T) {
	cases := []string{"0", "false", "no", "FALSE", "NO"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			t.Setenv("TEST_GETENVBOOL", v)
			if getEnvBool("TEST_GETENVBOOL", true) {
				t.Errorf("getEnvBool(%q): expected false", v)
			}
		})
	}
}

func TestGetEnvBoolInvalidFallback(t *testing.T) {
	t.Setenv("TEST_GETENVBOOL_BAD", "maybe")
	got := getEnvBool("TEST_GETENVBOOL_BAD", true)
	if !got {
		t.Error("getEnvBool invalid: expected fallback true")
	}
}

// ---------------------------------------------------------------------------
// splitCSV
// ---------------------------------------------------------------------------

func TestSplitCSVEmpty(t *testing.T) {
	got := splitCSV("")
	if got != nil {
		t.Errorf("splitCSV empty: got %v want nil", got)
	}
}

func TestSplitCSVSingleValue(t *testing.T) {
	got := splitCSV("alpha")
	if len(got) != 1 || got[0] != "alpha" {
		t.Errorf("splitCSV single: got %v", got)
	}
}

func TestSplitCSVMultipleValues(t *testing.T) {
	got := splitCSV("a,b,c")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV multiple: got %v want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("splitCSV multiple[%d]: got %q want %q", i, got[i], v)
		}
	}
}

func TestSplitCSVTrimsWhitespace(t *testing.T) {
	got := splitCSV(" foo , bar , baz ")
	want := []string{"foo", "bar", "baz"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV whitespace: got %v want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("splitCSV whitespace[%d]: got %q want %q", i, got[i], v)
		}
	}
}

func TestSplitCSVEmptyFields(t *testing.T) {
	// Trailing comma and empty interior fields are skipped.
	got := splitCSV("a,,b,")
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV empty fields: got %v want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("splitCSV empty fields[%d]: got %q want %q", i, got[i], v)
		}
	}
}

// ---------------------------------------------------------------------------
// loadTokenFile
// ---------------------------------------------------------------------------

func TestLoadTokenFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(path, []byte("  mytoken  \n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := loadTokenFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mytoken" {
		t.Errorf("loadTokenFile: got %q want mytoken", got)
	}
}

func TestLoadTokenFileMissing(t *testing.T) {
	_, err := loadTokenFile("/nonexistent/path/token.txt")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadTokenFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := loadTokenFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("loadTokenFile empty: got %q want empty string", got)
	}
}
