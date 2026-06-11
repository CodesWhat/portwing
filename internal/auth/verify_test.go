package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testSetup generates an Ed25519 keypair, writes the public key to an
// authorized_keys file, returns the loaded registry, a nonce LRU, the public
// key, and the private key.
func testSetup(t *testing.T) (
	*KeyRegistry, *NonceLRU, ed25519.PublicKey, ed25519.PrivateKey,
) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	b64 := base64.StdEncoding.EncodeToString(pub)
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(path, []byte("ed25519 "+b64+" testkey\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewKeyRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	lru := NewNonceLRU(1000, 60)
	return r, lru, pub, priv
}

// signRequest signs a request and sets Ed25519 headers on it.
func signRequest(t *testing.T, r *http.Request, body []byte, priv ed25519.PrivateKey, pub ed25519.PublicKey, tsUnix int64, nonce string) {
	t.Helper()
	bodyHash := BodyHashHex(body)
	msg := CanonicalMessage(r.Method, r.URL.Path, bodyHash, tsUnix, nonce)
	sig := ed25519.Sign(priv, msg)

	keyIDBytes := sha256.Sum256(pub)
	keyID := hex.EncodeToString(keyIDBytes[:8])

	r.Header.Set(HeaderKeyID, keyID)
	r.Header.Set(HeaderTimestamp, strconv.FormatInt(tsUnix, 10))
	r.Header.Set(HeaderNonce, nonce)
	r.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(sig))
}

// randomNonce generates a fresh 32-hex-char nonce.
func randomNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

// ---- CanonicalMessage tests ------------------------------------------------

func TestCanonicalMessage_EmptyBody(t *testing.T) {
	t.Parallel()
	msg := CanonicalMessage("GET", "/api/test", emptyBodyHash, 1749600000, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6")
	want := "GET\n/api/test\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n1749600000\na1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"
	if string(msg) != want {
		t.Errorf("canonical message mismatch\ngot:  %q\nwant: %q", string(msg), want)
	}
}

func TestBodyHashHex_EmptyBody(t *testing.T) {
	t.Parallel()
	h := BodyHashHex(nil)
	if h != emptyBodyHash {
		t.Errorf("empty body hash: got %q want %q", h, emptyBodyHash)
	}
	h2 := BodyHashHex([]byte{})
	if h2 != emptyBodyHash {
		t.Errorf("empty slice body hash: got %q want %q", h2, emptyBodyHash)
	}
}

func TestBodyHashHex_NonEmptyBody(t *testing.T) {
	t.Parallel()
	body := []byte(`{"test":true}`)
	h := BodyHashHex(body)
	// Verify it matches crypto/sha256 directly.
	raw := sha256.Sum256(body)
	want := hex.EncodeToString(raw[:])
	if h != want {
		t.Errorf("body hash: got %q want %q", h, want)
	}
}

// ---- VerifyRequest tests ---------------------------------------------------

func TestVerifyRequest_HappyPath(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()
	signRequest(t, req, nil, priv, pub, tsUnix, nonce)

	keyID, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if keyID == "" {
		t.Error("expected non-empty key ID")
	}
}

func TestVerifyRequest_BadSignature(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()
	signRequest(t, req, nil, priv, pub, tsUnix, nonce)

	// Corrupt the signature.
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(make([]byte, 64)))

	_, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != ErrBadSignature {
		t.Errorf("expected ErrBadSignature, got: %v", err)
	}
}

func TestVerifyRequest_SkewedTimestamp(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	nonce := randomNonce(t)
	// 120 seconds in the past — exceeds 60s window.
	tsUnix := time.Now().Unix() - 120
	signRequest(t, req, nil, priv, pub, tsUnix, nonce)

	_, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != ErrTimestampSkew {
		t.Errorf("expected ErrTimestampSkew, got: %v", err)
	}
}

func TestVerifyRequest_ReplayedNonce(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()

	// First request succeeds.
	req1 := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	signRequest(t, req1, nil, priv, pub, tsUnix, nonce)
	if _, err := VerifyRequest(req1, nil, reg, lru, 60); err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Second request with same nonce must be rejected as replay.
	req2 := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	signRequest(t, req2, nil, priv, pub, tsUnix, nonce)
	_, err := VerifyRequest(req2, nil, reg, lru, 60)
	if err != ErrNonceReplay {
		t.Errorf("expected ErrNonceReplay, got: %v", err)
	}
}

func TestVerifyRequest_UnknownKey(t *testing.T) {
	t.Parallel()
	reg, lru, _, _ := testSetup(t)

	// Generate a completely different keypair.
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()
	signRequest(t, req, nil, priv2, pub2, tsUnix, nonce)

	_, err = VerifyRequest(req, nil, reg, lru, 60)
	if err != ErrUnknownKey {
		t.Errorf("expected ErrUnknownKey, got: %v", err)
	}
}

func TestVerifyRequest_MissingHeaders(t *testing.T) {
	t.Parallel()
	reg, lru, _, _ := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/api/lookout/health", nil)
	// No signature headers at all.
	_, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != ErrMissingHeaders {
		t.Errorf("expected ErrMissingHeaders, got: %v", err)
	}
}

func TestVerifyRequest_MissingIndividualHeaders(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()

	// Build a base request.
	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/path", nil)
		signRequest(t, req, nil, priv, pub, tsUnix, nonce)
		return req
	}

	tests := []struct {
		name   string
		remove string
	}{
		{"no key-id", HeaderKeyID},
		{"no timestamp", HeaderTimestamp},
		{"no nonce", HeaderNonce},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := makeReq()
			req.Header.Del(tc.remove)
			_, err := VerifyRequest(req, nil, reg, lru, 60)
			if err != ErrMissingHeaders {
				t.Errorf("expected ErrMissingHeaders, got: %v", err)
			}
		})
	}
}

func TestVerifyRequest_InvalidNonceLength(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	tsUnix := time.Now().Unix()
	signRequest(t, req, nil, priv, pub, tsUnix, "short") // only 5 chars
	req.Header.Set(HeaderNonce, "short")

	_, err := VerifyRequest(req, nil, reg, lru, 60)
	if err != ErrInvalidNonce {
		t.Errorf("expected ErrInvalidNonce, got: %v", err)
	}
}

func TestVerifyRequest_WithBody(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	body := []byte(`{"key":"value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/lookout/containers", nil)
	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()
	signRequest(t, req, body, priv, pub, tsUnix, nonce)

	keyID, err := VerifyRequest(req, body, reg, lru, 60)
	if err != nil {
		t.Fatalf("expected success with body, got: %v", err)
	}
	if keyID == "" {
		t.Error("expected non-empty key ID")
	}
}

func TestVerifyRequest_BodyMismatch(t *testing.T) {
	t.Parallel()
	reg, lru, pub, priv := testSetup(t)

	body := []byte(`{"key":"value"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/lookout/containers", nil)
	nonce := randomNonce(t)
	tsUnix := time.Now().Unix()
	signRequest(t, req, body, priv, pub, tsUnix, nonce)

	// Verify with a different body — signature should not match.
	_, err := VerifyRequest(req, []byte(`{"key":"other"}`), reg, lru, 60)
	if err != ErrBadSignature {
		t.Errorf("expected ErrBadSignature for body mismatch, got: %v", err)
	}
}

func TestReasonFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err    error
		reason string
	}{
		{ErrTimestampSkew, "timestamp-skew"},
		{ErrNonceReplay, "replay"},
		{ErrUnknownKey, "unknown-key"},
		{ErrBadSignature, "invalid-signature"},
		{ErrMissingHeaders, "invalid-signature"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			if got := ReasonFor(tc.err); got != tc.reason {
				t.Errorf("ReasonFor(%v) = %q, want %q", tc.err, got, tc.reason)
			}
		})
	}
}

// TestVerifyRequest_ConcurrentReplay fires N copies of the same signed
// request concurrently and asserts exactly one is accepted. This guards the
// atomic check-and-set in the nonce LRU: a Seen()-then-Add() pair that is not
// atomic would let two racing copies both pass.
func TestVerifyRequest_ConcurrentReplay(t *testing.T) {
	t.Parallel()

	registry, lru, pub, priv := testSetup(t)
	tsUnix := time.Now().Unix()
	nonce := randomNonce(t)

	const n = 16
	var wg sync.WaitGroup
	var accepted atomic.Int32

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/containers", nil)
			signRequest(t, req, nil, priv, pub, tsUnix, nonce)
			if _, err := VerifyRequest(req, nil, registry, lru, 60); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := accepted.Load(); got != 1 {
		t.Fatalf("expected exactly 1 accepted request, got %d", got)
	}
}
