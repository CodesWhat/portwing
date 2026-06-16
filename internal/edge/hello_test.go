package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

func newHello() protocol.HelloMessage {
	return protocol.HelloMessage{
		Version:   protocol.AgentVersion,
		Protocol:  protocol.ProtocolString,
		AgentID:   "agent-1",
		AgentName: "test-agent",
	}
}

// setTokenHash derives the SHA-256 hex of the configured token; an empty token
// leaves the field blank.
func TestSetTokenHash(t *testing.T) {
	t.Parallel()

	c := &Client{cfg: &config.Config{Token: "s3cr3t"}}
	var hello = newHello()
	c.setTokenHash(&hello)

	want := fmt.Sprintf("%x", sha256.Sum256([]byte("s3cr3t")))
	if hello.TokenHash != want {
		t.Errorf("TokenHash = %q, want %q", hello.TokenHash, want)
	}

	c.cfg.Token = ""
	hello = newHello()
	c.setTokenHash(&hello)
	if hello.TokenHash != "" {
		t.Errorf("TokenHash = %q for empty token, want empty", hello.TokenHash)
	}
}

// signHello must populate the Ed25519 fields with a signature that verifies
// against the canonical WebSocket-upgrade message, and must clear TokenHash so
// the two auth modes never coexist.
func TestSignHelloProducesVerifiableSignature(t *testing.T) {
	t.Parallel()

	privPEM, _, err := auth.GenerateKeyPair("test")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	priv, err := auth.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}
	pub := priv.Public().(ed25519.PublicKey)

	keyPath := filepath.Join(t.TempDir(), "agent.key")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	c := &Client{cfg: &config.Config{PrivateKeyFile: keyPath, Token: "ignored-when-signing"}}
	hello := newHello()
	hello.TokenHash = "should-be-cleared"

	before := time.Now().Unix()
	if err := c.signHello(context.Background(), &hello); err != nil {
		t.Fatalf("signHello: %v", err)
	}
	after := time.Now().Unix()

	if hello.PubKeyID != auth.KeyIDForPublicKey(pub) {
		t.Errorf("PubKeyID = %q, want %q", hello.PubKeyID, auth.KeyIDForPublicKey(pub))
	}
	if hello.TokenHash != "" {
		t.Errorf("TokenHash = %q, want cleared when signing", hello.TokenHash)
	}
	if hello.Nonce == "" {
		t.Error("Nonce was not set")
	}
	if hello.Timestamp < before || hello.Timestamp > after {
		t.Errorf("Timestamp = %d, want within [%d,%d]", hello.Timestamp, before, after)
	}

	sig, err := base64.RawURLEncoding.DecodeString(hello.Signature)
	if err != nil {
		t.Fatalf("signature not base64url: %v", err)
	}
	canonical := auth.CanonicalMessage("GET", "/api/portwing/ws", auth.BodyHashHex(nil), hello.Timestamp, hello.Nonce)
	if !ed25519.Verify(pub, canonical, sig) {
		t.Error("signature did not verify against the canonical upgrade message")
	}
}

// A missing key file is a hard error the caller falls back from (to token auth).
func TestSignHelloFailsOnMissingKey(t *testing.T) {
	t.Parallel()

	c := &Client{cfg: &config.Config{PrivateKeyFile: filepath.Join(t.TempDir(), "absent.key")}}
	hello := newHello()
	if err := c.signHello(context.Background(), &hello); err == nil {
		t.Error("signHello succeeded with a missing key file, want error")
	}
}
