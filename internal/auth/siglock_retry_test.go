// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignatureLocks_RemovedDirectoryReportsFailureUntilRestored(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	locks, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "0011223344556677"
	if added, err := locks.Lock(keyID, time.Now()); err != nil || !added {
		t.Fatalf("initial lock: added=%v, err=%v", added, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if added, err := locks.Lock(keyID, time.Now()); added || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing directory attempt %d: added=%v, err=%v, want missing-file error", attempt, added, err)
		}
		if _, held := locks.LockedSince(keyID); !held {
			t.Fatal("failed persistence dropped the in-memory lock")
		}
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if added, err := locks.Lock(keyID, time.Now()); added || err != nil {
		t.Fatalf("restored directory: added=%v, err=%v", added, err)
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := reopened.LockedSince(keyID); !held {
		t.Fatal("recovered lock was not persisted")
	}
}

// A first persist that fails must be retried: with the lock only in memory a
// restart would reopen the downgrade.
func TestSignatureLocks_FailedPersistIsRetriedOnNextLock(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	locks, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The directory does not exist yet, so the first write fails.
	newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err == nil || !newly {
		t.Fatalf("first Lock = %v, %v; want newly locked with a persistence error", newly, err)
	}
	// Still failing: the next call reports the error again instead of (false, nil).
	if newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime.Add(time.Minute)); err == nil || newly {
		t.Fatalf("second Lock while still failing = %v, %v; want (false, error)", newly, err)
	}
	// The directory becomes writable: the next Lock persists the held lock.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime.Add(2*time.Minute)); err != nil || newly {
		t.Fatalf("retry Lock = %v, %v; want (false, nil)", newly, err)
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	since, locked := reopened.LockedSince("aaaaaaaaaaaaaaaa")
	if !locked || !since.Equal(lockTestTime) {
		t.Fatalf("after restart: since=%v locked=%v; the retried lock must keep its original time", since, locked)
	}
}

// The retry also happens on a plain check (every v2 or legacy request), and
// the refresh must not replace the unpersisted lock with the empty file.
func TestSignatureLocks_FailedPersistIsRetriedOnRefresh(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	locks, _ := OpenSignatureLocks(dir)
	if _, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime); err == nil {
		t.Fatal("want persistence error")
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("an unpersisted lock must stay in force")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("lock lost")
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, locked := reopened.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("refresh did not persist the pending lock")
	}
}

func TestCheckStateWritable(t *testing.T) {
	t.Parallel()
	good := filepath.Join(t.TempDir(), "new")
	if err := CheckStateWritable(good); err != nil {
		t.Fatalf("creatable dir: %v", err)
	}
	if entries, _ := os.ReadDir(good); len(entries) != 0 {
		t.Fatalf("probe left files behind: %v", entries)
	}
	blocker := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckStateWritable(filepath.Join(blocker, "x")); err == nil {
		t.Fatal("dir under a file must fail")
	}
	ro := t.TempDir()
	// #nosec G302 -- read-only private test fixture.
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// #nosec G302 -- restore owner write permission for cleanup.
		_ = os.Chmod(ro, 0o700)
	})
	if f, err := os.CreateTemp(ro, "probe"); err == nil {
		_ = f.Close()
		t.Skip("directory permissions are not enforced here")
	}
	if err := CheckStateWritable(ro); err == nil {
		t.Fatal("read-only dir must fail the write probe")
	}
}
