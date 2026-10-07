package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
)

const rebindHost = "rebind.attacker.example:3000"

// unauthenticated is the config the Host check exists for: ALLOW_UNAUTHENTICATED
// on loopback.
func unauthenticated(*config.Config) {}

func tokenAuth(c *config.Config) { c.Token = "s3cret"; c.AllowUnauthenticated = false }

// readRoutes are the GET and HEAD requests a rebound page makes same-origin, so
// they carry no Origin header. Each reads data on an unauthenticated server.
func readRoutes() []originRoute {
	return []originRoute{
		{name: "containers/json", method: http.MethodGet, path: "/v1.47/containers/json"},
		{name: "inspect", method: http.MethodGet, path: "/v1.47/containers/abc/json"},
		{name: "archive", method: http.MethodGet, path: "/v1.47/containers/abc/archive?path=/etc"},
		{name: "archive HEAD", method: http.MethodHead, path: "/v1.47/containers/abc/archive?path=/etc"},
		{name: "logs", method: http.MethodGet, path: "/v1.47/containers/abc/logs?stdout=1"},
		{name: "audit", method: http.MethodGet, path: "/_portwing/audit"},
		{name: "audit export", method: http.MethodGet, path: "/_portwing/audit/export"},
		{name: "metrics", method: http.MethodGet, path: "/metrics"},
		{name: "portwing metrics", method: http.MethodGet, path: "/_portwing/metrics"},
		{name: "info", method: http.MethodGet, path: "/_portwing/info"},
		{name: "rest", method: http.MethodGet, path: "/api/v1/containers"},
		{name: "health", method: http.MethodGet, path: "/_portwing/health"},
		{name: "ready", method: http.MethodGet, path: "/ready"},
		{name: "simple health", method: http.MethodGet, path: "/health"},
	}
}

func TestRebindingHostRejectedWhenAuthIsOff(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, unauthenticated)
	for _, rt := range readRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			// No Origin header: a same-origin read after rebinding.
			rec := f.do(rt.method, rt.path, "", http.Header{"Host": {rebindHost}})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Host %q = %d, want 403 (body %q)", rebindHost, rec.Code, rec.Body.String())
			}
			if rt.method != http.MethodHead && rec.Body.String() != "forbidden origin\n" {
				t.Errorf("body = %q, want the same body the Origin check sends", rec.Body.String())
			}
		})
	}
	if got := f.daemon.Load(); got != 0 {
		t.Errorf("daemon received %d requests behind a rebinding Host, want 0", got)
	}
	for _, rec := range f.s.auditor.Records(0) {
		if rec.Event != audit.EventAPIRequest || rec.Outcome != audit.OutcomeDenied || rec.Status != http.StatusForbidden {
			t.Errorf("unexpected audit record: %+v", rec)
		}
	}
	var prom strings.Builder
	f.s.metrics.WritePrometheus(&prom, func(s string) string { return s })
	if !strings.Contains(prom.String(), `portwing_http_requests_total{method="HEAD",code="403"} 1`) {
		t.Errorf("rejections not counted:\n%s", prom.String())
	}
}

func TestRebindingHostRejectedOnEveryRouteWhenAuthIsOff(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, unauthenticated)
	for _, rt := range originRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			h := http.Header{"Host": {rebindHost}}
			for k, v := range rt.header {
				h[k] = v
			}
			if rec := f.do(rt.method, rt.path, rt.body, h); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
	if got := f.daemon.Load(); got != 0 {
		t.Errorf("daemon reached %d times, want 0", got)
	}
	if got := len(f.s.execSem.slots); got != 0 {
		t.Errorf("exec slots taken = %d, want 0", got)
	}
}

func TestAcceptableHostsPassWhenAuthIsOff(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.AllowedHosts = []string{"ops.example.com"} })
	hosts := []string{
		"127.0.0.1:3000",
		"127.0.0.1",
		"[::1]:3000",
		"localhost:3000",
		"LocalHost",
		"portwing:3000",
		"portwing",
		"ops.example.com",
		"OPS.Example.COM:3000",
		"ops.example.com.",
		"ops.example.com.:3000",
	}
	for _, host := range hosts {
		rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{"Host": {host}})
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, rec.Code)
		}
	}
}

func TestLookalikeAndMalformedHostsRejectedWhenAuthIsOff(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, func(c *config.Config) { c.AllowedHosts = []string{"ops.example.com"} })
	hosts := []string{
		"localhost.attacker.example",
		"127.0.0.1.attacker.example:3000",
		"attacker.example.",
		"ops.example.com.attacker.example",
		"localhost@attacker.example",
		"user@localhost",
		"localhost/path",
		"attacker.example/localhost",
		"attacker..",
		"[::1",
		":3000",
	}
	for _, host := range hosts {
		rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{"Host": {host}})
		if rec.Code != http.StatusForbidden {
			t.Errorf("Host %q = %d, want 403", host, rec.Code)
		}
	}
	if got := f.daemon.Load(); got != 0 {
		t.Errorf("daemon reached %d times, want 0", got)
	}
}

// With authentication on the Host check does not apply: a rebinding page can
// neither sign a request nor present the secret, so auth is the control.
// HTTP/1.0 health checkers that probe by IP (HAProxy httpchk, Nagios
// check_http) can omit Host. A browser never can, so admitting it costs nothing
// against rebinding.
func TestRequestWithNoHostHeaderIsAdmitted(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, unauthenticated)
	rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{"Host": {""}})
	if rec.Code != http.StatusOK {
		t.Fatalf("empty Host = %d, want 200", rec.Code)
	}

	ts := httptest.NewServer(f.handler)
	defer ts.Close()
	assertRawHTTP10NoHost(t, ts.URL, "/health")
}

// assertRawHTTP10NoHost sends "GET path HTTP/1.0" with no Host header over a
// real connection and wants a 200.
func assertRawHTTP10NoHost(t *testing.T, baseURL, path string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", strings.TrimPrefix(baseURL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.0\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("HTTP/1.0 request with no Host: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s HTTP/1.0 with no Host = %d, want 200", path, resp.StatusCode)
	}
}

func TestHostCheckDoesNotApplyWhenAuthIsOn(t *testing.T) {
	t.Parallel()

	for mode, mutate := range authModes() {
		if mode == "allow_unauthenticated" {
			continue
		}
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newOriginFixture(t, func(c *config.Config) { mutate(t, c) })
			// The attacker Host gets through the guard and meets authentication.
			rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{"Host": {rebindHost}})
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attacker Host, no credentials = %d, want 401 from auth", rec.Code)
			}
			if got := f.daemon.Load(); got != 0 {
				t.Errorf("daemon reached %d times without credentials", got)
			}
		})
	}

	f := newOriginFixture(t, tokenAuth)
	rec := f.do(http.MethodGet, "/v1.47/containers/json", "", http.Header{
		"Host": {rebindHost}, "Authorization": {"Bearer s3cret"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("attacker Host with the secret = %d, want 200", rec.Code)
	}
}

func TestNewServerRejectsInvalidAllowedHosts(t *testing.T) {
	t.Parallel()

	client, stop := newStubDockerClient(t)
	defer stop()
	cfg := minimalConfig()
	cfg.AllowedHosts = []string{"https://ops.example.com"}
	_, err := NewServer(cfg, client, &stubServerAdapter{})
	if err == nil || !strings.Contains(err.Error(), "ALLOWED_HOSTS") {
		t.Fatalf("NewServer error = %v, want ALLOWED_HOSTS error", err)
	}
}

func TestAuthDisabledIsOneSourceOfTruth(t *testing.T) {
	t.Parallel()

	reg := Ed25519Config{}
	if !authDisabled(nil, reg) {
		t.Error("no verifier and no registry should be auth off")
	}
	if authDisabled(newRawTokenVerifier("x"), reg) {
		t.Error("a verifier means auth is on")
	}
	if authDisabled(nil, Ed25519Config{Registry: auth.NewKeyRegistry("")}) {
		t.Error("a key registry means auth is on")
	}
}

// A rejection arrives before the body is read, so it must free the connection
// without waiting for a body that never comes (forbid aborts the drain).
func TestGuardRejectionReleasesConnectionWithUnreadBody(t *testing.T) {
	t.Parallel()

	f := newOriginFixture(t, tokenAuth)
	ts := httptest.NewServer(f.handler)
	defer ts.Close()
	// Host "localhost" is fine; the Origin is what is rejected.
	assertUnreadBodyRejectedOnHost(t, ts.URL, "localhost", http.MethodPost, "/v1.47/exec/abc/start",
		"Origin: https://evil.test\r\nContent-Length: 1000\r\n", "", http.StatusForbidden)

	u := newOriginFixture(t, unauthenticated)
	us := httptest.NewServer(u.handler)
	defer us.Close()
	assertUnreadBodyRejectedOnHost(t, us.URL, "rebind.attacker.example", http.MethodPost, "/v1.47/exec/abc/start",
		"Content-Length: 1000\r\n", "", http.StatusForbidden)
}
