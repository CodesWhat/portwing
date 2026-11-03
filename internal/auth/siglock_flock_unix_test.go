// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

// Not parallel: it swaps flockCall, and sequential tests finish before any
// parallel test in the package resumes.
func TestFlockExclusive_RetriesInterruptAndReportsFailure(t *testing.T) {
	orig := flockCall
	t.Cleanup(func() { flockCall = orig })
	path := filepath.Join(t.TempDir(), "x.lock")

	calls := 0
	flockCall = func(fd, how int) error {
		calls++
		if how == syscall.LOCK_EX && calls == 1 {
			return syscall.EINTR
		}
		return orig(fd, how)
	}
	release, err := flockExclusive(path)
	if err != nil {
		t.Fatalf("flockExclusive after EINTR: %v", err)
	}
	release()
	if calls < 2 {
		t.Fatalf("flock called %d times; want a retry after EINTR", calls)
	}

	flockCall = func(int, int) error { return syscall.EIO }
	if _, err := flockExclusive(path); !errors.Is(err, syscall.EIO) {
		t.Fatalf("flockExclusive error = %v; want EIO", err)
	}
}
