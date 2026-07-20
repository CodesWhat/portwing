package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateKeyPair_RoundTrip(t *testing.T) {
	t.Parallel()
	privPEM, authKeyLine, err := GenerateKeyPair("testkey")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Verify private key PEM is valid PKCS#8.
	priv, err := ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}

	// Verify authorized_keys line format.
	parts := strings.Fields(authKeyLine)
	if len(parts) != 3 {
		t.Fatalf("expected 3 fields in authorized_keys line, got %d: %q", len(parts), authKeyLine)
	}
	if parts[0] != "ed25519" {
		t.Errorf("expected algorithm ed25519, got %q", parts[0])
	}
	if parts[2] != "testkey" {
		t.Errorf("expected comment testkey, got %q", parts[2])
	}

	// Verify the public key in the line matches the private key's public key.
	rawPub, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("base64 decode pubkey: %v", err)
	}
	if len(rawPub) != 32 {
		t.Fatalf("expected 32-byte public key, got %d", len(rawPub))
	}

	pub := priv.Public().(ed25519.PublicKey)
	if string(pub) != string(rawPub) {
		t.Error("public key in authorized_keys line does not match private key's public key")
	}

	// Verify sign + verify round-trip.
	msg := []byte("canonical message for testing")
	sig := ed25519.Sign(priv, msg)
	if !ed25519.Verify(ed25519.PublicKey(rawPub), msg, sig) {
		t.Error("signature verification failed for generated key pair")
	}
}

func TestGenerateKeyPair_NoComment(t *testing.T) {
	t.Parallel()
	_, authKeyLine, err := GenerateKeyPair("")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	parts := strings.Fields(authKeyLine)
	if len(parts) != 2 {
		t.Errorf("expected 2 fields for no-comment key, got %d: %q", len(parts), authKeyLine)
	}
}

func TestLoadPrivateKey_RoundTrip(t *testing.T) {
	t.Parallel()
	privPEM, _, err := GenerateKeyPair("roundtrip")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "private.pem")
	if err := os.WriteFile(path, privPEM, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	priv, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("expected %d-byte private key, got %d", ed25519.PrivateKeySize, len(priv))
	}
}

func TestLoadPrivateKey_NotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadPrivateKey("/nonexistent/path/private.pem")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadPrivateKey_GroupOrWorldWritableRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}
	privPEM, _, err := GenerateKeyPair("unsafe-mode")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o620, 0o602} {
		path := filepath.Join(t.TempDir(), "private.pem")
		if err := os.WriteFile(path, privPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPrivateKey(path); err == nil {
			t.Fatalf("mode %04o: expected writable private key rejection", mode)
		}
	}
}

func TestParsePrivateKeyPEM_NoPEMBlock(t *testing.T) {
	t.Parallel()
	_, err := ParsePrivateKeyPEM([]byte("not PEM"))
	if err == nil {
		t.Error("expected error for non-PEM data")
	}
	if !strings.Contains(err.Error(), "no PEM block") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePrivateKeyPEM_WrongType(t *testing.T) {
	t.Parallel()
	pemData := []byte("-----BEGIN CERTIFICATE-----\nYQ==\n-----END CERTIFICATE-----\n")
	_, err := ParsePrivateKeyPEM(pemData)
	if err == nil {
		t.Error("expected error for wrong PEM type")
	}
	if !strings.Contains(err.Error(), "expected PEM type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKeyIDForPublicKey_Deterministic(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := priv.Public().(ed25519.PublicKey)

	id1 := KeyIDForPublicKey(pub)
	id2 := KeyIDForPublicKey(pub)
	if id1 != id2 {
		t.Error("KeyIDForPublicKey is not deterministic")
	}
	if len(id1) != 16 {
		t.Errorf("expected 16-char key ID (8 bytes hex), got %d", len(id1))
	}
}

func TestNewNonce_Format(t *testing.T) {
	t.Parallel()
	n, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if len(n) != 32 {
		t.Errorf("expected 32-char nonce, got %d: %q", len(n), n)
	}
}

func TestNewNonce_Unique(t *testing.T) {
	t.Parallel()
	n1, _ := NewNonce()
	n2, _ := NewNonce()
	if n1 == n2 {
		t.Error("NewNonce generated identical nonces (collision)")
	}
}
