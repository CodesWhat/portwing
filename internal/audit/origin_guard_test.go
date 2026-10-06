package audit

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/codeswhat/portwing/internal/config"
)

func newOriginReq(origins ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil)
	r.RemoteAddr = "192.0.2.10:40000"
	for _, o := range origins {
		r.Header.Add("Origin", o)
	}
	return r
}

type countingRequests struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingRequests) IncRequest(method string, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method+" "+http.StatusText(code))
}

// captureLogs routes the default logger into a buffer. Tests that use it must
// not run in parallel with each other.
func captureLogs() (*strings.Builder, func()) {
	prev := slog.Default()
	logs := &strings.Builder{}
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	return logs, func() { slog.SetDefault(prev) }
}

func TestOriginGuardRejects(t *testing.T) {
	logs, restore := captureLogs()
	t.Cleanup(restore)

	auditor, _, err := New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auditor.Close)
	reg := &countingRequests{}

	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow, Auditor: auditor, Requests: reg}.Wrap(next)

	long := "https://" + strings.Repeat("a", 5000) + ".test\nforged=1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newOriginReq(long))

	if called {
		t.Fatal("handler ran for a rejected origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if rec.Header().Get("Connection") != "close" {
		t.Errorf("Connection = %q, want close", rec.Header().Get("Connection"))
	}

	records := auditor.Records(0)
	if len(records) != 1 {
		t.Fatalf("audit records = %+v, want 1", records)
	}
	got := records[0]
	if got.Event != EventAPIRequest || got.Outcome != OutcomeDenied || got.Status != 403 ||
		got.Actor != "192.0.2.10" || got.Method != "GET" || got.Path != "/api/v1/containers" {
		t.Errorf("audit record = %+v", got)
	}
	// A rejection answers in microseconds: a duration in milliseconds that is
	// negative or large means the unit conversion is wrong.
	if got.DurationMs < 0 || got.DurationMs > 1000 {
		t.Errorf("DurationMs = %v, want a small non-negative number of milliseconds", got.DurationMs)
	}

	if len(reg.calls) != 1 || reg.calls[0] != "GET Forbidden" {
		t.Errorf("request counter calls = %q, want one 403 for GET", reg.calls)
	}

	line := logs.String()
	if !strings.Contains(line, "origin not allowed") || !strings.Contains(line, "level=WARN") {
		t.Errorf("no WARN log: %q", line)
	}
	if strings.Contains(line, strings.Repeat("a", 200)) || strings.Contains(line, "\nforged=1") {
		t.Errorf("origin not bounded or not escaped in log: %q", line)
	}
}

func TestOriginGuardPassesThrough(t *testing.T) {
	t.Parallel()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow}.Wrap(next)

	for _, origins := range [][]string{nil, {"https://GOOD.example:443"}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newOriginReq(origins...))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d for %q, want 204", rec.Code, origins)
		}
	}
	if called != 2 {
		t.Fatalf("handler ran %d times, want 2", called)
	}
}

func TestOriginGuardActorOverrideAndBadRemoteAddr(t *testing.T) {
	t.Parallel()

	g := OriginGuard{Actor: func(*http.Request) string { return "198.51.100.7" }}
	if got := g.actor(newOriginReq()); got != "198.51.100.7" {
		t.Errorf("actor override = %q", got)
	}
	r := newOriginReq()
	r.RemoteAddr = "no-port"
	if got := (OriginGuard{}).actor(r); got != "no-port" {
		t.Errorf("actor with unsplittable RemoteAddr = %q", got)
	}
}

func TestBoundedLoggedOrigin(t *testing.T) {
	t.Parallel()

	if got := boundedOrigin("https://a.test"); got != "https://a.test" {
		t.Errorf("short = %s", got)
	}
	exact := strings.Repeat("x", maxLoggedOrigin)
	if got := boundedOrigin(exact); got != exact {
		t.Errorf("value of exactly %d bytes was altered: %q", maxLoggedOrigin, got)
	}
	over := strings.Repeat("x", maxLoggedOrigin+1)
	if got := boundedOrigin(over); got != exact+"..." {
		t.Errorf("value of %d bytes = %q, want the first %d bytes plus an ellipsis", len(over), got, maxLoggedOrigin)
	}
	got := boundedOrigin(strings.Repeat("x", maxLoggedOrigin+50))
	if len(got) > maxLoggedOrigin+10 || !strings.Contains(got, "...") {
		t.Errorf("long value not bounded: len %d", len(got))
	}
	if strings.ContainsAny(boundedOrigin("a\r\nb"), "\r\n") {
		t.Error("control characters survived")
	}
}

// The hot path pays nothing when the header is absent.
func TestOriginGuardNoOriginDoesNotAllocate(t *testing.T) {
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := newOriginReq()
	w := discardWriter{}
	if got := testing.AllocsPerRun(1000, func() { h.ServeHTTP(w, req) }); got != 0 {
		t.Fatalf("allocs per request with no Origin = %v, want 0", got)
	}
}

func BenchmarkOriginGuardNoOrigin(b *testing.B) {
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := newOriginReq()
	w := discardWriter{}
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkOriginGuardAllowedOrigin(b *testing.B) {
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := newOriginReq("https://good.example")
	w := discardWriter{}
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, req)
	}
}

type discardWriter struct{}

func (discardWriter) Header() http.Header         { return http.Header{} }
func (discardWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (discardWriter) WriteHeader(int)             {}
