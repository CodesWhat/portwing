// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestRotationRequiresConfiguredKey(t *testing.T) {
	for _, mode := range []string{"standard", "edge"} {
		t.Run(mode, func(t *testing.T) {
			setEnv(t, "KEY_ROTATION_DIR", t.TempDir(), "AUTHORIZED_KEYS", "", "AUTHORIZED_KEYS_FILE", "", "PRIVATE_KEY_FILE", "", "TOKEN", "example", "TOKEN_HASH", "", "DRYDOCK_URL", "")
			if mode == "edge" {
				t.Setenv("DRYDOCK_URL", "https://controller.example")
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "KEY_ROTATION_DIR") {
				t.Fatalf("rotation without a configured %s key must fail, got %v", mode, err)
			}
		})
	}
}
