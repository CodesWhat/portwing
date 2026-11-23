// SPDX-License-Identifier: MIT

package edge

// dot_segment_test.go pins how the Edge request path treats "." and ".."
// segments, against Standard mode's behaviour (proxy_version_prefix_test.go and
// proxy_redirect_query_test.go in internal/server). Standard never serves an
// unclean path: its mux, or the daemon's cleaning router behind it, answers
// with a cleaned-path 301 that is handed back and not followed. Edge has no mux
// of its own, so the daemon's router is the cleaner. It must see the path
// verbatim, relay the 301 unfollowed, and never let the spelling dodge exec admission: a
// dot-dot that hides an exec start isn't one, and a dot segment inside the name
// is counted as one, as Standard's {name:.*} matching does.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// edgeCleaningDaemon mirrors the daemon's router: it 301s any path that differs
// from its cleaned form, and records each request URI it receives.
type edgeCleaningDaemon struct {
	mu   sync.Mutex
	uris []string
}

func (d *edgeCleaningDaemon) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/version" {
		_, _ = w.Write([]byte(`{"ApiVersion":"1.44"}`))
		return
	}
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

func (d *edgeCleaningDaemon) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.uris...)
}

func newEdgeCleaningDockerClient(t *testing.T, d *edgeCleaningDaemon) *docker.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "ds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(d.handler))
	_ = srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	dc, err := docker.NewClient(socket, 10)
	if err != nil {
		t.Fatal(err)
	}
	return dc
}

func TestEdgeDotSegmentPathsAreRelayedAsDaemonRedirects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		// execStart is true when the spelling is still counted as an exec
		// start: the daemon's {name:.*} route lets the id contain a slash, so
		// "./abc" is a name, as in Standard's isDockerHijackPath. Counting it
		// only takes an exec slot it didn't need, so it can't bypass the cap.
		execStart bool
	}{
		{"dot-dot segment", http.MethodGet, "/images/../containers/json", false},
		{"dot-dot out of the version prefix", http.MethodGet, "/../containers/json", false},
		{"single dot segment", http.MethodGet, "/./containers/json", false},
		{"encoded dot segment", http.MethodGet, "/containers/abc/%2E/json", false},
		{"encoded dot-dot segment", http.MethodGet, "/containers/abc/%2E%2E/abc/json", false},
		{"dot-dot hiding an exec start", http.MethodPost, "/../exec/abc/start", false},
		{"dot segment inside an exec start", http.MethodPost, "/exec/./abc/start", true},
		{"dot segment before a stats route", http.MethodGet, "/containers/abc/./stats?stream=1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			daemon := &edgeCleaningDaemon{}
			c, logger, next := newAuditedTestClient(t)
			c.dockerClient = newEdgeCleaningDockerClient(t, daemon)

			c.handleRequest(context.Background(), protocol.RequestMessage{
				RequestID: "dot", Method: tc.method, Path: tc.path,
			})

			env := next()
			if env.Type != protocol.TypeResponse {
				t.Fatalf("frame = %q (%s), want a response", env.Type, env.Data)
			}
			var resp protocol.ResponseMessage
			decodeData(t, env.Data, &resp)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want the daemon's 301 relayed", resp.StatusCode)
			}
			if got := daemon.seen(); len(got) != 1 || got[0] != "/v1.44"+tc.path {
				t.Fatalf("daemon saw %v, want exactly the verbatim version-prefixed %q and no followed redirect", got, "/v1.44"+tc.path)
			}
			want := 0
			if tc.execStart {
				want = 1
			}
			if recs := recordsOfEvent(logger, audit.EventExecStart); len(recs) != want {
				t.Fatalf("exec_start records = %+v, want %d", recs, want)
			}
		})
	}
}

// A clean exec start is still classified and recorded, so the absence above is
// the dot segment's doing and not a blind spot in the audit assertion.
func TestEdgeCleanExecStartIsRecorded(t *testing.T) {
	t.Parallel()

	daemon := &edgeCleaningDaemon{}
	c, logger, next := newAuditedTestClient(t)
	c.dockerClient = newEdgeCleaningDockerClient(t, daemon)

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "clean", Method: http.MethodPost, Path: "/exec/abc/start",
	})
	next()

	if recs := recordsOfEvent(logger, audit.EventExecStart); len(recs) != 1 {
		t.Fatalf("exec_start records = %+v, want exactly one", recs)
	}
}
