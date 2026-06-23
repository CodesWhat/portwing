package config

import (
	"os"
	"strings"
	"testing"
)

// setEnv sets environment variables for the duration of a test.
func setEnv(t *testing.T, kv ...string) {
	t.Helper()
	if len(kv)%2 != 0 {
		t.Fatal("setEnv requires an even number of key/value arguments")
	}
	for i := 0; i < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

// TestLoadBothTokenAndHashErrors ensures that setting TOKEN and TOKEN_HASH
// simultaneously returns an error.
func TestLoadBothTokenAndHashErrors(t *testing.T) {
	setEnv(t,
		"TOKEN", "rawtoken",
		"TOKEN_HASH", "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaGhhc2g",
	)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when both TOKEN and TOKEN_HASH are set, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected 'mutually exclusive' in error, got: %v", err)
	}
}

// TestLoadEdgeModeWithHashOnlyErrors ensures that DRYDOCK_URL + TOKEN_HASH
// (without TOKEN) returns an error.
func TestLoadEdgeModeWithHashOnlyErrors(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"TOKEN_HASH", "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaGhhc2g",
	)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for edge mode with TOKEN_HASH only, got nil")
	}
	if !strings.Contains(err.Error(), "edge mode") {
		t.Fatalf("expected 'edge mode' in error, got: %v", err)
	}
}

// TestLoadTokenHashOnly ensures that TOKEN_HASH alone is accepted.
func TestLoadTokenHashOnly(t *testing.T) {
	const phc = "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaGhhc2g"
	setEnv(t, "TOKEN_HASH", phc)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TokenHash != phc {
		t.Fatalf("TokenHash: got %q, want %q", cfg.TokenHash, phc)
	}
	if cfg.Token != "" {
		t.Fatalf("Token should be empty, got %q", cfg.Token)
	}
}

// TestLoadTokenHashFileLoadsFromFile verifies TOKEN_HASH_FILE is read correctly.
func TestLoadTokenHashFileLoadsFromFile(t *testing.T) {
	const phc = "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$aGFzaGhhc2g"

	dir := t.TempDir()
	path := dir + "/token_hash"
	if err := os.WriteFile(path, []byte(phc+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	setEnv(t, "TOKEN_HASH_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TokenHash != phc {
		t.Fatalf("TokenHash: got %q, want %q", cfg.TokenHash, phc)
	}
}

// TestLoadEdgeModeWithBothTokenAndURL verifies that DRYDOCK_URL + TOKEN + PRIVATE_KEY_FILE is valid.
// PRIVATE_KEY_FILE is required in edge mode because drydock rejects token-only agents.
func TestLoadEdgeModeWithBothTokenAndURL(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"TOKEN", "rawtoken",
		"PRIVATE_KEY_FILE", "/etc/portwing/agent.key",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsEdgeMode() {
		t.Fatal("expected IsEdgeMode() to be true")
	}
}

// TestLoadAuthorizedKeysEnvVars verifies AUTHORIZED_KEYS and AUTHORIZED_KEYS_FILE.
func TestLoadAuthorizedKeysEnvVars(t *testing.T) {
	setEnv(t, "AUTHORIZED_KEYS", "/tmp/test_ak")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthorizedKeysFile != "/tmp/test_ak" {
		t.Errorf("AuthorizedKeysFile: got %q want /tmp/test_ak", cfg.AuthorizedKeysFile)
	}
}

// TestLoadAuthorizedKeysFileAlias verifies AUTHORIZED_KEYS_FILE is an alias.
func TestLoadAuthorizedKeysFileAlias(t *testing.T) {
	setEnv(t, "AUTHORIZED_KEYS_FILE", "/tmp/test_akf")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthorizedKeysFile != "/tmp/test_akf" {
		t.Errorf("AuthorizedKeysFile via alias: got %q want /tmp/test_akf", cfg.AuthorizedKeysFile)
	}
}

// TestLoadNonceLRUSizeDefault verifies NONCE_LRU_SIZE defaults to 10000.
func TestLoadNonceLRUSizeDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NonceLRUSize != 10000 {
		t.Errorf("NonceLRUSize default: got %d want 10000", cfg.NonceLRUSize)
	}
}

// TestLoadMaxClockSkewDefault verifies MAX_CLOCK_SKEW_SECONDS defaults to 60.
func TestLoadMaxClockSkewDefault(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxClockSkewSeconds != 60 {
		t.Errorf("MaxClockSkewSeconds default: got %d want 60", cfg.MaxClockSkewSeconds)
	}
}

// TestLoadEnrollmentToken verifies ENROLLMENT_TOKEN is loaded.
func TestLoadEnrollmentToken(t *testing.T) {
	setEnv(t, "ENROLLMENT_TOKEN", "topsecret")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EnrollmentToken != "topsecret" {
		t.Errorf("EnrollmentToken: got %q want topsecret", cfg.EnrollmentToken)
	}
}

// TestLoadEnrollmentTokenFile verifies ENROLLMENT_TOKEN_FILE is read.
func TestLoadEnrollmentTokenFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/et"
	if err := os.WriteFile(path, []byte("filetoken\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	setEnv(t, "ENROLLMENT_TOKEN_FILE", path)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EnrollmentToken != "filetoken" {
		t.Errorf("EnrollmentToken from file: got %q want filetoken", cfg.EnrollmentToken)
	}
}

// TestIsEdgeModeWithAuthorizedKeys verifies IsEdgeMode with AUTHORIZED_KEYS.
// PRIVATE_KEY_FILE is also required because drydock rejects token-only agents.
func TestIsEdgeModeWithAuthorizedKeys(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"AUTHORIZED_KEYS", "/tmp/ak",
		"PRIVATE_KEY_FILE", "/etc/portwing/agent.key",
	)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsEdgeMode() {
		t.Error("expected IsEdgeMode() true with DRYDOCK_URL + AUTHORIZED_KEYS")
	}
}

// TestLoadEdgeModeWithoutPrivateKeyErrors verifies that DRYDOCK_URL without PRIVATE_KEY_FILE
// always fails, even when TOKEN is set. Drydock rejects token-only agents.
func TestLoadEdgeModeWithoutPrivateKeyErrors(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"TOKEN", "rawtoken",
	)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for edge mode without PRIVATE_KEY_FILE, got nil")
	}
	if !strings.Contains(err.Error(), "PRIVATE_KEY_FILE") {
		t.Fatalf("expected 'PRIVATE_KEY_FILE' in error, got: %v", err)
	}
}
