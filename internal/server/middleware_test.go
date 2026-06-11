package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/codeswhat/lookout/internal/audit"
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

// helpers

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
