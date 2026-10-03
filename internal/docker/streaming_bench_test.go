package docker

import (
	"net/http"
	"testing"
)

// BenchmarkIsStreamingRequest covers the per-request classification the proxy
// runs on every Docker API call: a versioned stats route, a plain JSON route
// that falls through every check, and an encoded path.
func BenchmarkIsStreamingRequest(b *testing.B) {
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"stats", http.MethodGet, "/v1.47/containers/0123456789ab/stats"},
		{"list", http.MethodGet, "/v1.47/containers/json?all=1"},
		{"inspect", http.MethodGet, "/v1.47/containers/0123456789ab/json"},
		{"encoded", http.MethodGet, "/v1.47/images/library%2Fnginx/get"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				IsStreamingRequest(tc.method, tc.path)
			}
		})
	}
}
