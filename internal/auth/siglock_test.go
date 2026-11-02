// SPDX-License-Identifier: MIT

package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var lockTestTime = time.Date(2026, 11, 2, 12, 30, 0, 0, time.UTC)

func TestSignatureLocks_LockPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err != nil || !newly {
		t.Fatalf("first Lock = %v, %v; want newly locked", newly, err)
	}
	newly, err = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime.Add(time.Hour))
	if err != nil || newly {
		t.Fatalf("second Lock = %v, %v; want not newly locked", newly, err)
	}

	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	since, locked := reopened.LockedSince("aaaaaaaaaaaaaaaa")
	if !locked || !since.Equal(lockTestTime) {
		t.Fatalf("after reopen: since=%v locked=%v; want first lock time %v", since, locked, lockTestTime)
	}
	if _, locked := reopened.LockedSince("bbbbbbbbbbbbbbbb"); locked {
		t.Fatal("a different key must not be locked")
	}
}

func TestSignatureLocks_ClearByCLIReachesRunningInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	server, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Lock("aaaaaaaaaaaaaaaa", lockTestTime); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Lock("bbbbbbbbbbbbbbbb", lockTestTime); err != nil {
		t.Fatal(err)
	}

	cli, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := cli.Clear("aaaaaaaaaaaaaaaa")
	if err != nil || !cleared {
		t.Fatalf("Clear = %v, %v; want cleared", cleared, err)
	}
	if cleared, _ := cli.Clear("aaaaaaaaaaaaaaaa"); cleared {
		t.Fatal("clearing an unlocked key must report nothing cleared")
	}

	if _, locked := server.LockedSince("aaaaaaaaaaaaaaaa"); locked {
		t.Fatal("running server still sees the cleared lock")
	}
	if _, locked := server.LockedSince("bbbbbbbbbbbbbbbb"); !locked {
		t.Fatal("clearing one key must leave the other locked")
	}

	// The same key can lock again after a clear, as when a rolled-back
	// controller is upgraded again.
	newly, err := server.Lock("aaaaaaaaaaaaaaaa", lockTestTime.Add(time.Hour))
	if err != nil || !newly {
		t.Fatalf("relock = %v, %v; want newly locked", newly, err)
	}
}

func TestSignatureLocks_ClearAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	_, _ = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	_, _ = locks.Lock("bbbbbbbbbbbbbbbb", lockTestTime)
	n, err := locks.ClearAll()
	if err != nil || n != 2 {
		t.Fatalf("ClearAll = %d, %v; want 2", n, err)
	}
	if n, _ := locks.ClearAll(); n != 0 {
		t.Fatalf("second ClearAll = %d, want 0", n)
	}
	reopened, _ := OpenSignatureLocks(dir)
	if len(reopened.List()) != 0 {
		t.Fatal("cleared locks came back after reopen")
	}
}

func TestSignatureLocks_FileRemovedUnlocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	_, _ = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err := os.Remove(filepath.Join(dir, SignatureLocksFileName)); err != nil {
		t.Fatal(err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); locked {
		t.Fatal("operator deleting the lock file must clear the lock")
	}
}

func TestSignatureLocks_CorruptFileFailsToOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// #nosec G302 -- private test fixture.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"garbage":       "not json",
		"wrong version": `{"version":2,"locks":{}}`,
		"bad key id":    `{"version":1,"locks":{"zz":"2026-11-02T12:30:00Z"}}`,
		"bad time":      `{"version":1,"locks":{"aaaaaaaaaaaaaaaa":"yesterday"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, SignatureLocksFileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSignatureLocks(dir); err == nil {
			t.Errorf("%s: want error, got nil (a corrupt lock file must not silently drop locks)", name)
		}
	}
}

func TestSignatureLocks_CorruptRefreshKeepsMemory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	_, _ = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	path := filepath.Join(dir, SignatureLocksFileName)
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(path, future, future)
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("an unreadable lock file must fail closed and keep the in-memory lock")
	}
}

func TestSignatureLocks_MemoryOnlyWhenNoDir(t *testing.T) {
	t.Parallel()
	locks := NewMemorySignatureLocks()
	newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err != nil || !newly {
		t.Fatalf("Lock = %v, %v", newly, err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("memory-only lock lost")
	}
	if cleared, err := locks.Clear("aaaaaaaaaaaaaaaa"); err != nil || !cleared {
		t.Fatalf("Clear = %v, %v", cleared, err)
	}
}

func TestSignatureLocks_PersistFailureStillLocksInMemory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	newly, err := locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err == nil || !newly {
		t.Fatalf("Lock = %v, %v; want newly locked with a persistence error", newly, err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("a failed write must not drop the in-memory lock")
	}
}

func TestValidKeyID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"0123456789abcdef", "ffffffffffffffff"} {
		if !ValidKeyID(id) {
			t.Errorf("ValidKeyID(%q) = false", id)
		}
	}
	for _, id := range []string{"", "0123456789abcde", "0123456789abcdef0", "0123456789ABCDEF", "g123456789abcdef"} {
		if ValidKeyID(id) {
			t.Errorf("ValidKeyID(%q) = true", id)
		}
	}
}
