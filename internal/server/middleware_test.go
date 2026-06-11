package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// TestAuthMiddlewareRawTokenAccept verifies the raw-token path accepts a correct token.
func TestAuthMiddlewareRawTokenAccept(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "correct"}
	h := rl.AuthMiddleware(verifier, http.HandlerFunc(okHandler))

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
	h := rl.AuthMiddleware(verifier, http.HandlerFunc(okHandler))

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
	h := rl.AuthMiddleware(verifier, http.HandlerFunc(okHandler))

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
	h := rl.AuthMiddleware(verifier, http.HandlerFunc(okHandler))

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
	h := rl.AuthMiddleware(nil, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for no-auth mode, got %d", rec.Code)
	}
}

// TestAuthMiddlewareFallbackHeader verifies that X-Dd-Agent-Secret is accepted
// when X-Lookout-Token is absent.
func TestAuthMiddlewareFallbackHeader(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	verifier := &rawTokenVerifier{token: "legacytoken"}
	h := rl.AuthMiddleware(verifier, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dd-Agent-Secret", "legacytoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for X-Dd-Agent-Secret header, got %d", rec.Code)
	}
}
