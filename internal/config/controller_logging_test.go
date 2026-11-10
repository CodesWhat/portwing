// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestControllerDiagnosticsRedactCredentials(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "refused"
		if allow {
			name = "explicitly allowed"
		}
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			raw := "http://hidden-user:hidden-password@controller.example/base?credential=query-secret#fragment-secret"
			optIn := "false"
			if allow {
				optIn = "true"
			}
			setEnv(t, "DRYDOCK_URL", raw, "PRIVATE_KEY_FILE", "/unused/key.pem",
				"ALLOW_INSECURE_EDGE_URL", optIn, "STATE_DIR", t.TempDir())
			cfg, err := Load()
			if allow {
				if err != nil {
					t.Fatal(err)
				}
				if cfg.DrydockURL != raw {
					t.Fatal("diagnostic redaction changed the connection URL")
				}
			} else if err == nil {
				t.Fatal("plaintext controller was accepted without the opt-in")
			}
			diagnostic := logs.String()
			if err != nil {
				diagnostic += err.Error()
			}
			for _, secret := range []string{"hidden-user", "hidden-password", "query-secret", "fragment-secret"} {
				if strings.Contains(diagnostic, secret) {
					t.Errorf("controller diagnostic exposed %q: %s", secret, diagnostic)
				}
			}
			if !strings.Contains(diagnostic, "controller.example") {
				t.Fatal("diagnostic lost the controller address")
			}
		})
	}
}
