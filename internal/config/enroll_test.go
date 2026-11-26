// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/auth"
)

const testEnrollCode = "Zx9-test_enroll-CODE-0123456789"

func TestEnrollFirstStartThenLaterStartWithoutCode(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir, "ENROLL_CODE", testEnrollCode)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	want := filepath.Join(dir, auth.EnrolledKeyFileName)
	if cfg.PrivateKeyFile != want {
		t.Fatalf("PrivateKeyFile = %q, want %q", cfg.PrivateKeyFile, want)
	}
	if cfg.EnrollCode != testEnrollCode || cfg.EnrollMarkerPath == "" {
		t.Fatalf("first start must be enrolling: %+v", cfg)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %04o, want 0600", info.Mode().Perm())
	}
	firstKey, _ := os.ReadFile(want)

	// Restart before the welcome: same key, code sent again.
	cfg, err = Load()
	if err != nil {
		t.Fatalf("restart while pending: %v", err)
	}
	if again, _ := os.ReadFile(want); string(again) != string(firstKey) {
		t.Fatal("pending key was regenerated")
	}
	if cfg.EnrollCode != testEnrollCode {
		t.Fatal("pending key must resend the code")
	}

	// Confirm, then restart without the code.
	if err := auth.ConfirmEnrolledKey(cfg.EnrollMarkerPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENROLL_CODE", "")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("later start without code: %v", err)
	}
	if cfg.PrivateKeyFile != want || cfg.EnrollCode != "" || cfg.EnrollMarkerPath != "" {
		t.Fatalf("confirmed key must connect normally: %+v", cfg)
	}

	// A code left in the environment is ignored once the key is confirmed.
	t.Setenv("ENROLL_CODE", testEnrollCode)
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnrollCode != "" {
		t.Fatal("confirmed key must ignore ENROLL_CODE")
	}
}

func TestEnrollExplicitPrivateKeyFileWins(t *testing.T) {
	dir := t.TempDir()
	privPEM, _, err := auth.GenerateKeyPair("t")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "mine.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir,
		"PRIVATE_KEY_FILE", keyPath, "ENROLL_CODE", testEnrollCode)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PrivateKeyFile != keyPath || cfg.EnrollCode != "" || cfg.EnrollMarkerPath != "" {
		t.Fatalf("explicit key must win: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, auth.EnrolledKeyFileName)); !os.IsNotExist(err) {
		t.Fatal("no enrolled key may be generated when PRIVATE_KEY_FILE is set")
	}
}

func TestEnrollNoKeyNoCodeMentionsEnrollCode(t *testing.T) {
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir())
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ENROLL_CODE") {
		t.Fatalf("want an error naming ENROLL_CODE, got %v", err)
	}
}

func TestEnrollCodeFile(t *testing.T) {
	codeFile := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(codeFile, []byte(testEnrollCode+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(), "ENROLL_CODE_FILE", codeFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnrollCode != testEnrollCode {
		t.Fatal("code not read from ENROLL_CODE_FILE")
	}
}

func TestEnrollCodeValidation(t *testing.T) {
	for name, code := range map[string]string{
		"short":    "abc",
		"long":     strings.Repeat("a", 129),
		"badchars": "has spaces in the code!!!!",
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(), "ENROLL_CODE", code)
			_, err := Load()
			if err == nil {
				t.Fatal("want a validation error")
			}
			if strings.Contains(err.Error(), code) {
				t.Fatal("error leaks the code")
			}
		})
	}
	t.Run("both", func(t *testing.T) {
		setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(),
			"ENROLL_CODE", testEnrollCode, "ENROLL_CODE_FILE", "/nonexistent")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("want mutually exclusive error, got %v", err)
		}
	})
}

func TestEnrollPlaintextRefusedUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "DRYDOCK_URL", "ws://drydock.local", "STATE_DIR", dir, "ENROLL_CODE", testEnrollCode,
		"ALLOW_INSECURE_EDGE_URL", "true")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ALLOW_INSECURE_ENROLLMENT") {
		t.Fatalf("want a plaintext enrollment refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, auth.EnrolledKeyFileName)); !os.IsNotExist(statErr) {
		t.Fatal("no key may be generated when the scheme is refused")
	}

	t.Setenv("ALLOW_INSECURE_ENROLLMENT", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("allowed with the override: %v", err)
	}
	if cfg.EnrollCode != testEnrollCode {
		t.Fatal("expected enrolling config")
	}
}

func TestEnrollUnwritableStateDirRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // read-only directory fixture
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore the fixture so cleanup can remove it
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir, "ENROLL_CODE", testEnrollCode)
	if _, err := Load(); err == nil {
		t.Fatal("unwritable state dir must refuse enrollment")
	} else if strings.Contains(err.Error(), testEnrollCode) {
		t.Fatal("error leaks the code")
	}
}

func TestEnrollKeyRotationDirAcceptsEnrolledKey(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir,
		"KEY_ROTATION_DIR", dir, "ENROLL_CODE", testEnrollCode)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("KEY_ROTATION_DIR with an enrolled key: %v", err)
	}
	if cfg.PrivateKeyFile == "" || cfg.KeyRotationDir != dir {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestExplicitKeyIgnoresInvalidEnrollConfig(t *testing.T) {
	privPEM, _, err := auth.GenerateKeyPair("t")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "mine.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"invalid code":     {"ENROLL_CODE", "abc"},
		"unreadable file":  {"ENROLL_CODE_FILE", "/nonexistent"},
		"conflicting vars": {"ENROLL_CODE", testEnrollCode, "ENROLL_CODE_FILE", "/nonexistent"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			setEnv(t, append([]string{"DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(), "PRIVATE_KEY_FILE", keyPath}, kv...)...)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("explicit key must win over bad enrollment config: %v", err)
			}
			if cfg.PrivateKeyFile != keyPath || cfg.EnrollCode != "" {
				t.Fatalf("unexpected cfg: %+v", cfg)
			}
		})
	}
}

func TestEnrollInsecureTransportRefused(t *testing.T) {
	codeFile := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(codeFile, []byte(testEnrollCode+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources := map[string][]string{
		"ENROLL_CODE":      {"ENROLL_CODE", testEnrollCode},
		"ENROLL_CODE_FILE": {"ENROLL_CODE_FILE", codeFile},
	}
	cases := []struct {
		name      string
		url       string
		skip      string
		allow     string
		wantError string // empty means accepted
	}{
		{"https verified", "https://drydock.example.com", "", "", ""},
		{"wss verified", "wss://drydock.example.com", "", "", ""},
		{"https skip verify refused", "https://drydock.example.com", "true", "", "TLS_SKIP_VERIFY"},
		{"wss skip verify refused", "wss://drydock.example.com", "true", "", "TLS_SKIP_VERIFY"},
		{"https skip verify allowed", "https://drydock.example.com", "true", "true", ""},
		{"wss skip verify allowed", "wss://drydock.example.com", "true", "true", ""},
		{"ws refused", "ws://drydock.local", "", "", "ALLOW_INSECURE_ENROLLMENT"},
		{"http refused", "http://drydock.local", "", "", "ALLOW_INSECURE_ENROLLMENT"},
		{"ws allowed", "ws://drydock.local", "", "true", ""},
		{"http allowed", "http://drydock.local", "", "true", ""},
	}
	for srcName, src := range sources {
		for _, tc := range cases {
			t.Run(srcName+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				kv := []string{"DRYDOCK_URL", tc.url, "STATE_DIR", dir, "ALLOW_INSECURE_EDGE_URL", "true",
					"TLS_SKIP_VERIFY", tc.skip, "ALLOW_INSECURE_ENROLLMENT", tc.allow}
				setEnv(t, append(kv, src...)...)
				cfg, err := Load()
				if tc.wantError == "" {
					if err != nil {
						t.Fatalf("want accepted, got %v", err)
					}
					if cfg.EnrollCode != testEnrollCode {
						t.Fatal("expected enrolling config")
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error naming %s, got %v", tc.wantError, err)
				}
				if strings.Contains(err.Error(), testEnrollCode) {
					t.Fatal("error leaks the code")
				}
				for _, name := range []string{auth.EnrolledKeyFileName, auth.EnrolledPendingFileName} {
					if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
						t.Fatalf("%s must not exist after a refusal", name)
					}
				}
			})
		}
	}
}

func TestEnrollSkipVerifyExplicitKeyUnaffected(t *testing.T) {
	privPEM, _, err := auth.GenerateKeyPair("t")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "mine.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(),
		"PRIVATE_KEY_FILE", keyPath, "ENROLL_CODE", testEnrollCode, "TLS_SKIP_VERIFY", "true")
	if _, err := Load(); err != nil {
		t.Fatalf("explicit key with skip verify must load: %v", err)
	}
}

func TestEnrollStandardModeIgnoresEnrollConfig(t *testing.T) {
	cases := map[string][]string{
		"valid code":       {"ENROLL_CODE", testEnrollCode},
		"invalid code":     {"ENROLL_CODE", "abc"},
		"unreadable file":  {"ENROLL_CODE_FILE", "/nonexistent"},
		"conflicting vars": {"ENROLL_CODE", testEnrollCode, "ENROLL_CODE_FILE", "/nonexistent"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setEnv(t, append([]string{"TOKEN", "standard-mode-token", "STATE_DIR", dir}, kv...)...)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("standard mode must ignore enrollment config: %v", err)
			}
			if cfg.EnrollCode != "" {
				t.Fatal("standard mode must not carry an enrollment code")
			}
			if _, statErr := os.Stat(filepath.Join(dir, auth.EnrolledKeyFileName)); !os.IsNotExist(statErr) {
				t.Fatal("no key may be generated in standard mode")
			}
		})
	}
}

func TestEnrollConfirmedKeyIgnoresMissingCodeFile(t *testing.T) {
	dir := t.TempDir()
	codeFile := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(codeFile, []byte(testEnrollCode+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir, "ENROLL_CODE_FILE", codeFile)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := cfg.PrivateKeyFile
	if err := auth.ConfirmEnrolledKey(cfg.EnrollMarkerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(codeFile); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatalf("confirmed key must not need the removed code file: %v", err)
	}
	if cfg.PrivateKeyFile != want || cfg.EnrollCode != "" || cfg.EnrollMarkerPath != "" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestEnrollPendingKeyRestartWithoutCode(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", dir, "ENROLL_CODE", testEnrollCode)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if first.EnrollMarkerPath == "" {
		t.Fatal("first start must report the pending marker")
	}

	t.Setenv("ENROLL_CODE", "")
	second, err := Load()
	if err != nil {
		t.Fatalf("restart without code: %v", err)
	}
	secondBytes, _ := os.ReadFile(second.PrivateKeyFile)
	if second.PrivateKeyFile != first.PrivateKeyFile || string(secondBytes) != string(firstBytes) {
		t.Fatal("pending key must be reused")
	}
	if second.EnrollCode != "" {
		t.Fatal("no code configured, none may be carried")
	}
	if second.EnrollMarkerPath == "" || second.EnrollMarkerPath != first.EnrollMarkerPath {
		t.Fatalf("pending key must keep its marker, got %q", second.EnrollMarkerPath)
	}
	if _, err := os.Stat(second.EnrollMarkerPath); err != nil {
		t.Fatalf("marker must still exist: %v", err)
	}
}

func TestEnrollDefaultStateDirMissingRefuses(t *testing.T) {
	if _, err := os.Stat(DefaultStateDir); err == nil {
		t.Skipf("%s exists on this host", DefaultStateDir)
	}
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "ENROLL_CODE", testEnrollCode,
		"STATE_DIR", "", "KEY_ROTATION_DIR", "", "PRIVATE_KEY_FILE", "", "ENROLL_CODE_FILE", "")
	_, err := Load()
	if err == nil {
		t.Fatal("a missing default state dir must refuse enrollment")
	}
	for _, want := range []string{DefaultStateDir, "STATE_DIR"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), testEnrollCode) {
		t.Fatal("error leaks the code")
	}
}

func TestResolveEnrolledKeyMissingImplicitDirRefuses(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "credentials")
	_, err := resolveEnrolledKey(missing, false, testEnrollCode)
	if err == nil {
		t.Fatal("an implicit state dir that does not exist must refuse")
	}
	if strings.Contains(err.Error(), testEnrollCode) {
		t.Fatal("error leaks the code")
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatal("the directory must not be created")
	}
}

func TestEnrollCodeFileUnreadable(t *testing.T) {
	setEnv(t, "DRYDOCK_URL", "https://drydock.example.com", "STATE_DIR", t.TempDir(),
		"ENROLL_CODE_FILE", filepath.Join(t.TempDir(), "missing"))
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "reading ENROLL_CODE_FILE") {
		t.Fatalf("want a read error naming ENROLL_CODE_FILE, got %v", err)
	}
}
