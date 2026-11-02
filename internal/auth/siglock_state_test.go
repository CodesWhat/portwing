// SPDX-License-Identifier: MIT

package auth

import (
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
