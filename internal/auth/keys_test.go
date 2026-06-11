package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeKeyFile writes content to a temp file with the given permission bits
// and returns the path.
func writeKeyFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("writeKeyFile: %v", err)
	}
	return path
}

// genPubKeyB64 generates a random Ed25519 public key and returns its
// standard base64 encoding (the format used in authorized_keys).
func genPubKeyB64(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, base64.StdEncoding.EncodeToString(pub)
}

// ---- parseKeyLine tests ---------------------------------------------------

func TestParseKeyLine_Valid(t *testing.T) {
	t.Parallel()
	pub, b64 := genPubKeyB64(t)

	line := "ed25519 " + b64 + " mycomment"
	k, err := parseKeyLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k.Comment != "mycomment" {
		t.Errorf("comment: got %q want %q", k.Comment, "mycomment")
	}
	if len(k.PubKey) != 32 {
		t.Errorf("pubkey length: got %d want 32", len(k.PubKey))
	}
	// Key ID must match our derivation.
	want := deriveKeyID(pub)
	if k.KeyID != want {
		t.Errorf("key_id: got %q want %q", k.KeyID, want)
	}
}

func TestParseKeyLine_NoComment(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	k, err := parseKeyLine("ed25519 " + b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k.Comment != "" {
		t.Errorf("expected empty comment, got %q", k.Comment)
	}
}

func TestParseKeyLine_MultiWordComment(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	k, err := parseKeyLine("ed25519 " + b64 + " platform:drydock:prod:2026-06-11")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k.Comment != "platform:drydock:prod:2026-06-11" {
		t.Errorf("comment: got %q", k.Comment)
	}
}

func TestParseKeyLine_WrongAlgorithm(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	_, err := parseKeyLine("rsa " + b64)
	if err == nil {
		t.Fatal("expected error for wrong algorithm")
	}
}

func TestParseKeyLine_BadBase64(t *testing.T) {
	t.Parallel()
	_, err := parseKeyLine("ed25519 not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for bad base64")
	}
}

func TestParseKeyLine_WrongKeyLength(t *testing.T) {
	t.Parallel()
	// 10 bytes is wrong for Ed25519 (needs 32).
	short := base64.StdEncoding.EncodeToString(make([]byte, 10))
	_, err := parseKeyLine("ed25519 " + short)
	if err == nil {
		t.Fatal("expected error for wrong key length")
	}
	if !strings.Contains(err.Error(), "invalid key length") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestParseKeyLine_TooFewFields(t *testing.T) {
	t.Parallel()
	_, err := parseKeyLine("ed25519")
	if err == nil {
		t.Fatal("expected error for too few fields")
	}
}

// ---- parseAuthorizedKeys / KeyRegistry tests -----------------------------

func TestParseAuthorizedKeys_CommentsAndBlanks(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	content := "# This is a comment\n\ned25519 " + b64 + " mykey\n# another comment\n"
	path := writeKeyFile(t, content, 0o600)
	keys, err := parseAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
}

func TestParseAuthorizedKeys_MalformedLineSkipped(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	content := "notvalidatall\ned25519 " + b64 + " goodkey\n"
	path := writeKeyFile(t, content, 0o600)
	keys, err := parseAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key after skipping malformed line, got %d", len(keys))
	}
}

func TestParseAuthorizedKeys_DuplicateKeyID(t *testing.T) {
	t.Parallel()
	_, b64 := genPubKeyB64(t)
	// Same key twice; second line should be silently ignored.
	content := "ed25519 " + b64 + " first\ned25519 " + b64 + " second\n"
	path := writeKeyFile(t, content, 0o600)
	keys, err := parseAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key (duplicate dropped), got %d", len(keys))
	}
	// First occurrence wins.
	for _, k := range keys {
		if k.Comment != "first" {
			t.Errorf("expected first comment to win, got %q", k.Comment)
		}
	}
}

func TestParseAuthorizedKeys_MultipleKeys(t *testing.T) {
	t.Parallel()
	_, b64a := genPubKeyB64(t)
	_, b64b := genPubKeyB64(t)
	content := "ed25519 " + b64a + " keya\ned25519 " + b64b + " keyb\n"
	path := writeKeyFile(t, content, 0o600)
	keys, err := parseAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestParseAuthorizedKeys_WorldReadableRefused(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}
	_, b64 := genPubKeyB64(t)
	path := writeKeyFile(t, "ed25519 "+b64+" k\n", 0o644) // world-readable
	_, err := parseAuthorizedKeys(path)
	if err == nil {
		t.Fatal("expected error for world-readable file")
	}
	if !strings.Contains(err.Error(), "world-readable") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestParseAuthorizedKeys_GroupReadableAccepted(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}
	_, b64 := genPubKeyB64(t)
	// 0640 = owner rw, group r, world none — should be accepted.
	path := writeKeyFile(t, "ed25519 "+b64+" k\n", 0o640)
	_, err := parseAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("unexpected error for 0640 file: %v", err)
	}
}

// TestKeyRegistry_SIGHUPReload verifies that Load() adds/removes keys
// correctly on a second call (simulating SIGHUP).
func TestKeyRegistry_SIGHUPReload(t *testing.T) {
	t.Parallel()
	pub1, b64a := genPubKeyB64(t)
	_, b64b := genPubKeyB64(t)

	// Initial file: one key.
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	write("ed25519 " + b64a + " keya\n")
	r := NewKeyRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("expected 1 key after first load, got %d", r.Len())
	}

	// Reload with a different key.
	write("ed25519 " + b64b + " keyb\n")
	if err := r.Load(); err != nil {
		t.Fatalf("Load (reload): %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("expected 1 key after reload, got %d", r.Len())
	}
	keyID1 := deriveKeyID(pub1)
	if _, ok := r.LookupByID(keyID1); ok {
		t.Error("old key should be gone after reload")
	}
}

func TestKeyRegistry_EmptyPath(t *testing.T) {
	t.Parallel()
	r := NewKeyRegistry("")
	if err := r.Load(); err != nil {
		t.Fatalf("Load with empty path should be no-op, got: %v", err)
	}
	if r.Len() != 0 {
		t.Errorf("expected 0 keys for empty path")
	}
}
