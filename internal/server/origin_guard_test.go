package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/config"
)

// originFixture is a fully built Server (NewServer, so the real handler chain)
// in front of a daemon that counts every request it receives.
type originFixture struct {
	s       *Server
	handler http.Handler
	daemon  *atomic.Int64
}

func newOriginFixture(t *testing.T, mutate func(*config.Config)) *originFixture {
	t.Helper()

	sockPath, cleanup := shortSocketPath(t)
	listener := newUnixListener(t, sockPath)
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"26.0.0","ApiVersion":"1.44"}`))
	})
	client, stop := newDockerClientOnListener(t, mux, sockPath, listener, cleanup)
	t.Cleanup(stop)

	cfg := minimalConfig()
	cfg.MaxExecSessions = 1
	cfg.MaxStreamSessions = 1
	if mutate != nil {
		mutate(cfg)
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
	hits.Store(0)
	return &originFixture{s: s, handler: s.httpServer.Handler, daemon: &hits}
}

func (f *originFixture) do(method, path, body string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:40000"
	// A loopback Host by default, so a test that sets none isn't turned away by
	// the Host check on an unauthenticated server. "Host" in header overrides it.
	req.Host = "127.0.0.1:3000"
	for k, vs := range header {
		if k == "Host" {
			req.Host = vs[0]
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

type originRoute struct {
	name, method, path, body string
	header                   http.Header
}

func originRoutes() []originRoute {
	return []originRoute{
		{name: "health", method: http.MethodGet, path: "/_portwing/health"},
		{name: "ready", method: http.MethodGet, path: "/ready"},
		{name: "simple health", method: http.MethodGet, path: "/health"},
		{name: "info", method: http.MethodGet, path: "/_portwing/info"},
		{name: "metrics", method: http.MethodGet, path: "/metrics"},
		{name: "portwing metrics", method: http.MethodGet, path: "/_portwing/metrics"},
		{name: "audit", method: http.MethodGet, path: "/_portwing/audit"},
		{name: "audit export", method: http.MethodGet, path: "/_portwing/audit/export"},
		{name: "compose", method: http.MethodPost, path: "/_portwing/compose", body: `{}`},
		{name: "rest", method: http.MethodGet, path: "/api/v1/containers"},
		{name: "proxy catch-all", method: http.MethodGet, path: "/v1.47/containers/json"},
		{name: "proxy post", method: http.MethodPost, path: "/v1.47/containers/abc/stop"},
		{
			name: "mcp 2025-11-25", method: http.MethodPost, path: "/_portwing/mcp",
			body:   `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
			header: http.Header{"Content-Type": {"application/json"}},
		},
		{
			name: "mcp 2026-07-28", method: http.MethodPost, path: "/_portwing/mcp",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
				`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
			header: http.Header{
				"Content-Type":         {"application/json"},
				"Mcp-Protocol-Version": {"2026-07-28"},
				"Mcp-Method":           {"tools/list"},
			},
		},
		{
			name: "exec start upgrade", method: http.MethodPost, path: "/v1.47/exec/abc123/start", body: `{}`,
			header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"tcp"}},
		},
		{
			name: "attach upgrade", method: http.MethodPost, path: "/v1.47/containers/abc/attach?stream=1", body: ``,
			header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}},
		},
	}
}

func withOrigin(h http.Header, origins ...string) http.Header {
	out := http.Header{}
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	out["Origin"] = origins
	return out
}

// tokenHashFixture is hashed once per test binary: Argon2id is deliberately slow.
var tokenHashFixture = sync.OnceValues(func() (string, error) { return HashToken("s3cret") })

// authModes covers every admission mode: shared secret, hashed secret, Ed25519
// keys, and none at all.
func authModes() map[string]func(*testing.T, *config.Config) {
	return map[string]func(*testing.T, *config.Config){
		"token": func(_ *testing.T, c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false },
		"token_hash": func(t *testing.T, c *config.Config) {
			phc, err := tokenHashFixture()
			if err != nil {
				t.Fatalf("HashToken: %v", err)
			}
			c.TokenHash = phc
			c.AllowUnauthenticated = false
		},
		"ed25519": func(t *testing.T, c *config.Config) {
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatalf("GenerateKey: %v", err)
			}
			c.AuthorizedKeysFile = writeAuthorizedKeys(t, pub)
			c.AllowUnauthenticated = false
		},
		"allow_unauthenticated": func(*testing.T, *config.Config) {},
	}
}

func TestOriginRejectedOnEveryRouteInEveryAuthMode(t *testing.T) {
	t.Parallel()

	for mode, mutate := range authModes() {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newOriginFixture(t, func(c *config.Config) { mutate(t, c) })
			for _, rt := range originRoutes() {
				t.Run(rt.name, func(t *testing.T) {
					// No credentials are presented: a bad Origin must be 403, not 401.
					rec := f.do(rt.method, rt.path, rt.body, withOrigin(rt.header, "https://evil.test"))
					if rec.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403 (body %q)", rec.Code, rec.Body.String())
					}
				})
			}
			if got := f.daemon.Load(); got != 0 {
				t.Errorf("daemon received %d requests behind rejected origins, want 0", got)
			}
			if got := len(f.s.execSem.slots); got != 0 {
				t.Errorf("exec slots taken = %d, want 0", got)
			}
			if got := len(f.s.streamSem.slots); got != 0 {
				t.Errorf("stream slots taken = %d, want 0", got)
			}
			for _, rec := range f.s.auditor.Records(0) {
				if rec.Event == audit.EventExecStart {
					t.Errorf("exec_start record written for a rejected origin: %+v", rec)
				}
				if rec.Event == audit.EventAuthFailure {
					t.Errorf("auth failure recorded before the origin check: %+v", rec)
				}
				if rec.Outcome != audit.OutcomeDenied || rec.Status != http.StatusForbidden {
					t.Errorf("unexpected audit record: %+v", rec)
				}
			}
		})
	}
}

func TestNoOriginIsUnaffectedOnEveryRouteFamily(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false })
	for _, rt := range originRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			// Unauthenticated: authenticated routes answer 401, public routes anything but
			// 403. The point is the origin guard never fires without an Origin header.
			rec := f.do(rt.method, rt.path, rt.body, rt.header)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("request without Origin got 403: %q", rec.Body.String())
			}
		})
	}

	f = newOriginFixture(t, func(c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false })
	authed := http.Header{"Authorization": {"Bearer s3cret"}}
	for _, path := range []string{"/_portwing/info", "/_portwing/audit", "/v1.47/containers/json"} {
		rec := f.do(http.MethodGet, path, "", authed)
		if rec.Code != http.StatusOK {
			t.Errorf("authenticated %s without Origin = %d, want 200", path, rec.Code)
		}
	}
	mcp := originRoutes()[12]
	h := withOrigin(mcp.header)
	delete(h, "Origin")
	h.Set("Authorization", "Bearer s3cret")
	if rec := f.do(mcp.method, mcp.path, mcp.body, h); rec.Code != http.StatusOK {
		t.Errorf("authenticated MCP without Origin = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestAllowlistedOriginPasses(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) {
		c.AllowedOrigins = []string{"https://good.example", "http://localhost:3000"}
	})
	for _, origin := range []string{
		"https://good.example",
		"HTTPS://Good.Example",
		"https://good.example:443",
		"http://localhost:3000",
	} {
		rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{"Origin": {origin}})
		if rec.Code != http.StatusOK {
			t.Errorf("allowlisted Origin %q = %d, want 200", origin, rec.Code)
		}
	}
	if f.daemon.Load() == 0 {
		t.Error("allowlisted requests never reached the daemon")
	}
}

func TestMalformedAndLookalikeOriginsRejected(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.AllowedOrigins = []string{"https://good.example"} })
	cases := map[string][]string{
		"null":                {"null"},
		"garbage":             {"%%%"},
		"empty":               {""},
		"duplicate":           {"https://good.example", "https://good.example"},
		"duplicate mixed":     {"https://good.example", "https://evil.test"},
		"prefix lookalike":    {"https://good.example.evil.test"},
		"port lookalike":      {"https://good.example:444"},
		"scheme downgrade":    {"http://good.example"},
		"suffix lookalike":    {"https://notgood.example"},
		"userinfo lookalike":  {"https://good.example@evil.test"},
		"same origin as Host": {"http://example.com"},
		"wildcard-looking":    {"*"},
	}
	for name, origins := range cases {
		t.Run(name, func(t *testing.T) {
			rec := f.do(http.MethodGet, "/v1.47/containers/json", "", withOrigin(nil, origins...))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Origin %q = %d, want 403", origins, rec.Code)
			}
		})
	}
	if got := f.daemon.Load(); got != 0 {
		t.Errorf("daemon reached %d times, want 0", got)
	}
}

// With auth on, the Host check does not apply, so this isolates the Origin
// check: an Origin that equals the request Host is not allowed automatically.
// Portwing serves no pages of its own, and a page on a rebound hostname
// presents exactly this pair.
func TestOriginEqualToRequestHostIsNotAutoAllowed(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false })
	rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{
		"Host":          {"rebind.example:3000"},
		"Origin":        {"http://rebind.example:3000"},
		"Authorization": {"Bearer s3cret"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin equal to Host = %d, want 403", rec.Code)
	}
	if got := f.daemon.Load(); got != 0 {
		t.Errorf("daemon reached %d times, want 0", got)
	}
}

func TestRejectedOriginCountedLikeA401(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, nil)
	f.do(http.MethodGet, "/api/v1/containers", "", http.Header{"Origin": {"https://evil.test"}})

	var prom strings.Builder
	f.s.metrics.WritePrometheus(&prom, func(s string) string { return s })
	if !strings.Contains(prom.String(), `portwing_http_requests_total{method="GET",code="403"} 1`) {
		t.Errorf("403 not in request metrics:\n%s", prom.String())
	}
}

func TestNewServerRejectsInvalidAllowedOrigins(t *testing.T) {
	t.Parallel()

	client, stop := newStubDockerClient(t)
	defer stop()
	cfg := minimalConfig()
	cfg.AllowedOrigins = []string{"*"}
	_, err := NewServer(cfg, client, &stubServerAdapter{})
	if err == nil || !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
		t.Fatalf("NewServer error = %v, want ALLOWED_ORIGINS error", err)
	}
}

// The docker HEALTHCHECK and `portwing healthcheck` send no Origin, so the
// health routes need no exemption and keep answering with an empty allowlist.
func TestHealthWithoutOriginUnaffectedByGuard(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false })
	for _, path := range []string{"/_portwing/health", "/ready", "/health"} {
		if rec := f.do(http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
			t.Errorf("%s without Origin = %d, want 200", path, rec.Code)
		}
	}
}
