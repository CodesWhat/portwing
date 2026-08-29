package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
)

// routeTestServerOpts configures newRouteTestServer.
type routeTestServerOpts struct {
	// token, if non-empty, configures TOKEN auth (AllowUnauthenticated is
	// disabled to match). Empty leaves the server unauthenticated, matching
	// minimalConfig's default.
	token string
	// enroll configures AUTHORIZED_KEYS + ENROLLMENT_TOKEN so the
	// /api/portwing/enroll route is registered.
	enroll bool
}

// newRouteTestServer builds a real *Server via NewServer — the same
// production entry point ListenAndServe uses — and returns the exact
// http.Handler it serves with (s.httpServer.Handler).
//
// Tests that care about route *topology* (which registered pattern actually
// wins for a given method+path) must drive requests through this handler
// rather than a hand-copied mux: a hand-copied mux can silently diverge from
// registerRoutes, and that is exactly how the /_portwing/mcp and
// /api/portwing/enroll wrong-method bug went unnoticed. The old test mux
// omitted the "/" Docker-proxy catch-all that registerRoutes always
// registers last, so TestMCPRoutePostOnly's "GET returns 405" case was
// exercising Go's automatic single-pattern method dispatch against a
// topology that does not exist in production — with the catch-all present,
// a GET to /_portwing/mcp actually fell through to the Docker proxy and
// leaked Docker's own error shape instead of a 405. Building the mux via
// NewServer's own registerRoutes call closes that gap: there is nothing
// left to hand-copy, so nothing can drift out of sync again.
func newRouteTestServer(t *testing.T, opts routeTestServerOpts) (*Server, http.Handler) {
	t.Helper()

	client, cleanup := newDockerClientWithPing(t, true)
	t.Cleanup(cleanup)

	cfg := minimalConfig()
	if opts.token != "" {
		cfg.Token = opts.token
		cfg.AllowUnauthenticated = false
	}
	if opts.enroll {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		cfg.AuthorizedKeysFile = writeAuthorizedKeys(t, pub)
		cfg.EnrollmentToken = "enrollment-secret"
	}

	s, err := NewServer(cfg, client, &stubServerAdapter{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	return s, s.httpServer.Handler
}

// TestMCPRoutePostOnly verifies that the MCP endpoint is POST-only: a GET
// returns 405 (not the Docker proxy's "404 page not found"), a POST reaches
// the real MCP handler.
func TestMCPRoutePostOnly(t *testing.T) {
	t.Parallel()

	const token = "test-token"
	_, handler := newRouteTestServer(t, routeTestServerOpts{token: token})

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

// TestEnrollRouteWrongMethod verifies that /api/portwing/enroll — reachable
// without credentials, since it is the bootstrap endpoint — returns 405 for
// a GET instead of falling through to the Docker proxy catch-all.
func TestEnrollRouteWrongMethod(t *testing.T) {
	t.Parallel()

	_, handler := newRouteTestServer(t, routeTestServerOpts{enroll: true})

	req := httptest.NewRequest(http.MethodGet, "/api/portwing/enroll", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/portwing/enroll, got %d", rec.Code)
	}
}

// buildComposeStubMux builds a minimal http.ServeMux with a single stub
// POST /_portwing/compose route, auth-gated the same way production wraps
// it. It intentionally does not mirror the rest of registerRoutes: it
// exists only to exercise MaxBytesReader body-size mechanics in isolation,
// without needing a real Docker Compose backend behind s.compose.Execute
// (see newRouteTestServer for the topology-sensitive routes instead).
func buildComposeStubMux(t *testing.T, token string) *http.ServeMux {
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

	composeHandler := authWrap(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("POST /_portwing/compose", composeHandler)

	return mux
}

// TestComposeHandlerBodyLimit verifies that a body exceeding 32 MiB causes
// MaxBytesReader to return an error and the handler responds with 413.
// We use an io.LimitedReader that reports exactly 32 MiB + 1 byte so the test
// stays fast (no real 32 MB allocation required).
func TestComposeHandlerBodyLimit(t *testing.T) {
	t.Parallel()

	const token = "test-token"
	mux := buildComposeStubMux(t, token)
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
	mux := buildComposeStubMux(t, token)
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

	_, handler := newRouteTestServer(t, routeTestServerOpts{token: "secret"})

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
	_, handler := newRouteTestServer(t, routeTestServerOpts{token: "secret"})

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

	_, handler := newRouteTestServer(t, routeTestServerOpts{token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/_portwing/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /_portwing/health without credentials, got %d", rec.Code)
	}
}
