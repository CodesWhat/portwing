package audit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/config"
)

func hostReq(host string, origins ...string) *http.Request {
	r := newOriginReq(origins...)
	r.Host = host
	return r
}

func TestHostGuard(t *testing.T) {
	t.Parallel()

	hosts, err := config.ParseHostAllowlist([]string{"ops.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	allow, _ := config.ParseOriginAllowlist(nil)

	cases := []struct {
		name      string
		checkHost bool
		host      string
		origins   []string
		want      int
	}{
		{"attacker host rejected", true, "rebind.attacker.example:3000", nil, http.StatusForbidden},
		{"empty host rejected", true, "", nil, http.StatusForbidden},
		{"ip literal passes", true, "127.0.0.1:3000", nil, http.StatusNoContent},
		{"ipv6 literal passes", true, "[::1]:3000", nil, http.StatusNoContent},
		{"localhost passes", true, "localhost:3000", nil, http.StatusNoContent},
		{"single label passes", true, "portwing:3000", nil, http.StatusNoContent},
		{"allowed host passes", true, "OPS.example.com.:3000", nil, http.StatusNoContent},
		{"host check off lets attacker host through", false, "rebind.attacker.example:3000", nil, http.StatusNoContent},
		{"host check off still checks origin", false, "rebind.attacker.example:3000", []string{"https://evil.test"}, http.StatusForbidden},
		{"good host with bad origin rejected", true, "localhost:3000", []string{"https://evil.test"}, http.StatusForbidden},
		{"bad host with allowlist-less origin rejected", true, "attacker.example", []string{"https://attacker.example"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := &countingRequests{}
			called := false
			h := OriginGuard{Allow: allow, CheckHost: tc.checkHost, Hosts: hosts, Requests: reg}.Wrap(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, hostReq(tc.host, tc.origins...))
			if rec.Code != tc.want {
				t.Fatalf("Host %q Origin %q = %d, want %d", tc.host, tc.origins, rec.Code, tc.want)
			}
			if called != (tc.want == http.StatusNoContent) {
				t.Fatalf("handler called = %v with status %d", called, rec.Code)
			}
			if tc.want == http.StatusForbidden && len(reg.calls) != 1 {
				t.Errorf("rejection counted %d times, want once", len(reg.calls))
			}
		})
	}
}

// Both checks answer with the same body so a probe learns nothing about which
// one fired.
func TestHostAndOriginRejectionsShareTheBody(t *testing.T) {
	t.Parallel()

	allow, _ := config.ParseOriginAllowlist(nil)
	h := OriginGuard{Allow: allow, CheckHost: true}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	byHost := httptest.NewRecorder()
	h.ServeHTTP(byHost, hostReq("attacker.example"))
	byOrigin := httptest.NewRecorder()
	h.ServeHTTP(byOrigin, hostReq("localhost", "https://evil.test"))

	if byHost.Code != http.StatusForbidden || byOrigin.Code != http.StatusForbidden {
		t.Fatalf("statuses = %d, %d, want 403, 403", byHost.Code, byOrigin.Code)
	}
	if byHost.Body.String() != byOrigin.Body.String() || byHost.Body.String() != rejectBody+"\n" {
		t.Errorf("bodies differ: %q vs %q", byHost.Body.String(), byOrigin.Body.String())
	}
	if byHost.Header().Get("Connection") != "close" {
		t.Errorf("Connection = %q, want close", byHost.Header().Get("Connection"))
	}
}

func TestHostRejectionAuditsOnlyWhenAnAuditorIsSet(t *testing.T) {
	t.Parallel()

	allow, _ := config.ParseOriginAllowlist(nil)
	auditor, _, err := New("", 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auditor.Close)

	silent := OriginGuard{Allow: allow, CheckHost: true}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	silent.ServeHTTP(httptest.NewRecorder(), hostReq("attacker.example"))
	if got := auditor.Records(0); len(got) != 0 {
		t.Fatalf("guard without an Auditor wrote records: %+v", got)
	}

	audited := OriginGuard{Allow: allow, CheckHost: true, Auditor: auditor}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	audited.ServeHTTP(httptest.NewRecorder(), hostReq("attacker.example"))
	got := auditor.Records(0)
	if len(got) != 1 || got[0].Outcome != OutcomeDenied || got[0].Status != http.StatusForbidden {
		t.Fatalf("records = %+v, want one denied 403", got)
	}
}

func TestWarnLimiter(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := &warnLimiter{}

	for i := 0; i < warnBurst; i++ {
		emit, skipped := l.allow(base.Add(time.Duration(i) * time.Millisecond))
		if !emit || skipped != 0 {
			t.Fatalf("rejection %d in the first window: emit=%v skipped=%d, want a log with no skips", i+1, emit, skipped)
		}
	}
	for i := 0; i < 3; i++ {
		if emit, _ := l.allow(base.Add(time.Second)); emit {
			t.Fatalf("rejection past the burst logged")
		}
	}
	// One nanosecond before the window closes is still the same window.
	if emit, _ := l.allow(base.Add(warnWindow - time.Nanosecond)); emit {
		t.Fatal("logged just before the window ended")
	}
	// The window ends exactly warnWindow after its first line.
	emit, skipped := l.allow(base.Add(warnWindow))
	if !emit || skipped != 4 {
		t.Fatalf("first line of the next window: emit=%v skipped=%d, want a log reporting 4 skipped", emit, skipped)
	}
	if emit, skipped := l.allow(base.Add(warnWindow + time.Second)); !emit || skipped != 0 {
		t.Fatalf("second line of the next window: emit=%v skipped=%d", emit, skipped)
	}
}

func TestGuardSamplesTheWarnLogButCountsEveryRejection(t *testing.T) {
	logs, restore := captureLogs()
	defer restore()

	reg := &countingRequests{}
	allow, _ := config.ParseOriginAllowlist(nil)
	h := OriginGuard{Allow: allow, CheckHost: true, Requests: reg}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	const total = 50
	for i := 0; i < total; i++ {
		h.ServeHTTP(httptest.NewRecorder(), hostReq("attacker.example"))
	}
	if len(reg.calls) != total {
		t.Fatalf("counted %d rejections, want %d", len(reg.calls), total)
	}
	if got := strings.Count(logs.String(), "host not allowed"); got != warnBurst {
		t.Fatalf("WARN lines = %d, want %d", got, warnBurst)
	}
	if !strings.Contains(logs.String(), "host=attacker.example") {
		t.Errorf("host not logged: %q", logs.String())
	}
}

// The happy path for an unauthenticated server (allowed Host, no Origin)
// allocates nothing for an IP literal, localhost or a single-label name.
func TestHostGuardHappyPathAllocations(t *testing.T) {
	allow, _ := config.ParseOriginAllowlist([]string{"https://good.example"})
	h := OriginGuard{Allow: allow, CheckHost: true}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, host := range []string{"127.0.0.1:3000", "[::1]:3000", "localhost:3000", "portwing:3000", "LocalHost:3000"} {
		req := hostReq(host)
		got := testing.AllocsPerRun(1000, func() { h.ServeHTTP(discardWriter{}, req) })
		t.Logf("allocs per request, Host %q, no Origin: %v", host, got)
		if got != 0 {
			t.Errorf("Host %q: allocs per request = %v, want 0", host, got)
		}
	}
}

func BenchmarkHostGuardIPLiteral(b *testing.B) {
	allow, _ := config.ParseOriginAllowlist(nil)
	h := OriginGuard{Allow: allow, CheckHost: true}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := hostReq("127.0.0.1:3000")
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(discardWriter{}, req)
	}
}

func BenchmarkHostGuardAllowedHostName(b *testing.B) {
	hosts, _ := config.ParseHostAllowlist([]string{"ops.example.com"})
	allow, _ := config.ParseOriginAllowlist(nil)
	h := OriginGuard{Allow: allow, CheckHost: true, Hosts: hosts}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := hostReq("ops.example.com:3000")
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(discardWriter{}, req)
	}
}
