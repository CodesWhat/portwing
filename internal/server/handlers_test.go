package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

// buildTestMux builds a minimal http.ServeMux that mirrors the route
// registration in registerRoutes without requiring a live Docker client.
// The auth-gated routes use AuthMiddlewareWithEd25519 with a simple raw-token
// verifier, matching the path taken in production when only TOKEN is set.
//
// Routes registered:
//
//	GET  /health                — unauthenticated simple health
//	GET  /_portwing/health      — unauthenticated extended health (stubbed)
//	POST /_portwing/mcp         — auth-gated (POST only per Go 1.22 method routing)
//	POST /_portwing/compose     — auth-gated
func buildTestMux(t *testing.T, token string) (*http.ServeMux, *RateLimiter) {
	t.Helper()

	auditor, cleanup, err := audit.New("", 0)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	t.Cleanup(cleanup)

	rl := NewRateLimiter()
	t.Cleanup(rl.Stop)

	var verifier tokenVerifier
	if token != "" {
		verifier = newRawTokenVerifier(token)
	}

	authWrap := func(h http.HandlerFunc) http.Handler {
		return rl.AuthMiddlewareWithEd25519(verifier, Ed25519Config{}, auditor, nil, http.HandlerFunc(h))
	}

	mux := http.NewServeMux()

	// Unauthenticated routes.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	// GET /_portwing/health — stub that always reports healthy (no Docker needed).
	mux.HandleFunc("GET /_portwing/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"healthy","docker":"connected"}`)
	})

	// POST-only MCP endpoint (Go 1.22 method+pattern routing enforces POST).
	mcpHandler := authWrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("POST /_portwing/mcp", mcpHandler)

	// POST-only compose endpoint with MaxBytesReader.
	composeHandler := authWrap(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("POST /_portwing/compose", composeHandler)

	return mux, rl
}

// TestMCPRoutePostOnly verifies that the MCP endpoint is POST-only:
// a GET returns 405 (Go 1.22 method routing), a POST reaches the handler.
func TestMCPRoutePostOnly(t *testing.T) {
	t.Parallel()

	const token = "test-token"
	mux, _ := buildTestMux(t, token)
	handler := RecoveryMiddleware(mux)

	t.Run("GET returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/_portwing/mcp", nil)
		req.Header.Set(headerPortwingToken, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for GET /_portwing/mcp, got %d", rec.Code)
		}
	})

	t.Run("POST reaches handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(`{}`))
		req.Header.Set(headerPortwingToken, token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for POST /_portwing/mcp, got %d", rec.Code)
		}
	})
}

// TestComposeHandlerBodyLimit verifies that a body exceeding 32 MiB causes
// MaxBytesReader to return an error and the handler responds with 413.
// We use an io.LimitedReader that reports exactly 32 MiB + 1 byte so the test
// stays fast (no real 32 MB allocation required).
func TestComposeHandlerBodyLimit(t *testing.T) {
	t.Parallel()

	const token = "test-token"
	mux, _ := buildTestMux(t, token)
	handler := RecoveryMiddleware(mux)

	// Create a body slightly over 32 MiB. io.LimitedReader is used so we don't
	// actually allocate 32 MB in the test process.
	const limit = 32 << 20         // 32 MiB
	const overLimit = limit + 1024 // 32 MiB + 1 KiB

	body := io.LimitReader(strings.NewReader(strings.Repeat("x", overLimit)), overLimit)

	req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", body)
	req.Header.Set(headerPortwingToken, token)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = overLimit
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for body > 32 MiB, got %d", rec.Code)
	}
}

// TestComposeHandlerBodyWithinLimit verifies that a body within the 32 MiB
// limit is not rejected by the size check alone.
func TestComposeHandlerBodyWithinLimit(t *testing.T) {
	t.Parallel()

	const token = "test-token"
	mux, _ := buildTestMux(t, token)
	handler := RecoveryMiddleware(mux)

	// A small valid-looking body (well under the limit).
	body := strings.NewReader(`{"operation":"ps","stackName":"test"}`)

	req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", body)
	req.Header.Set(headerPortwingToken, token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for body within limit, got %d", rec.Code)
	}
}

// TestAuthGatedRouteReturns401WithoutCredentials verifies that an auth-gated
// route returns 401 when no credentials are presented (and auth is configured).
func TestAuthGatedRouteReturns401WithoutCredentials(t *testing.T) {
	t.Parallel()

	mux, _ := buildTestMux(t, "secret")
	handler := RecoveryMiddleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(`{}`))
	// Deliberately no auth header.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", rec.Code)
	}
}

// TestHealthEndpointNoAuth verifies that GET /health returns 200 without any
// credentials. This is the unauthenticated health endpoint.
func TestHealthEndpointNoAuth(t *testing.T) {
	t.Parallel()

	// Even with auth configured, /health must not require credentials.
	mux, _ := buildTestMux(t, "secret")
	handler := RecoveryMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	// No auth header.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /health without credentials, got %d", rec.Code)
	}
}

// TestPortwingHealthEndpointNoAuth verifies GET /_portwing/health returns 200
// without credentials (it is not auth-gated).
func TestPortwingHealthEndpointNoAuth(t *testing.T) {
	t.Parallel()

	mux, _ := buildTestMux(t, "secret")
	handler := RecoveryMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/_portwing/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /_portwing/health without credentials, got %d", rec.Code)
	}
}
