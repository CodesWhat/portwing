// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSignatureLocks_UnusableDirIsEmptyNotFatal(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	locks, err := OpenSignatureLocks(filepath.Join(blocker, "state"))
	if err != nil {
		t.Fatalf("OpenSignatureLocks: %v", err)
	}
	if len(locks.List()) != 0 {
		t.Fatal("want no locks")
	}
}

func TestSignatureLocks_InaccessibleParentIsFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	locks, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Lock("0123456789abcdef", lockTestTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// #nosec G302 -- restore private test directory permissions for cleanup.
		_ = os.Chmod(dir, 0o700)
	})
	if _, err := OpenSignatureLocks(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("want permission error instead of empty state, got %v", err)
	}
}

func TestSignatureLocks_WorldReadableFileIsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, SignatureLocksFileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"locks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSignatureLocks(dir); err == nil {
		t.Fatal("want permission error")
	}
}
