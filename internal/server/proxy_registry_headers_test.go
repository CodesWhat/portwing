package server

// proxy_registry_headers_test.go pins header fidelity on the registry-bearing
// Docker routes. Drydock builds and pushes through the proxy with credentials
// in X-Registry-Auth and X-Registry-Config. Those must reach the daemon
// unchanged, and must never be written to audit records. Portwing's own auth
// header must not reach the daemon.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestProxyForwardsRegistryHeadersUnchangedAndKeepsThemOutOfAudit(t *testing.T) {
	t.Parallel()

	// Base64 JSON with padding and URL-safe characters, as the Docker CLI sends.
	// Built at runtime so no credential-shaped literal sits in the source.
	registryAuth := base64.URLEncoding.EncodeToString([]byte(`{"username":"pw-user","password":"pw-secret-password-?_-"}`))
	registryConfig := base64.URLEncoding.EncodeToString([]byte(`{"registry.example":{"username":"pw-user","password":"pw-config-secret"}}`))
	tests := []struct {
		name       string
		method     string
		path       string
		header     http.Header
		wantConfig bool
	}{
		{
			name:       "build carries both headers",
			method:     http.MethodPost,
			path:       "/v1.47/build?t=app%3Alatest&dockerfile=Dockerfile",
			header:     http.Header{"X-Registry-Auth": {registryAuth}, "X-Registry-Config": {registryConfig}},
			wantConfig: true,
		},
		{
			name:   "image create carries auth",
			method: http.MethodPost,
			path:   "/v1.47/images/create?fromImage=registry.example%2Fteam%2Fapp&tag=1",
			header: http.Header{"X-Registry-Auth": {registryAuth}},
		},
		{
			name:   "image push carries auth",
			method: http.MethodPost,
			path:   "/v1.47/images/registry.example/team/app/push?tag=1",
			header: http.Header{"X-Registry-Auth": {registryAuth}},
		},
		{
			name:   "unversioned image push carries auth",
			method: http.MethodPost,
			path:   "/images/registry.example/team/app/push",
			header: http.Header{"X-Registry-Auth": {registryAuth}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &proxyRecorder{}
			s, ts := newStreamRouteServer(t, rec.handler, 5, 4)

			status := rawProxyRequest(t, ts, tc.method, tc.path, tc.header)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}

			got := rec.snapshot()
			if len(got) != 1 {
				t.Fatalf("daemon saw %d requests, want 1", len(got))
			}
			if got[0].requestURI != tc.path {
				t.Errorf("daemon request URI = %q, want %q", got[0].requestURI, tc.path)
			}
			if v := got[0].header.Get("X-Registry-Auth"); v != registryAuth {
				t.Errorf("X-Registry-Auth = %q, want it unchanged", v)
			}
			wantConfig := ""
			if tc.wantConfig {
				wantConfig = registryConfig
			}
			if v := got[0].header.Get("X-Registry-Config"); v != wantConfig {
				t.Errorf("X-Registry-Config = %q, want %q", v, wantConfig)
			}
			if v := got[0].header.Get(headerPortwingToken); v != "" {
				t.Errorf("Portwing auth header reached the daemon as %q", v)
			}

			records := s.auditor.Records(100)
			if len(records) == 0 {
				t.Fatal("no audit records written, the leak check below would pass vacuously")
			}
			encoded, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{registryAuth, registryConfig, "proxy-secret"} {
				if strings.Contains(string(encoded), secret) {
					t.Errorf("audit records contain credential %q: %s", secret, encoded)
				}
			}
		})
	}
}
