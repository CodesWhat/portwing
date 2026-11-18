// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestLoadClientCARequiresNativeStandardTLS(t *testing.T) {
	for _, tc := range []struct{ name, cert, key, controller string }{
		{name: "plaintext"},
		{name: "edge", cert: "server.crt", key: "server.key", controller: "https://controller.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "TLS_CLIENT_CA", "client-ca.pem", "TLS_CERT", tc.cert, "TLS_KEY", tc.key, "DRYDOCK_URL", tc.controller, "TOKEN", "test-credential", "TOKEN_HASH", "", "PRIVATE_KEY_FILE", "controller.key")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "TLS_CLIENT_CA") {
				t.Fatalf("Load() error=%v, want unsupported client-CA configuration rejected", err)
			}
		})
	}
}
