// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignatureLocks_MutatorsWaitForFileLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, SignatureLocksFileName+".lock")
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("sidecar perms = %v; want 0600", perm)
	}

	cases := map[string]func() error{
		"Lock":     func() error { _, err := locks.Lock("bbbbbbbbbbbbbbbb", lockTestTime); return err },
		"Clear":    func() error { _, err := locks.Clear("aaaaaaaaaaaaaaaa"); return err },
		"ClearAll": func() error { _, err := locks.ClearAll(); return err },
	}
	for name, mutate := range cases {
		release, err := flockExclusive(sidecar)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- mutate() }()
		select {
		case <-done:
			t.Fatalf("%s returned while another holder had the file lock", name)
		case <-time.After(150 * time.Millisecond):
		}
		release()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s after release: %v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s still blocked after the file lock was released", name)
		}
	}
}

func TestFlockExclusive_OpenFailure(t *testing.T) {
	t.Parallel()
	if _, err := flockExclusive(filepath.Join(t.TempDir(), "missing", "x.lock")); err == nil {
		t.Fatal("want an error when the lock file cannot be opened")
	}
}
