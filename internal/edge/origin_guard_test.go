package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/metrics"
)

// Edge mode has no inbound control port, but it does serve an operations
// listener (health, readiness, metrics, audit export). That listener carries
// no authentication, so a rebinding page could read it: it gets the same
// Origin guard as standard mode.
func TestOperationsListenerRejectsDisallowedOrigin(t *testing.T) {
	t.Parallel()

	auditor, _, err := audit.New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auditor.Close)
	reg := metrics.NewRegistry()
	reg.SetEdgeMode(true)
	c := &Client{
		cfg: &config.Config{
			BindAddress:    "127.0.0.1",
			Port:           "0",
			AllowedOrigins: []string{"https://good.example"},
		},
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

	get := func(path string, origins ...string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
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

	for _, path := range []string{"/health", "/ready", "/_portwing/health", "/metrics", "/_portwing/audit/export"} {
		if got := get(path, "https://evil.test"); got != http.StatusForbidden {
			t.Errorf("GET %s with disallowed Origin = %d, want 403", path, got)
		}
		if got := get(path, "https://good.example", "https://good.example"); got != http.StatusForbidden {
			t.Errorf("GET %s with duplicate Origin = %d, want 403", path, got)
		}
	}
	if got := get("/health"); got != http.StatusOK {
		t.Errorf("GET /health without Origin = %d, want 200", got)
	}
	if got := get("/health", "HTTPS://Good.Example:443"); got != http.StatusOK {
		t.Errorf("GET /health with allowlisted Origin = %d, want 200", got)
	}
}

func TestOriginGuardFallsBackToRejectingEveryOrigin(t *testing.T) {
	t.Parallel()

	c := &Client{cfg: &config.Config{AllowedOrigins: []string{"*"}}}
	h := c.originGuard().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for origin, want := range map[string]int{"": http.StatusOK, "https://good.example": http.StatusForbidden} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1/health", nil)
		req.RemoteAddr = "127.0.0.1:1"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Origin %q = %d, want %d", origin, rec.Code, want)
		}
	}
}
