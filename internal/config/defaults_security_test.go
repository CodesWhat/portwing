// SPDX-License-Identifier: MIT

package config

import "testing"

// TLS_SKIP_VERIFY and ALLOW_UNAUTHENTICATED weaken the agent's security, so
// they must be off unless an operator turns them on. Unset, empty and
// unparseable values all fall back to false.
func TestLoadSecurityOptOutsDefaultToFalse(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"unset", ""},
		{"unparseable", "maybe"},
		{"explicit false", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "TOKEN", "secret", "TLS_SKIP_VERIFY", tc.value, "ALLOW_UNAUTHENTICATED", tc.value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TLSSkipVerify {
				t.Error("TLSSkipVerify = true, want false")
			}
			if cfg.AllowUnauthenticated {
				t.Error("AllowUnauthenticated = true, want false")
			}
		})
	}
}

// The same flags do turn on when asked, so the default test above is not
// passing because the fields never change.
func TestLoadSecurityOptOutsHonorExplicitTrue(t *testing.T) {
	setEnv(t, "TOKEN", "secret", "TLS_SKIP_VERIFY", "true", "ALLOW_UNAUTHENTICATED", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TLSSkipVerify || !cfg.AllowUnauthenticated {
		t.Fatalf("TLSSkipVerify = %v, AllowUnauthenticated = %v, want both true", cfg.TLSSkipVerify, cfg.AllowUnauthenticated)
	}
}
