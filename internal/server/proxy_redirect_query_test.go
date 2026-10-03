package server

// proxy_redirect_query_test.go pins two ways the proxy could act on a request
// differently from the one it classified. The daemon's router answers an
// unclean path with a 301 to the cleaned path, and the proxy must hand that
// back rather than follow it. A '#' in the query is query text, and must reach
// the daemon whole.

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

// cleaningDaemon behaves like the daemon's router: it 301s any path that
// differs from its cleaned form and serves the cleaned path. It records every
// request it receives.
type cleaningDaemon struct {
	mu   sync.Mutex
	uris []string
}

func (d *cleaningDaemon) handler(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.uris = append(d.uris, r.RequestURI)
	d.mu.Unlock()
	if cleaned := path.Clean(r.URL.Path); cleaned != r.URL.Path {
		w.Header().Set("Location", cleaned)
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (d *cleaningDaemon) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.uris...)
}

// Each path decodes to an unclean one that, once cleaned, is a stats route the
// stream limit must cover.
var uncleanStatsPaths = []string{
	"/v1.47/containers/abc%2F/stats",
	"/v1.47/containers/%2Fabc/stats",
	"/v1.47/containers/abc/%2E/stats",
	"/v1.47/containers/abc/%2E%2E/abc/stats",
	"/v1.47/containers/abc//stats",
}

func TestProxyReturnsDaemonCleanPathRedirect(t *testing.T) {
	t.Parallel()

	for _, p := range uncleanStatsPaths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			daemon := &cleaningDaemon{}
			s, _ := newStreamRouteServer(t, daemon.handler, 5, 4)

			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			s.handleDockerProxy(rec, req)

			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want the daemon's 301 handed back to the caller", rec.Code)
			}
			if got := daemon.seen(); len(got) != 1 {
				t.Fatalf("daemon saw %v, want exactly the one unclean request and no followed redirect", got)
			}
		})
	}
}

// Through the full server with the stream limit full, an unclean spelling of a
// stats route is never served the cleaned stream: it is either refused by the
// limit (the name over-matches the daemon's {name:.*} route) or handed a
// redirect, whichever layer answers.
func TestProxyNeverServesCleanedStatsPastStreamLimit(t *testing.T) {
	t.Parallel()

	for _, p := range uncleanStatsPaths {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			daemon := &cleaningDaemon{}
			s, ts := newStreamRouteServer(t, daemon.handler, 5, 1)
			fillStreamSlot(t, s)

			status := rawProxyRequest(t, ts, http.MethodGet, p, nil)

			if status == http.StatusOK {
				t.Fatalf("status = %d, want a refusal or a redirect, never the stream", status)
			}
			for _, uri := range daemon.seen() {
				if cleaned := path.Clean(strings.SplitN(uri, "?", 2)[0]); strings.HasSuffix(uri, "/stats") && cleaned == uri {
					t.Fatalf("daemon served the cleaned stats path %q past the stream limit", uri)
				}
			}
		})
	}
}

// rawTCPRequest writes request bytes to the server as is, so a '#' in the
// request target survives a client that would parse it as a fragment.
func rawTCPRequest(t *testing.T, ts *httptest.Server, target string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	raw := "GET " + target + " HTTP/1.1\r\nHost: portwing\r\n" + headerPortwingToken + ": proxy-secret\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// stream=0 after a '#' is part of the query Portwing classified on, so the
// daemon must receive it, or it would stream a request that skipped the limit.
func TestProxyForwardsHashInQueryWhole(t *testing.T) {
	t.Parallel()

	const target = "/v1.47/containers/abc/stats?a=b#&stream=0"
	rec := &proxyRecorder{}
	s, ts := newStreamRouteServer(t, rec.handler, 5, 1)
	fillStreamSlot(t, s)

	if status := rawTCPRequest(t, ts, target); status != http.StatusOK {
		t.Fatalf("status = %d, want the daemon's 200 (stream=0 is not a stream)", status)
	}

	got := rec.snapshot()
	if len(got) != 1 || got[0].requestURI != target {
		t.Fatalf("daemon saw %+v, want one request with the full target %q", got, target)
	}
}

func TestProxyForwardsEmptyQueryMarker(t *testing.T) {
	t.Parallel()

	const target = "/v1.47/containers/json?"
	rec := &proxyRecorder{}
	_, ts := newStreamRouteServer(t, rec.handler, 5, 1)

	if status := rawTCPRequest(t, ts, target); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	got := rec.snapshot()
	if len(got) != 1 || got[0].requestURI != target {
		t.Fatalf("daemon saw %+v, want one request for %q", got, target)
	}
}
