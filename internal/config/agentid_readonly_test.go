// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExistingAgentIDInReadOnlyStateDirRefusesV3(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-id"), []byte("existing-id-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- read-only private test fixture.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// #nosec G302 -- restore owner write permission for cleanup.
		_ = os.Chmod(dir, 0o700)
	})
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced here (running as root?)")
	}
	setEnv(t, "STATE_DIR", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentID != "existing-id-1" || cfg.SignatureV3Unavailable != ReasonStateNotWritable {
		t.Fatalf("cfg = %q / %q; want the existing ID with %q", cfg.AgentID, cfg.SignatureV3Unavailable, ReasonStateNotWritable)
	}
}
