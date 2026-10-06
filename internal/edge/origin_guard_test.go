package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/metrics"
)

var operationsPaths = []string{"/health", "/ready", "/_portwing/health", "/metrics", "/_portwing/audit/export"}

// operationsListener starts the edge operations listener on a free port and
// returns a request helper, the audit logger, and the metrics registry.
func operationsListener(t *testing.T, mutate func(*config.Config)) (get func(path, host string, origins ...string) int, auditor *audit.Logger, reg *metrics.Registry) {
	t.Helper()

	auditor, _, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auditor.Close)
	reg = metrics.NewRegistry()
	reg.SetEdgeMode(true)
	cfg := &config.Config{BindAddress: "127.0.0.1", Port: "0"}
	if mutate != nil {
		mutate(cfg)
	}
	c := &Client{
		cfg:          cfg,
		dockerClient: &fakeDocker{},
		collector:    metrics.NewCollector("", true),
		metrics:      reg,
		auditor:      auditor,
		startTime:    time.Now(),
	}
	c.startHealthServer()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.healthServer.Shutdown(ctx)
	})
	base := waitForHealthServer(t, c, "/health")
	base = base[:len(base)-len("/health")]

	get = func(path, host string, origins ...string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		for _, o := range origins {
			req.Header.Add("Origin", o)
		}
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	return get, auditor, reg
}

func requestCount(reg *metrics.Registry, code string) bool {
	var b strings.Builder
	reg.WritePrometheus(&b, func(s string) string { return s })
	return strings.Contains(b.String(), `code="`+code+`"`)
}

// Edge mode has no inbound control port, but it does serve an operations
// listener (health, readiness, metrics, audit export) with no authentication.
// A disallowed Origin gets 403 there, is counted in the request metrics, and
// writes no audit record: a page can fire rejected GETs fast enough to evict
// real records from the ring, and nothing else on this listener writes it.
func TestOperationsListenerRejectsDisallowedOriginWithoutAuditRecords(t *testing.T) {
	t.Parallel()

	get, auditor, reg := operationsListener(t, func(c *config.Config) {
		c.AllowedOrigins = []string{"https://good.example"}
	})
	for _, path := range operationsPaths {
		if got := get(path, "", "https://evil.test"); got != http.StatusForbidden {
			t.Errorf("GET %s with disallowed Origin = %d, want 403", path, got)
		}
		if got := get(path, "", "https://good.example", "https://good.example"); got != http.StatusForbidden {
			t.Errorf("GET %s with duplicate Origin = %d, want 403", path, got)
		}
	}
	if got := get("/health", ""); got != http.StatusOK {
		t.Errorf("GET /health without Origin = %d, want 200", got)
	}
	if got := get("/health", "", "HTTPS://Good.Example:443"); got != http.StatusOK {
		t.Errorf("GET /health with allowlisted Origin = %d, want 200", got)
	}
	if !requestCount(reg, "403") {
		t.Error("rejections were not counted in the request metrics")
	}
	if records := auditor.Records(0); len(records) != 0 {
		t.Errorf("guard rejections wrote %d audit records, want none: %+v", len(records), records)
	}
}

// The operations listener is all GET, and after DNS rebinding a page's GETs are
// same-origin and carry no Origin, so the Host header is what stops the read.
func TestOperationsListenerRejectsRebindingHost(t *testing.T) {
	t.Parallel()

	get, auditor, reg := operationsListener(t, func(c *config.Config) {
		c.AllowedHosts = []string{"ops.example.com"}
	})
	for _, host := range []string{
		"rebind.attacker.example:3000",
		"localhost.attacker.example",
		"127.0.0.1.attacker.example:3000",
		"attacker.example.",
	} {
		for _, path := range operationsPaths {
			if got := get(path, host); got != http.StatusForbidden {
				t.Errorf("GET %s with Host %q = %d, want 403", path, host, got)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:3000", "[::1]:3000", "localhost:3000", "portwing:3000", "ops.example.com:3000", "OPS.example.com."} {
		for _, path := range operationsPaths {
			// Readiness answers 503 here (no controller is connected); the
			// point is that the guard let it through.
			if got := get(path, host); got == http.StatusForbidden {
				t.Errorf("GET %s with Host %q = 403, want the guard to pass it", path, host)
			}
		}
	}
	if !requestCount(reg, "403") {
		t.Error("rejections were not counted in the request metrics")
	}
	if records := auditor.Records(0); len(records) != 0 {
		t.Errorf("guard rejections wrote %d audit records, want none", len(records))
	}
}

func TestOriginGuardFallsBackToBuiltInRules(t *testing.T) {
	t.Parallel()

	c := &Client{cfg: &config.Config{AllowedOrigins: []string{"*"}, AllowedHosts: []string{"*"}}}
	h := c.originGuard().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1", "", http.StatusOK},
		{"127.0.0.1", "https://good.example", http.StatusForbidden},
		{"ops.example.com", "", http.StatusForbidden},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Host = tc.host
		req.RemoteAddr = "127.0.0.1:1"
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Host %q Origin %q = %d, want %d", tc.host, tc.origin, rec.Code, tc.want)
		}
	}
}
