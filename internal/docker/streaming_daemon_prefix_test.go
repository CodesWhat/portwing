package docker

import (
	"net/http"
	"testing"
)

// The Docker daemon's router accepts a version prefix of "/v" followed by any
// run of digits and dots ("/v1.47.0/", "/v1/"), not only MAJOR.MINOR (moby
// daemon/server/server.go, versionMatcher). The router sends these requests to
// the same handler as their canonical form (whether the version is then
// supported is decided after routing), so they must be classified the same way.

func TestIsStreamingRequestDaemonAcceptedPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"stats under three-part prefix", http.MethodGet, "/v1.47.0/containers/abc/stats"},
		{"stats under major-only prefix", http.MethodGet, "/v1/containers/abc/stats"},
		{"stats under four-part prefix", http.MethodGet, "/v1.47.0.0/containers/abc/stats"},
		{"push under three-part prefix", http.MethodPost, "/v1.47.0/images/nginx/push"},
		{"push under major-only prefix", http.MethodPost, "/v1/images/nginx/push"},
		{"stats under dots-only prefix", http.MethodGet, "/v./containers/abc/stats"},
		{"stats under trailing-dot prefix", http.MethodGet, "/v1../containers/abc/stats"},
		{"push under four-part prefix", http.MethodPost, "/v1.47.0.1/images/nginx/push"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !IsStreamingRequest(tc.method, tc.path) {
				t.Fatalf("IsStreamingRequest(%s, %q) = false, want true: the daemon routes this like its canonical form", tc.method, tc.path)
			}
		})
	}
}
