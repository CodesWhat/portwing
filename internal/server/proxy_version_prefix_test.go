package server

// proxy_version_prefix_test.go pins how the Docker proxy treats odd API
// version prefixes and path shapes. Portainer 2.39.7 and 2.45.0 fixed a
// critical authorization bypass where a prefix such as /v1.47.0/ or /v01.47/
// skipped the proxy's access control. Portwing has no allow or deny list of
// its own (access control lives in the Sockguard presets in front of it), but
// it does treat a few routes specially: exec and attach hijack with an exec
// audit record and exec-session limit, and streaming routes with a stream
// session limit. The invariant under test is that an odd prefix is either
// forwarded to the daemon byte for byte, or answered by Portwing itself with a
// redirect, and is never rewritten into a different path.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
)

func TestIsDockerHijackPathVersionPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"exec start canonical prefix hijacks", "/v1.47/exec/abc/start", true},
		{"exec start unversioned hijacks", "/exec/abc/start", true},
		{"exec start leading-zero prefix hijacks like the daemon", "/v01.47/exec/abc/start", true},
		{"attach canonical prefix hijacks", "/v1.47/containers/abc/attach", true},
		{"attach unversioned hijacks", "/containers/abc/attach", true},
		{"attach leading-zero prefix hijacks like the daemon", "/v01.47/containers/abc/attach", true},
		{"exec start uppercase V is not a version; daemon 404s it", "/V1.47/exec/abc/start", false},
		{"attach uppercase V is not a version; daemon 404s it", "/V1.47/containers/abc/attach", false},
		{"exec start trailing slash is not the route; daemon 404s it", "/v1.47/exec/abc/start/", false},
		{"attach trailing slash is not the route; daemon 404s it", "/v1.47/containers/abc/attach/", false},
		{"exec start doubled slash after prefix is not the route", "/v1.47//exec/abc/start", false},
		{"exec start doubled slash in the middle is not the route", "/v1.47/exec//start", false},
		{"exec start decoded slash in the id is not the route", "/v1.47/exec/a/b/start", false},
		{"attach decoded slash in the id is not the route", "/v1.47/containers/a/b/attach", false},
		{"exec start with dot-dot segments is not the route", "/v1.47/../exec/abc/start", false},
		{"exec start four-part prefix hijacks like the daemon", "/v1.47.0.1/exec/abc/start", true},
		{"exec start dots-only prefix hijacks like the daemon", "/v./exec/abc/start", true},
		{"exec start dot-dot prefix hijacks like the daemon", "/v1../exec/abc/start", true},
		{"exec start empty version is not a prefix", "/v/exec/abc/start", false},
		{"exec start letters in the version are not a prefix", "/v1.x/exec/abc/start", false},
		{"exec start doubled prefix is not the route", "/v1.47/v1.47/exec/abc/start", false},
		{"exec resize is not a hijack route", "/v1.47/exec/abc/resize", false},
		{"container start is not a hijack route", "/v1.47/containers/abc/start", false},
		{"empty path is not a hijack route", "", false},
		{"relative path is not a hijack route", "v1.47/exec/abc/start", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDockerHijackPath(tc.path); got != tc.want {
				t.Fatalf("isDockerHijackPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// proxyRecorder is the fake Docker socket's view of what Portwing forwarded.
type proxyRecorder struct {
	mu   sync.Mutex
	reqs []recordedProxyRequest
}

type recordedProxyRequest struct {
	method     string
	requestURI string
	header     http.Header
}

func (p *proxyRecorder) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.reqs = append(p.reqs, recordedProxyRequest{method: r.Method, requestURI: r.RequestURI, header: r.Header.Clone()})
	p.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (p *proxyRecorder) snapshot() []recordedProxyRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedProxyRequest(nil), p.reqs...)
}

// rawProxyRequest sends a request without following redirects, so a redirect
// answered by Portwing's own mux is observable instead of being re-sent. It
// returns the response status and closes the body.
func rawProxyRequest(t *testing.T, ts *httptest.Server, method, path string, header http.Header) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set(headerPortwingToken, "proxy-secret")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestProxyForwardsOddPrefixesVerbatimOrRedirects(t *testing.T) {
	t.Parallel()

	const (
		forwarded = "forwarded verbatim to the daemon, which rejects or serves it on its own rules"
		redirect  = "answered by Portwing's mux with a cleaned-path redirect, never forwarded"
	)
	tests := []struct {
		name string
		path string
		want string
	}{
		{"canonical prefix", "/v1.47/containers/json", forwarded},
		{"unversioned", "/containers/json", forwarded},
		{"three-part prefix", "/v1.47.0/containers/json", forwarded},
		{"leading-zero prefix", "/v01.47/containers/json", forwarded},
		{"uppercase V prefix", "/V1.47/containers/json", forwarded},
		{"trailing slash", "/v1.47/containers/json/", forwarded},
		{"encoded slash in the id", "/v1.47/containers/a%2Fb/json", forwarded},
		{"encoded slash replacing the separator after the prefix", "/v1.47%2Fcontainers/json", forwarded},
		{"encoded letter in a route word", "/v1.47/containers/%6Ason", forwarded},
		{"doubled slash after prefix", "/v1.47//containers/json", redirect},
		{"doubled slash at the root", "//v1.47/containers/json", redirect},
		{"dot-dot segment", "/v1.47/images/../containers/json", redirect},
		{"dot-dot out of the prefix", "/v1.47/../containers/json", redirect},
		{"single dot segment", "/v1.47/./containers/json", redirect},
	}
	for _, tc := range tests {
		t.Run(tc.name+": "+tc.want, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			_, ts := newStreamRouteServer(t, rec.handler, 5, 4)

			status := rawProxyRequest(t, ts, http.MethodGet, tc.path, nil)

			got := rec.snapshot()
			switch tc.want {
			case forwarded:
				if status != http.StatusOK {
					t.Fatalf("status = %d, want the daemon's 200", status)
				}
				if len(got) != 1 || got[0].requestURI != tc.path {
					t.Fatalf("daemon saw %+v, want exactly one request for %q", got, tc.path)
				}
			case redirect:
				if status < 300 || status > 399 {
					t.Fatalf("status = %d, want a 3xx redirect", status)
				}
				if len(got) != 0 {
					t.Fatalf("daemon saw %+v, want nothing for a redirected path", got)
				}
			}
		})
	}
}

// fillStreamSlot takes the only stream slot. A request Portwing classifies as
// streaming is then refused with 503 before reaching the daemon, while any
// other request passes straight through, which makes admission an exact oracle
// for the classification of a path.
func fillStreamSlot(t *testing.T, s *Server) {
	t.Helper()
	if !s.streamSem.acquire() {
		t.Fatal("stream slot already taken")
	}
	t.Cleanup(s.streamSem.release)
}

func TestProxyStreamClassificationVersionPrefixes(t *testing.T) {
	t.Parallel()

	const (
		streamed   = "classified as streaming and refused at the full stream limit"
		unguarded  = "not a stats route for the daemon either, so it passes to the daemon"
		wantStatus = http.StatusServiceUnavailable
	)
	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"stats canonical prefix", http.MethodGet, "/v1.47/containers/abc/stats", streamed},
		{"stats unversioned", http.MethodGet, "/containers/abc/stats", streamed},
		{"stats leading-zero prefix", http.MethodGet, "/v01.47/containers/abc/stats", streamed},
		{"push canonical prefix", http.MethodPost, "/v1.47/images/nginx/push", streamed},
		{"push leading-zero prefix", http.MethodPost, "/v01.47/images/nginx/push", streamed},
		{"stats uppercase V", http.MethodGet, "/V1.47/containers/abc/stats", unguarded},
		{"stats trailing slash", http.MethodGet, "/v1.47/containers/abc/stats/", unguarded},
		{"stats encoded slash in the id is decoded to a non-route", http.MethodGet, "/v1.47/containers/a%2Fb/stats", unguarded},
		{"stats with stream disabled", http.MethodGet, "/v1.47/containers/abc/stats?stream=0", unguarded},
		{"logs odd prefix still suffix-matched", http.MethodGet, "/v1.47.0/containers/abc/logs", streamed},
	}
	for _, tc := range tests {
		t.Run(tc.name+": "+tc.want, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			s, ts := newStreamRouteServer(t, rec.handler, 5, 1)
			fillStreamSlot(t, s)

			status := rawProxyRequest(t, ts, tc.method, tc.path, nil)

			switch tc.want {
			case streamed:
				if status != wantStatus || len(rec.snapshot()) != 0 {
					t.Fatalf("status = %d, daemon saw %d requests, want 503 and none", status, len(rec.snapshot()))
				}
			case unguarded:
				if status != http.StatusOK || len(rec.snapshot()) != 1 {
					t.Fatalf("status = %d, daemon saw %d requests, want 200 and one", status, len(rec.snapshot()))
				}
			}
		})
	}
}

// TestProxyHijackClassificationVersionPrefixes uses the exec-session limit and
// the exec_start audit record as oracles. A path Portwing treats as an exec
// hijack is audited and refused with 503 while the exec limit is full. Any
// other path is proxied as plain HTTP, with no exec audit record.
func TestProxyHijackClassificationVersionPrefixes(t *testing.T) {
	t.Parallel()

	const (
		hijacked = "treated as an exec hijack: audited and refused at the full exec limit"
		plain    = "not an exec route for the daemon either, so it is proxied as plain HTTP"
	)
	tests := []struct {
		name string
		path string
		want string
		// wantAudit is the exec_start record count: exec start is audited,
		// attach is limited but not audited as an exec.
		wantAudit int
	}{
		{"exec start canonical prefix", "/v1.47/exec/abc/start", hijacked, 1},
		{"exec start unversioned", "/exec/abc/start", hijacked, 1},
		{"exec start leading-zero prefix", "/v01.47/exec/abc/start", hijacked, 1},
		{"attach canonical prefix", "/v1.47/containers/abc/attach", hijacked, 0},
		{"attach leading-zero prefix", "/v01.47/containers/abc/attach", hijacked, 0},
		{"exec start encoded letter in the route word", "/v1.47/exec/abc/%73tart", hijacked, 1},
		{"exec start uppercase V", "/V1.47/exec/abc/start", plain, 0},
		{"exec start trailing slash", "/v1.47/exec/abc/start/", plain, 0},
		{"exec start encoded slash in the id", "/v1.47/exec/a%2Fb/start", plain, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name+": "+tc.want, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			s, ts := newStreamRouteServer(t, rec.handler, 5, 4)
			s.execSem = newConcurrencyLimiter(1)
			if !s.execSem.acquire() {
				t.Fatal("exec slot already taken")
			}
			defer s.execSem.release()

			status := rawProxyRequest(t, ts, http.MethodPost, tc.path, http.Header{"Upgrade": {"tcp"}, "Connection": {"Upgrade"}})

			execRecords := 0
			for _, record := range s.auditor.Records(100) {
				if record.Event == audit.EventExecStart {
					execRecords++
				}
			}
			switch tc.want {
			case hijacked:
				if status != http.StatusServiceUnavailable || execRecords != tc.wantAudit || len(rec.snapshot()) != 0 {
					t.Fatalf("status = %d, exec audit records = %d, daemon saw %d requests; want 503, %d, 0",
						status, execRecords, len(rec.snapshot()), tc.wantAudit)
				}
			case plain:
				if status != http.StatusOK || execRecords != 0 || len(rec.snapshot()) != 1 {
					t.Fatalf("status = %d, exec audit records = %d, daemon saw %d requests; want 200, 0, 1",
						status, execRecords, len(rec.snapshot()))
				}
			}
		})
	}
}

func TestProxyAuditRecordsPathAsSent(t *testing.T) {
	t.Parallel()

	rec := &proxyRecorder{}
	s, ts := newStreamRouteServer(t, rec.handler, 5, 4)

	for _, path := range []string{"/v1.47.0/containers/json", "/V1.47/containers/json", "/v01.47/containers/json"} {
		_ = rawProxyRequest(t, ts, http.MethodGet, path, nil)
	}

	var seen []string
	for _, record := range s.auditor.Records(100) {
		if record.Event == audit.EventAPIRequest {
			seen = append(seen, record.Path)
		}
	}
	slices.Sort(seen)
	want := "/V1.47/containers/json,/v01.47/containers/json,/v1.47.0/containers/json"
	if got := strings.Join(seen, ","); got != want {
		t.Fatalf("audit paths = %q, want the paths exactly as requested: %q", got, want)
	}
}
