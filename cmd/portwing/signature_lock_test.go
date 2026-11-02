// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/auth"
)

const lockIDA = "0123456789abcdef"
const lockIDB = "fedcba9876543210"

func seedLocks(t *testing.T, dir string, ids ...string) {
	t.Helper()
	locks, err := auth.OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := locks.Lock(id, time.Date(2026, 11, 2, 12, 30, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
}

func runLockCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runSignatureLock(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func auditLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestSignatureLockClearOneKey(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", auditPath)
	seedLocks(t, dir, lockIDA, lockIDB)

	code, out, errOut := runLockCmd(t, "clear", lockIDA)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, lockIDA) {
		t.Fatalf("stdout = %q", out)
	}
	locks, _ := auth.OpenSignatureLocks(dir)
	if _, locked := locks.LockedSince(lockIDA); locked {
		t.Fatal("key A still locked")
	}
	if _, locked := locks.LockedSince(lockIDB); !locked {
		t.Fatal("key B must stay locked")
	}
	events := auditLines(t, auditPath)
	if len(events) != 1 || events[0]["event"] != "signature_lock" || events[0]["operation"] != "clear" ||
		events[0]["key_id"] != lockIDA || events[0]["outcome"] != "allowed" {
		t.Fatalf("audit events = %v", events)
	}
}

func TestSignatureLockClearAll(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", auditPath)
	seedLocks(t, dir, lockIDA, lockIDB)

	code, out, errOut := runLockCmd(t, "clear", "--all")
	if code != 0 || !strings.Contains(out, "2") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
	locks, _ := auth.OpenSignatureLocks(dir)
	if len(locks.List()) != 0 {
		t.Fatal("locks remain")
	}
	events := auditLines(t, auditPath)
	if len(events) != 1 || events[0]["operation"] != "clear_all" || events[0]["outcome"] != "allowed" {
		t.Fatalf("audit events = %v", events)
	}
}

func TestSignatureLockClearUnknownKeyIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", auditPath)
	code, out, _ := runLockCmd(t, "clear", lockIDA)
	if code != 0 || !strings.Contains(out, "no lock") {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	if _, err := os.Stat(auditPath); err == nil {
		if events := auditLines(t, auditPath); len(events) != 0 {
			t.Fatalf("a no-op clear must not audit a change: %v", events)
		}
	}
}

func TestSignatureLockList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", "")
	seedLocks(t, dir, lockIDB, lockIDA)
	code, out, _ := runLockCmd(t, "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	a, b := strings.Index(out, lockIDA), strings.Index(out, lockIDB)
	if a < 0 || b < 0 || a > b || !strings.Contains(out, "2026-11-02T12:30:00Z") {
		t.Fatalf("list output = %q", out)
	}
}

func TestSignatureLockListEmpty(t *testing.T) {
	t.Setenv("STATE_DIR", t.TempDir())
	t.Setenv("AUDIT_LOG", "")
	code, out, _ := runLockCmd(t, "list")
	if code != 0 || !strings.Contains(out, "no keys are locked") {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

func TestSignatureLockUsageErrors(t *testing.T) {
	t.Setenv("STATE_DIR", t.TempDir())
	t.Setenv("AUDIT_LOG", "")
	for name, args := range map[string][]string{
		"no args":            {},
		"unknown subcommand": {"remove", lockIDA},
		"clear no target":    {"clear"},
		"bad key id":         {"clear", "not-a-key"},
		"uppercase key id":   {"clear", "0123456789ABCDEF"},
		"id and --all":       {"clear", lockIDA, "--all"},
		"extra arg":          {"clear", lockIDA, lockIDB},
		"list with arg":      {"list", lockIDA},
	} {
		code, _, errOut := runLockCmd(t, args...)
		if code != 2 || !strings.Contains(errOut, "Usage: portwing signature-lock") {
			t.Errorf("%s: exit %d stderr %q, want exit 2 with usage", name, code, errOut)
		}
	}
}

func TestSignatureLockCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	// #nosec G302 -- private test directory.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, auth.SignatureLocksFileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", "")
	code, _, errOut := runLockCmd(t, "clear", "--all")
	if code != 1 || !strings.Contains(errOut, "signature-lock:") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestSignatureLockClearPersistFailureAuditedAsError(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", auditPath)
	seedLocks(t, dir, lockIDA)
	// #nosec G302 -- private test directory.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- private test directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced here (running as root?)")
	}
	code, _, errOut := runLockCmd(t, "clear", lockIDA)
	if code != 1 {
		t.Fatalf("exit %d stderr %q, want 1", code, errOut)
	}
	events := auditLines(t, auditPath)
	if len(events) != 1 || events[0]["outcome"] != "error" {
		t.Fatalf("audit events = %v", events)
	}
}

func TestRunDispatchesSignatureLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATE_DIR", dir)
	t.Setenv("AUDIT_LOG", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"portwing", "signature-lock", "list"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no keys are locked") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
