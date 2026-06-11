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

// TestLoadEdgeModeWithBothTokenAndURL verifies that DRYDOCK_URL + TOKEN is valid.
func TestLoadEdgeModeWithBothTokenAndURL(t *testing.T) {
	setEnv(t,
		"DRYDOCK_URL", "https://drydock.example.com",
		"TOKEN", "rawtoken",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsEdgeMode() {
		t.Fatal("expected IsEdgeMode() to be true")
	}
}
