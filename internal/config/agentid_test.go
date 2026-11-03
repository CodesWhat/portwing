// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentIDEnvOverrideLegacyValuesStillStart(t *testing.T) {
	for _, legacy := range []string{"Upper", "has_underscore", "has space", strings.Repeat("a", 65), "dot.ted"} {
		t.Run(legacy, func(t *testing.T) {
			setEnv(t, "AGENT_ID", legacy, "STATE_DIR", t.TempDir())
			cfg, err := Load()
			if err != nil {
				t.Fatalf("an AGENT_ID accepted before v1.1 must not stop startup: %v", err)
			}
			if cfg.AgentID != legacy {
				t.Fatalf("AgentID = %q, want the configured value", cfg.AgentID)
			}
			if cfg.SignatureV3Unavailable != ReasonAgentIDNotV3Compatible {
				t.Fatalf("SignatureV3Unavailable = %q, want %q", cfg.SignatureV3Unavailable, ReasonAgentIDNotV3Compatible)
			}
		})
	}
}

func TestLoadAgentIDEnvOverrideCompatibleEnablesV3(t *testing.T) {
	for _, ok := range []string{"a", "agent-1", strings.Repeat("a", 64)} {
		setEnv(t, "AGENT_ID", ok, "STATE_DIR", t.TempDir())
		cfg, err := Load()
		if err != nil || cfg.AgentID != ok || cfg.SignatureV3Unavailable != "" {
			t.Fatalf("AGENT_ID=%q: cfg=%+v err=%v; want v3 available", ok, cfg, err)
		}
	}
}

func TestLoadAgentIDEnvOverrideWithUnwritableStateDirRefusesV3(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "AGENT_ID", "pinned-1", "STATE_DIR", filepath.Join(blocker, "state"))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("startup must not fail: %v", err)
	}
	if cfg.AgentID != "pinned-1" || cfg.SignatureV3Unavailable != ReasonStateNotWritable {
		t.Fatalf("cfg = %q / %q; want pinned-1 with %q (locks could not be durable)", cfg.AgentID, cfg.SignatureV3Unavailable, ReasonStateNotWritable)
	}
}

func TestLoadAgentIDEnvOverrideWithMissingDefaultStateDirRefusesV3(t *testing.T) {
	if _, err := os.Stat(DefaultStateDir); err == nil {
		t.Skip("default state directory exists on this machine")
	}
	setEnv(t, "AGENT_ID", "pinned-1", "STATE_DIR", "", "KEY_ROTATION_DIR", "")
	cfg, err := Load()
	if err != nil || cfg.SignatureV3Unavailable != ReasonStateNotWritable {
		t.Fatalf("cfg=%+v err=%v; want state-not-writable", cfg, err)
	}
}

func TestLoadAgentIDIncompatibleBeatsStateNotWritable(t *testing.T) {
	setEnv(t, "AGENT_ID", "Bad_ID", "STATE_DIR", filepath.Join(t.TempDir(), "f", "x"))
	cfg, err := Load()
	if err != nil || cfg.SignatureV3Unavailable != ReasonAgentIDNotV3Compatible {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestV3UnavailableLogLevel(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"not persistent, v3 not required", []string{"STATE_DIR", filepath.Join(blocker, "s")}, "level=INFO"},
		{"not persistent, min version 2", []string{"STATE_DIR", filepath.Join(blocker, "s"), "SIGNATURE_MIN_VERSION", "2"}, "level=INFO"},
		{"not persistent, min version 3", []string{"STATE_DIR", filepath.Join(blocker, "s"), "SIGNATURE_MIN_VERSION", "3"}, "level=WARN"},
		{"incompatible AGENT_ID, v3 not required", []string{"AGENT_ID", "Bad_ID", "STATE_DIR", t.TempDir()}, "level=INFO"},
		{"incompatible AGENT_ID, min version 3", []string{"AGENT_ID", "Bad_ID", "STATE_DIR", t.TempDir(), "SIGNATURE_MIN_VERSION", "3"}, "level=WARN"},
		{"state not writable, v3 not required", []string{"AGENT_ID", "ok-1", "STATE_DIR", filepath.Join(blocker, "s")}, "level=INFO"},
		{"state not writable, min version 3", []string{"AGENT_ID", "ok-1", "STATE_DIR", filepath.Join(blocker, "s"), "SIGNATURE_MIN_VERSION", "3"}, "level=WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLogs(t)
			setEnv(t, tc.env...)
			if _, err := Load(); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if !strings.Contains(out, "reason=") || !strings.Contains(out, tc.want) {
				t.Fatalf("log = %q, want a reason line at %s", out, tc.want)
			}
			if tc.want == "level=INFO" && strings.Contains(out, "level=WARN") {
				t.Fatalf("a deployment not using v3 must not see a new warning: %q", out)
			}
		})
	}
}

func TestLoadAgentIDEnvOverrideIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "AGENT_ID", "pinned-1", "STATE_DIR", dir)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-id")); !os.IsNotExist(err) {
		t.Fatalf("override must not write the agent ID file, stat err = %v", err)
	}
}

func TestLoadAgentIDPersistsAcrossLoads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	setEnv(t, "STATE_DIR", dir)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if first.SignatureV3Unavailable != "" || second.SignatureV3Unavailable != "" {
		t.Fatalf("persisted ID must offer v3, got %q", first.SignatureV3Unavailable)
	}
	if len(first.AgentID) != 32 || first.AgentID != second.AgentID {
		t.Fatalf("agent IDs %q and %q: want one stable 32-char ID", first.AgentID, second.AgentID)
	}
	if first.StateDir != dir {
		t.Fatalf("StateDir = %q, want %q", first.StateDir, dir)
	}
}

func TestLoadStateDirDefaultsToKeyRotationDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rot")
	setEnv(t, "KEY_ROTATION_DIR", dir, "AUTHORIZED_KEYS", "/tmp/keys", "DRYDOCK_URL", "", "STATE_DIR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != dir {
		t.Fatalf("StateDir = %q, want %q", cfg.StateDir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-id")); err != nil {
		t.Fatalf("agent ID not persisted beside rotation state: %v", err)
	}
}

func TestLoadAgentIDCorruptFileFailsStartup(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- private test directory.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-id"), []byte("BAD_ID\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "STATE_DIR", dir)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "agent ID") {
		t.Fatalf("Load() error = %v, want agent ID error", err)
	}
}

func TestLoadAgentIDUnwritableStateDirFallsBack(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "STATE_DIR", filepath.Join(blocker, "state"))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("an unusable state dir must not block startup: %v", err)
	}
	if len(cfg.AgentID) != 32 {
		t.Fatalf("fallback AgentID = %q", cfg.AgentID)
	}
	if cfg.SignatureV3Unavailable != ReasonAgentIDNotPersistent {
		t.Fatalf("SignatureV3Unavailable = %q, want %q", cfg.SignatureV3Unavailable, ReasonAgentIDNotPersistent)
	}
}

func TestLoadSignatureMinVersion(t *testing.T) {
	for raw, want := range map[string]int{"": 1, "1": 1, "2": 2, "3": 3} {
		setEnv(t, "SIGNATURE_MIN_VERSION", raw, "STATE_DIR", t.TempDir())
		cfg, err := Load()
		if err != nil || cfg.SignatureMinVersion != want {
			t.Errorf("SIGNATURE_MIN_VERSION=%q: got %d, %v; want %d", raw, cfg.SignatureMinVersion, err, want)
		}
	}
	for _, bad := range []string{"0", "4", "v3", "abc", "-1", " 3"} {
		setEnv(t, "SIGNATURE_MIN_VERSION", bad, "STATE_DIR", t.TempDir())
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SIGNATURE_MIN_VERSION") {
			t.Errorf("SIGNATURE_MIN_VERSION=%q: err = %v, want validation error", bad, err)
		}
	}
}
