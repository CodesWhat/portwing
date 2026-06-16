package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkMCPHandler measures the JSON-RPC dispatch hot path for the methods
// that don't touch Docker — the envelope decode, version check, method switch,
// and response encode. A nil docker client is safe here for the same reason it
// is in FuzzMCPHandler: none of these methods reach it.
func BenchmarkMCPHandler(b *testing.B) {
	h := &Handler{docker: nil, collector: nil}

	cases := []struct {
		name string
		body string
	}{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`},
		{"tools_list", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`},
		{"ping", `{"jsonrpc":"2.0","id":3,"method":"ping"}`},
		{"parse_error", `not json at all`},
		{"method_not_found", `{"jsonrpc":"2.0","id":4,"method":"unknown/method"}`},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(c.body))
				req.Header.Set("Content-Type", "application/json")
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
