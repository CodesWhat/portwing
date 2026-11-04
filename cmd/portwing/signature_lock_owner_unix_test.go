// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignatureLockClearRefusesDifferentUID(t *testing.T) {
	for _, target := range []string{lockIDA, "--all"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			seedLocks(t, dir, lockIDA)
			path := filepath.Join(dir, "signature-locks.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Remove the sidecar to prove refusal precedes its creation too.
			if err := os.Remove(path + ".lock"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("STATE_DIR", dir)
			auditPath := filepath.Join(dir, "audit.log")
			t.Setenv("AUDIT_LOG", auditPath)
			original := signatureLockUID
			signatureLockUID = func() int { return os.Geteuid() + 1 }
			t.Cleanup(func() { signatureLockUID = original })
			code, _, stderr := runLockCmd(t, "clear", target)
			if code != 1 || !strings.Contains(stderr, "run as UID") {
				t.Errorf("exit %d stderr %q; want refusal with owner UID", code, stderr)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Errorf("lock state changed: %v", err)
			}
			for _, p := range []string{path + ".lock", auditPath} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("unexpected write at %s: %v", p, err)
				}
			}
		})
	}
}

func TestSignatureLockListAllowsDifferentUID(t *testing.T) {
	dir := t.TempDir()
	seedLocks(t, dir, lockIDA)
	t.Setenv("STATE_DIR", dir)
	original := signatureLockUID
	signatureLockUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { signatureLockUID = original })
	code, stdout, stderr := runLockCmd(t, "list")
	if code != 0 || !strings.Contains(stdout, lockIDA) {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestSignatureLockOwnerCheckMissingDirectory(t *testing.T) {
	if err := checkSignatureLockOwner(filepath.Join(t.TempDir(), "missing"), os.Geteuid()); err == nil {
		t.Fatal("want ownership check to fail for a missing state directory")
	}
}

func TestSignatureLockClearEmptyDoesNotCreateFiles(t *testing.T) {
	for _, target := range []string{lockIDA, "--all"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("STATE_DIR", dir)
			t.Setenv("AUDIT_LOG", "")
			original := signatureLockUID
			signatureLockUID = func() int { return os.Geteuid() + 1 }
			t.Cleanup(func() { signatureLockUID = original })
			code, _, stderr := runLockCmd(t, "clear", target)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("empty clear wrote state: %v %v", entries, err)
			}
		})
	}
}
