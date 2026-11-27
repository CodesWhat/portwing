// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // private directory fixture
		t.Fatal(err)
	}
	return dir
}

func TestEnrolledLockRefusesSymlink(t *testing.T) {
	dir := privateDir(t)
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, EnrolledLockFileName)
	if err := os.Symlink(target, lock); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateEnrolledKey(dir, true)
	if err == nil || !strings.Contains(err.Error(), "locking enrolled key directory") || !strings.Contains(err.Error(), lock) {
		t.Fatalf("a symlinked lock must be refused, naming it: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, EnrolledKeyFileName)); statErr == nil {
		t.Fatal("no key may be written")
	}
	if err := ConfirmEnrolledKey(filepath.Join(dir, EnrolledPendingFileName)); err == nil || !strings.Contains(err.Error(), lock) {
		t.Fatalf("confirm must refuse a symlinked lock too: %v", err)
	}
}

func TestEnrolledLockRefusesLooseOrWrongTypeLock(t *testing.T) {
	t.Run("group and other bits", func(t *testing.T) {
		dir := privateDir(t)
		lock := filepath.Join(dir, EnrolledLockFileName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(lock, 0o644); err != nil { //nolint:gosec // loose lock fixture
			t.Fatal(err)
		}
		_, err := LoadOrCreateEnrolledKey(dir, true)
		if err == nil || !strings.Contains(err.Error(), "locking enrolled key directory") || !strings.Contains(err.Error(), lock) || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("a 0644 lock must be refused, naming it: %v", err)
		}
		if err := ConfirmEnrolledKey(filepath.Join(dir, EnrolledPendingFileName)); err == nil {
			t.Fatal("confirm must refuse a 0644 lock")
		}
	})
	t.Run("private lock is accepted and reused", func(t *testing.T) {
		dir := privateDir(t)
		if err := os.WriteFile(filepath.Join(dir, EnrolledLockFileName), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateEnrolledKey(dir, true); err != nil {
			t.Fatalf("a private lock must work: %v", err)
		}
	})
	t.Run("not a regular file", func(t *testing.T) {
		d, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if err := checkPrivateRegularFile(d, "x", "enrolled key lock"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want a not-regular error, got %v", err)
		}
	})
	t.Run("fstat fails", func(t *testing.T) {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if err := checkPrivateRegularFile(f, "x", "enrolled key lock"); err == nil || !strings.Contains(err.Error(), "checking enrolled key lock") {
			t.Fatalf("want an fstat error, got %v", err)
		}
	})
}

func TestEnrolledOwnerCheck(t *testing.T) {
	setOwner := func(t *testing.T, uid uint32, ok bool) {
		prev := fileOwnerUID
		t.Cleanup(func() { fileOwnerUID = prev })
		fileOwnerUID = func(os.FileInfo) (uint32, bool) { return uid, ok }
	}
	t.Run("directory owned by another uid", func(t *testing.T) {
		dir := privateDir(t)
		setOwner(t, uint32(os.Geteuid())+1, true) // #nosec G115 -- euid is never negative
		_, err := LoadOrCreateEnrolledKey(dir, true)
		if err == nil || !strings.Contains(err.Error(), "enrolled key directory") || !strings.Contains(err.Error(), "owned by uid") {
			t.Fatalf("a foreign-owned directory must be refused: %v", err)
		}
	})
	t.Run("lock owned by another uid", func(t *testing.T) {
		dir := privateDir(t)
		lock := filepath.Join(dir, EnrolledLockFileName)
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(lock)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		setOwner(t, 4242, true)
		prevEuid := geteuid
		t.Cleanup(func() { geteuid = prevEuid })
		geteuid = func() int { return 1000 }
		if err := checkPrivateRegularFile(f, lock, "enrolled key lock"); err == nil || !strings.Contains(err.Error(), "owned by uid 4242") || !strings.Contains(err.Error(), lock) {
			t.Fatalf("a foreign-owned lock must be refused: %v", err)
		}
		geteuid = func() int { return 4242 }
		if err := checkPrivateRegularFile(f, lock, "enrolled key lock"); err != nil {
			t.Fatalf("a lock owned by the euid must pass: %v", err)
		}
	})
	t.Run("owner unknown", func(t *testing.T) {
		setOwner(t, 0, false)
		info, err := os.Stat(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := checkEnrolledDirOwner(info, "d"); err == nil || !strings.Contains(err.Error(), "cannot determine its owner") {
			t.Fatalf("want an unknown-owner error, got %v", err)
		}
	})
	t.Run("marker fails the check and is removed", func(t *testing.T) {
		dir := privateDir(t)
		marker := filepath.Join(dir, EnrolledPendingFileName)
		setOwner(t, 0, false)
		if err := createMarkerExclusive(marker); err == nil || !strings.Contains(err.Error(), "enrollment marker") {
			t.Fatalf("want a marker check error, got %v", err)
		}
		if _, err := os.Lstat(marker); err == nil {
			t.Fatal("a marker that fails the check must not be left behind")
		}
	})
}

type sysLessInfo struct{ os.FileInfo }

func (sysLessInfo) Sys() any { return nil }

func TestFileOwnerUIDWithoutStatT(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fileOwnerUID(sysLessInfo{info}); ok {
		t.Fatal("an info without a Stat_t has no known owner")
	}
	uid, ok := fileOwnerUID(info)
	if !ok || int64(uid) != int64(os.Geteuid()) {
		t.Fatalf("a fresh temp dir is owned by the euid: %d %v", uid, ok)
	}
}

func TestEnrolledMarkerRefusesSymlink(t *testing.T) {
	dir := privateDir(t)
	marker := filepath.Join(dir, EnrolledPendingFileName)
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), marker); err != nil {
		t.Fatal(err)
	}
	if err := createMarkerExclusive(marker); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a symlinked marker must be refused: %v", err)
	}
}

func TestConfirmEnrolledKeyLockFailureLeavesMarker(t *testing.T) {
	dir := privateDir(t)
	marker := filepath.Join(dir, EnrolledPendingFileName)
	if err := os.WriteFile(marker, []byte("pending\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, EnrolledLockFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	err := ConfirmEnrolledKey(marker)
	if err == nil || !strings.Contains(err.Error(), "locking enrolled key directory") {
		t.Fatalf("want a lock error, got %v", err)
	}
	if _, statErr := os.Lstat(marker); statErr != nil {
		t.Fatalf("a failed lock must leave the marker: %v", statErr)
	}
}
