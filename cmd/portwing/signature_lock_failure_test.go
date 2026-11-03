// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignatureLockClearFailsWhenAuditLogCannotOpen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATE_DIR", dir)
	// A directory cannot be opened as an append-only log file.
	t.Setenv("AUDIT_LOG", t.TempDir())
	seedLocks(t, dir, lockIDA)
	code, _, errOut := runLockCmd(t, "clear", lockIDA)
	if code != 1 || !strings.Contains(errOut, "opening audit log") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestSignatureLockClearAllPersistFailureAuditedAsError(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", auditPath)
	seedLocks(t, dir, lockIDA)
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
	code, _, errOut := runLockCmd(t, "clear", "--all")
	if code != 1 || !strings.Contains(errOut, "signature-lock:") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	events := auditLines(t, auditPath)
	if len(events) != 1 || events[0]["operation"] != "clear_all" || events[0]["outcome"] != "error" {
		t.Fatalf("audit events = %v", events)
	}
}
