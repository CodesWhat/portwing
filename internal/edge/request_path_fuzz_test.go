// SPDX-License-Identifier: MIT

package edge

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/docker"
)

// FuzzEdgeRequestPath feeds arbitrary method and path strings through the
// classifiers handleRequestTo runs before a request reaches the daemon: the
// leading-slash gate, the raw exec start match, the streaming match and the
// audit path. None may panic, and the ones that gate admission must agree with
// the rules the rest of the handler relies on.
func FuzzEdgeRequestPath(f *testing.F) {
	for _, seed := range []struct{ method, path string }{
		{"GET", "/containers/json"},
		{"GET", "/v1.44/containers/json?all=1"},
		{"POST", "/exec/abc/start"},
		{"POST", "/v1.44/exec/abc/start"},
		{"POST", "/v1.44/../exec/abc/start"},
		{"POST", "/exec/./abc/start"},
		{"POST", "/exec/%2E%2E/abc/start"},
		{"GET", "/containers/abc/stats?stream=1"},
		{"GET", "/containers/abc/%2E%2E/abc/stats"},
		{"GET", "/images/../containers/json"},
		{"GET", "containers/json"},
		{"GET", ""},
		{"POST", "/exec//start"},
		{"POST", "/%zz/exec/a/start"},
		{"", "/"},
	} {
		f.Add(seed.method, seed.path)
	}

	f.Fuzz(func(t *testing.T, method, path string) {
		if gateErr := docker.ValidateAPIPath(path); (gateErr == nil) != strings.HasPrefix(path, "/") {
			t.Fatalf("ValidateAPIPath(%q) = %v, want an error exactly when there is no leading slash", path, gateErr)
		}

		if audited := auditPath(path); strings.Contains(audited, "?") || !strings.HasPrefix(path, audited) {
			t.Fatalf("auditPath(%q) = %q, want the path with its query dropped", path, audited)
		}

		_, routePath, ok := rawExecStart(method, path)
		if ok {
			if method != http.MethodPost {
				t.Fatalf("rawExecStart(%q, %q) matched a non-POST", method, path)
			}
			if !strings.HasSuffix(routePath, "/start") || !strings.Contains(routePath, "/exec/") {
				t.Fatalf("rawExecStart(%q, %q) matched route %q, which is not an exec start", method, path, routePath)
			}
			if strings.Contains(routePath, "?") {
				t.Fatalf("rawExecStart(%q, %q) route %q still carries a query", method, path, routePath)
			}
		}

		_ = docker.IsStreamingRequest(method, path)
	})
}
