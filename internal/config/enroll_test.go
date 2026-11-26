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
