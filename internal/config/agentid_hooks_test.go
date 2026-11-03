// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStateDirPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stateDir     string
		rotationDir  string
		wantDir      string
		wantExplicit bool
	}{
		{"STATE_DIR wins", "/srv/state", "/srv/rot", "/srv/state", true},
		{"KEY_ROTATION_DIR next", "", "/srv/rot", "/srv/rot", true},
		{"image default last", "", "", DefaultStateDir, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, "STATE_DIR", tc.stateDir, "KEY_ROTATION_DIR", tc.rotationDir)
			dir, explicit := ResolveStateDir()
			if dir != tc.wantDir || explicit != tc.wantExplicit {
				t.Fatalf("ResolveStateDir = %q, %v; want %q, %v", dir, explicit, tc.wantDir, tc.wantExplicit)
			}
		})
	}
}

func TestLoadFailsWhenNoEntropyForGeneratedAgentID(t *testing.T) {
	real := generateAgentID
	generateAgentID = func() (string, error) { return "", errors.New("no entropy") }
	t.Cleanup(func() { generateAgentID = real })
	// No usable state dir and no AGENT_ID: the fallback ID cannot be made.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "STATE_DIR", filepath.Join(blocker, "state"))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "no entropy") {
		t.Fatalf("Load error = %v, want the entropy failure", err)
	}
}
