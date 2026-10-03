package server

import "testing"

// BenchmarkIsDockerHijackPath covers the per-request hijack check the proxy
// runs on every Docker API call.
func BenchmarkIsDockerHijackPath(b *testing.B) {
	cases := []struct{ name, path string }{
		{"exec-start", "/v1.47/exec/0123456789ab/start"},
		{"attach", "/v1.47/containers/0123456789ab/attach"},
		{"list", "/v1.47/containers/json"},
		{"inspect", "/v1.47/containers/0123456789ab/json"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isDockerHijackPath(tc.path)
			}
		})
	}
}
