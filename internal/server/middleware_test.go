package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/lookout/internal/audit"
	"github.com/codeswhat/lookout/internal/auth"
)

// noAudit returns a disabled audit.Logger for tests that only care about HTTP
// status codes and don't need to verify audit output.
func noAudit(t *testing.T) *audit.Logger {
	t.Helper()
	l, _, err := audit.New("")
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	return l
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// TestAuthMiddlewareRawTokenAccept verifies the raw-token path accepts a correct token.
func TestAuthMiddlewareRawTokenAccept(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Lookout-Token", "correct")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestAuthMiddlewareRawTokenReject verifies the raw-token path rejects a wrong token.
func TestAuthMiddlewareRawTokenReject(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Lookout-Token", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestAuthMiddlewareArgon2idAccept verifies that a correct token is accepted
// when the verifier uses Argon2id.
func TestAuthMiddlewareArgon2idAccept(t *testing.T) {
	t.Parallel()

	const token = "argon2correct"
	phc, err := HashToken(token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}

	params, err := ParsePHC(phc)
	if err != nil {
		t.Fatalf("ParsePHC: %v", err)
	}

	rl := NewRateLimiter()
	verifier := newArgon2Verifier(params)
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Lookout-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestAuthMiddlewareArgon2idReject verifies that a wrong token is rejected
// when the verifier uses Argon2id.
func TestAuthMiddlewareArgon2idReject(t *testing.T) {
	t.Parallel()

	const token = "argon2secret"
	phc, err := HashToken(token)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}

	params, err := ParsePHC(phc)
	if err != nil {
		t.Fatalf("ParsePHC: %v", err)
	}

	rl := NewRateLimiter()
	verifier := newArgon2Verifier(params)
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Lookout-Token", "wrongtoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestAuthMiddlewareNilVerifier verifies that no-auth mode passes all requests.
func TestAuthMiddlewareNilVerifier(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	h := rl.AuthMiddleware(nil, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for no-auth mode, got %d", rec.Code)
	}
}

// TestAuthMiddlewareBearerHeader verifies that Authorization: Bearer is
// accepted as the primary token header.
func TestAuthMiddlewareBearerHeader(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "bearer-secret"}
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bearer-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for Authorization: Bearer, got %d", rec.Code)
	}
}

// TestClientIPIgnoresForwardedForByDefault verifies that with no trusted
// proxies configured, spoofed forwarding headers do not change the
// rate-limiting identity.
func TestClientIPIgnoresForwardedForByDefault(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:50000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Real-IP", "203.0.113.8")

	if got := rl.clientIP(req); got != "192.0.2.1" {
		t.Fatalf("expected remote addr 192.0.2.1, got %q", got)
	}
}

// TestClientIPTrustedProxy verifies that forwarding headers are honored when
// the direct peer is a trusted proxy, walking the chain right to left past
// other trusted hops.
func TestClientIPTrustedProxy(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	nets, err := ParseTrustedProxies([]string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	rl.SetTrustedProxies(nets)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:50000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 192.0.2.5")

	if got := rl.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("expected forwarded client 203.0.113.7, got %q", got)
	}
}

// TestRateLimiterNotBypassedBySpoofedXFF verifies that rotating
// X-Forwarded-For values cannot reset the failure counter for the real peer.
func TestRateLimiterNotBypassedBySpoofedXFF(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:50000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		req.Header.Set("X-Lookout-Token", "wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:50000"
	req.Header.Set("X-Forwarded-For", "203.0.113.250")
	req.Header.Set("X-Lookout-Token", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 10 failures from same peer, got %d", rec.Code)
	}
}

// TestAuthMiddlewareFallbackHeader verifies that X-Dd-Agent-Secret is accepted
// when X-Lookout-Token is absent.
func TestAuthMiddlewareFallbackHeader(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "legacytoken"}
	h := rl.AuthMiddleware(verifier, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dd-Agent-Secret", "legacytoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for X-Dd-Agent-Secret header, got %d", rec.Code)
	}
}

// TestAuditMiddlewareEmitsAuthFailure verifies that the middleware emits an
// auth_failure audit event when a wrong token is presented.
func TestAuditMiddlewareEmitsAuthFailure(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, close, err := audit.New(tmp)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(close)

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, l, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/_lookout/info", nil)
	req.Header.Set("X-Lookout-Token", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	data, _ := readFile(tmp)
	if !contains(data, audit.EventAuthFailure) {
		t.Errorf("expected %q in audit log, got: %s", audit.EventAuthFailure, data)
	}
}

// TestAuditMiddlewareEmitsRateLimited verifies that a rate-limited request
// produces a rate_limited audit event.
func TestAuditMiddlewareEmitsRateLimited(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, close, err := audit.New(tmp)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(close)

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, l, http.HandlerFunc(okHandler))

	// Exhaust the 10-failure limit.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Lookout-Token", "bad")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}

	// This request should now be rate-limited.
	req := httptest.NewRequest(http.MethodGet, "/_lookout/compose", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Lookout-Token", "bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	data, _ := readFile(tmp)
	if !contains(data, audit.EventRateLimited) {
		t.Errorf("expected %q in audit log, got: %s", audit.EventRateLimited, data)
	}
}

// TestAuditMiddlewareEmitsAPIRequest verifies that an allowed request emits
// an api_request event.
func TestAuditMiddlewareEmitsAPIRequest(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir() + "/audit.log"
	l, close, err := audit.New(tmp)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(close)

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, l, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/_lookout/info", nil)
	req.Header.Set("X-Lookout-Token", "correct")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data, _ := readFile(tmp)
	if !contains(data, audit.EventAPIRequest) {
		t.Errorf("expected %q in audit log, got: %s", audit.EventAPIRequest, data)
	}
}

// TestAuthMiddlewarePreservesStreamingInterfaces verifies that the
// statusRecorder wrapper still exposes http.Flusher and http.Hijacker to
// downstream handlers — SSE streaming and Docker exec/attach hijacking
// depend on them.
func TestAuthMiddlewarePreservesStreamingInterfaces(t *testing.T) {
	t.Parallel()

	var sawFlusher, sawHijacker bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawFlusher = w.(http.Flusher)
		_, sawHijacker = w.(http.Hijacker)
		w.WriteHeader(http.StatusOK)
	})

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, noAudit(t), inner)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("X-Lookout-Token", "correct")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !sawFlusher {
		t.Error("http.Flusher lost through AuthMiddleware")
	}
	if !sawHijacker {
		t.Error("http.Hijacker lost through AuthMiddleware")
	}

	// No-auth mode wraps with the same recorder; verify that path too.
	sawFlusher, sawHijacker = false, false
	h = rl.AuthMiddleware(nil, noAudit(t), inner)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil))

	if !sawFlusher {
		t.Error("http.Flusher lost through AuthMiddleware (no-auth mode)")
	}
	if !sawHijacker {
		t.Error("http.Hijacker lost through AuthMiddleware (no-auth mode)")
	}
}

// ---- Ed25519 middleware integration tests ---------------------------------

// setupEd25519 creates an Ed25519 keypair, writes the authorized_keys file,
// returns a loaded Ed25519Config plus the private key.
func setupEd25519(t *testing.T) (Ed25519Config, ed25519.PrivateKey) {
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
	reg := auth.NewKeyRegistry(path)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := Ed25519Config{
		Registry:       reg,
		Nonces:         auth.NewNonceLRU(1000, 60),
		MaxSkewSeconds: 60,
	}
	return cfg, priv
}

// signEd25519Request attaches Ed25519 headers to req.
func signEd25519Request(t *testing.T, req *http.Request, body []byte, priv ed25519.PrivateKey, tsUnix int64, nonce string) {
	t.Helper()
	pub := priv.Public().(ed25519.PublicKey)
	bodyHash := auth.BodyHashHex(body)
	msg := auth.CanonicalMessage(req.Method, req.URL.Path, bodyHash, tsUnix, nonce)
	sig := ed25519.Sign(priv, msg)

	h := sha256.Sum256(pub)
	keyID := hex.EncodeToString(h[:8])

	req.Header.Set(auth.HeaderKeyID, keyID)
	req.Header.Set(auth.HeaderTimestamp, strconv.FormatInt(tsUnix, 10))
	req.Header.Set(auth.HeaderNonce, nonce)
	req.Header.Set(auth.HeaderSignature, base64.RawURLEncoding.EncodeToString(sig))
}

func freshNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

// TestEd25519MiddlewareAccept verifies a correctly signed request is accepted.
func TestEd25519MiddlewareAccept(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/_lookout/info", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestEd25519MiddlewareTokenFallback verifies that if no Ed25519 config is set,
// the token verifier is still used.
func TestEd25519MiddlewareTokenFallback(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "mysecret"}
	h := rl.AuthMiddlewareWithEd25519(verifier, Ed25519Config{}, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	req.Header.Set("X-Lookout-Token", "mysecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for token fallback, got %d", rec.Code)
	}
}

// TestEd25519MiddlewareBothConfigured verifies that when both Ed25519 and token
// are configured, a signed request uses the Ed25519 path (not the token path).
func TestEd25519MiddlewareBothConfigured(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "mysecret"}
	h := rl.AuthMiddlewareWithEd25519(verifier, ed, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix(), freshNonce(t))
	// Do not set the token — should succeed via Ed25519 alone.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 via Ed25519 when both configured, got %d", rec.Code)
	}
}

// TestEd25519MiddlewareBadTimestamp verifies that a skewed timestamp returns
// 401 with X-Lookout-Reason: timestamp-skew.
func TestEd25519MiddlewareBadTimestamp(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	signEd25519Request(t, req, nil, priv, time.Now().Unix()-200, freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if got := rec.Header().Get(auth.HeaderReason); got != "timestamp-skew" {
		t.Errorf("X-Lookout-Reason: got %q want %q", got, "timestamp-skew")
	}
}

// TestEd25519MiddlewareReplayedNonce verifies replay protection.
func TestEd25519MiddlewareReplayedNonce(t *testing.T) {
	t.Parallel()
	ed, priv := setupEd25519(t)
	rl := NewRateLimiter()
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), http.HandlerFunc(okHandler))

	nonce := freshNonce(t)
	tsUnix := time.Now().Unix()

	req1 := httptest.NewRequest(http.MethodGet, "/path", nil)
	signEd25519Request(t, req1, nil, priv, tsUnix, nonce)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/path", nil)
	signEd25519Request(t, req2, nil, priv, tsUnix, nonce)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request: expected 401, got %d", rec2.Code)
	}
	if got := rec2.Header().Get(auth.HeaderReason); got != "replay" {
		t.Errorf("X-Lookout-Reason: got %q want %q", got, "replay")
	}
}

// TestEd25519MiddlewareUnknownKey verifies that an unknown key returns 401.
func TestEd25519MiddlewareUnknownKey(t *testing.T) {
	t.Parallel()
	ed, _ := setupEd25519(t)
	rl := NewRateLimiter()
	h := rl.AuthMiddlewareWithEd25519(nil, ed, noAudit(t), http.HandlerFunc(okHandler))

	// Generate a key that is NOT in the registry.
	_, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	signEd25519Request(t, req, nil, priv2, time.Now().Unix(), freshNonce(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown key, got %d", rec.Code)
	}
	if got := rec.Header().Get(auth.HeaderReason); got != "unknown-key" {
		t.Errorf("X-Lookout-Reason: got %q want %q", got, "unknown-key")
	}
}

// TestRateLimitOnlyRecordsFailures verifies that the enrollment guard counts
// downstream 401s toward the rate limit, so the enrollment token cannot be
// brute-forced past the limiter.
func TestRateLimitOnlyRecordsFailures(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	deny := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	h := rl.rateLimitOnly(deny)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/lookout/enroll", nil)
		req.RemoteAddr = "192.0.2.9:40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/lookout/enroll", nil)
	req.RemoteAddr = "192.0.2.9:40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 10 enrollment failures, got %d", rec.Code)
	}
}

// helpers

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
