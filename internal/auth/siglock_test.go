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

func TestSignatureLocks_FileRemovedKeepsLocksAndRewrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, SignatureLocksFileName)
	locks, _ := OpenSignatureLocks(dir)
	_, _ = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("deleting the lock file must not unlock a key")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file was not re-created: %v", err)
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, locked := reopened.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("re-created lock file lost the lock")
	}
}

func TestSignatureLocks_FileAndDirRemovedKeepsLocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	_, _ = locks.Lock("aaaaaaaaaaaaaaaa", lockTestTime)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, locked := locks.LockedSince("aaaaaaaaaaaaaaaa"); !locked {
		t.Fatal("a failed re-persist must not drop the in-memory lock")
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

func TestSignatureLocks_InterleavedLockAndClearBothSurvive(t *testing.T) {
	t.Parallel()
	const a, b, c = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "cccccccccccccccc"
	dir := t.TempDir()
	agent, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Lock(b, lockTestTime); err != nil {
		t.Fatal(err)
	}
	cli, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The CLI has b in its view. The agent locks a behind its back, then the
	// CLI clears b. The stale CLI map must not drop a.
	if _, err := agent.Lock(a, lockTestTime); err != nil {
		t.Fatal(err)
	}
	if cleared, err := cli.Clear(b); err != nil || !cleared {
		t.Fatalf("Clear = %v, %v", cleared, err)
	}
	// Opposite order: the CLI cleared b, so the agent's stale view (still
	// holding b) must not re-add it when it locks c.
	if _, err := agent.Lock(c, lockTestTime); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List()
	if len(got) != 2 || got[0].KeyID != a || got[1].KeyID != c {
		t.Fatalf("locks = %v; want a and c only", got)
	}
}

func TestSignatureLocks_UnpersistedLockMergesWithFile(t *testing.T) {
	t.Parallel()
	const x, y, z = "1111111111111111", "2222222222222222", "3333333333333333"
	dir := t.TempDir()
	agent, _ := OpenSignatureLocks(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Lock(x, lockTestTime); err == nil {
		t.Fatal("Lock into a missing dir must report a persistence error")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cli, _ := OpenSignatureLocks(dir)
	if _, err := cli.Lock(y, lockTestTime); err != nil {
		t.Fatal(err)
	}
	// The agent is dirty (holds x unwritten). Locking z merges x, y and z. A
	// Clear issued by the CLI while the agent was dirty would be undone the
	// same way, which fails closed.
	if newly, err := agent.Lock(z, lockTestTime); err != nil || !newly {
		t.Fatalf("Lock = %v, %v", newly, err)
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List(); len(got) != 3 {
		t.Fatalf("locks = %v; want x, y and z", got)
	}
}

func TestSignatureLocks_CheckRetriesDirtyWriteUnderLock(t *testing.T) {
	t.Parallel()
	const x = "1111111111111111"
	dir := t.TempDir()
	agent, _ := OpenSignatureLocks(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	_, _ = agent.Lock(x, lockTestTime)
	if _, locked := agent.LockedSince(x); !locked {
		t.Fatal("dirty lock must still hold")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, locked := agent.LockedSince(x); !locked {
		t.Fatal("dirty lock must still hold")
	}
	reopened, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, locked := reopened.LockedSince(x); !locked {
		t.Fatal("the retry must have persisted the lock")
	}
	if newly, err := agent.Lock(x, lockTestTime); err != nil || newly {
		t.Fatalf("Lock = %v, %v; want existing", newly, err)
	}
}

func TestSignatureLocks_SidecarLockUnavailable(t *testing.T) {
	t.Parallel()
	const a, b = "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	locks, _ := OpenSignatureLocks(dir)
	if _, err := locks.Lock(a, lockTestTime); err != nil {
		t.Fatal(err)
	}
	// A directory where the sidecar file belongs cannot be opened as a lock.
	sidecar := filepath.Join(dir, SignatureLocksFileName+".lock")
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Clear(a); err == nil {
		t.Fatal("Clear must fail when the lock cannot be taken")
	}
	if _, err := locks.ClearAll(); err == nil {
		t.Fatal("ClearAll must fail when the lock cannot be taken")
	}
	newly, err := locks.Lock(b, lockTestTime)
	if err == nil || !newly {
		t.Fatalf("Lock = %v, %v; want newly locked with an error", newly, err)
	}
	if _, locked := locks.LockedSince(b); !locked {
		t.Fatal("Lock must keep the lock in memory")
	}
	if _, locked := locks.LockedSince(a); !locked {
		t.Fatal("a failed Clear must not drop the lock")
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
