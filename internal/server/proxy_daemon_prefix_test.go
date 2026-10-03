package server

// proxy_daemon_prefix_test.go pins that requests the Docker daemon routes like
// their canonical form are classified like it by Portwing: a version prefix of
// "/v" followed by any run of digits and dots ("/v1.47.0/", "/v1/"), and
// percent-encoded letters in a route word ("/%73tats"). The daemon routes on
// the decoded path (gorilla/mux without UseEncodedPath), so exec and attach
// hijack handling, the exec audit record and the exec and stream session limits
// must follow the decoded path and the same prefix shape, or a spelling of a
// guarded route would escape them.

import (
	"net/http"
	"testing"

	"github.com/codeswhat/portwing/internal/audit"
)

func TestIsDockerHijackPathDaemonAcceptedPrefixes(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/v1.47.0/exec/abc/start",
		"/v1/exec/abc/start",
		"/v1.47.0/containers/abc/attach",
		"/v1/containers/abc/attach",
		"/v1.47.0.1/exec/abc/start",
		"/v./exec/abc/start",
		"/v1../containers/abc/attach",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if !isDockerHijackPath(path) {
				t.Fatalf("isDockerHijackPath(%q) = false, want true: the daemon routes it like the canonical form", path)
			}
		})
	}
}

func TestProxyStreamLimitAppliesToDaemonAcceptedSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"stats under three-part prefix", http.MethodGet, "/v1.47.0/containers/abc/stats"},
		{"stats under major-only prefix", http.MethodGet, "/v1/containers/abc/stats"},
		{"stats with an encoded letter", http.MethodGet, "/v1.47/containers/abc/%73tats"},
		{"stats under dots-only prefix", http.MethodGet, "/v./containers/abc/stats"},
		{"push under three-part prefix", http.MethodPost, "/v1.47.0/images/nginx/push"},
		{"push with an encoded letter", http.MethodPost, "/v1.47/images/nginx/pus%68"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			s, ts := newStreamRouteServer(t, rec.handler, 5, 1)
			fillStreamSlot(t, s)

			status := rawProxyRequest(t, ts, tc.method, tc.path, nil)

			if status != http.StatusServiceUnavailable || len(rec.snapshot()) != 0 {
				t.Fatalf("status = %d, daemon saw %d requests, want 503 and none: the spelling dodged the stream limit",
					status, len(rec.snapshot()))
			}
		})
	}
}

func TestProxyExecLimitAndAuditApplyToDaemonAcceptedPrefixes(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/v1.47.0/exec/abc/start", "/v1/exec/abc/start", "/v./exec/abc/start"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			s, ts := newStreamRouteServer(t, rec.handler, 5, 4)
			s.execSem = newConcurrencyLimiter(1)
			if !s.execSem.acquire() {
				t.Fatal("exec slot already taken")
			}
			defer s.execSem.release()

			status := rawProxyRequest(t, ts, http.MethodPost, path, http.Header{"Upgrade": {"tcp"}, "Connection": {"Upgrade"}})

			execRecords := 0
			for _, record := range s.auditor.Records(100) {
				if record.Event == audit.EventExecStart {
					execRecords++
				}
			}
			if status != http.StatusServiceUnavailable || execRecords != 1 || len(rec.snapshot()) != 0 {
				t.Fatalf("status = %d, exec audit records = %d, daemon saw %d requests; want 503, 1, 0: the spelling dodged the exec limit and audit",
					status, execRecords, len(rec.snapshot()))
			}
		})
	}
}
