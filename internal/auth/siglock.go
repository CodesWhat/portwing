// SPDX-License-Identifier: MIT

package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// SignatureLocksFileName is the file under the state directory that records
// which key IDs have been locked to signature version 3.
const SignatureLocksFileName = "signature-locks.json"

const maxSignatureLocksBytes = 1024 * 1024

type signatureLockFile struct {
	Version int               `json:"version"`
	Locks   map[string]string `json:"locks"`
}

// SignatureLocks records the downgrade lock per key ID: once a key has
// verified a version 3 request, its version 2 and legacy signatures are
// refused. Locks persist in a file so a restart cannot reopen the downgrade.
//
// The file is shared with the `portwing signature-lock` command, which only
// ever removes locks. A running agent notices a changed file on its next check
// (one stat), so a clear takes effect without a restart or signal. Mutations
// from either process hold an exclusive flock on a sidecar file (the lock file
// name plus ".lock") around the whole read-modify-write.
type SignatureLocks struct {
	mu    sync.Mutex
	path  string // empty means memory only
	locks map[string]time.Time
	seen  os.FileInfo // file state the in-memory map was last synced with; nil when absent
	// dirty is set when the in-memory locks could not be written. The memory
	// map is then authoritative and refreshLocked retries the write instead
	// of re-reading the file; pendingErr is the latest failure.
	dirty      bool
	pendingErr error
}

// ValidKeyID reports whether id has the shape of a key ID: 16 lowercase hex
// characters.
func ValidKeyID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// NewMemorySignatureLocks returns locks that are not persisted, so a restart
// forgets every lock. The server never offers v3 on them; they exist for tests
// and for the vectors harness.
func NewMemorySignatureLocks() *SignatureLocks {
	return &SignatureLocks{locks: make(map[string]time.Time)}
}

// OpenSignatureLocks loads the lock file in dir. A missing file is an empty
// set. A file that exists but is unreadable or invalid is an error: dropping
// locks silently would reopen the downgrade.
func OpenSignatureLocks(dir string) (*SignatureLocks, error) {
	l := &SignatureLocks{path: filepath.Join(dir, SignatureLocksFileName), locks: make(map[string]time.Time)}
	locks, info, err := readSignatureLocks(l.path)
	if err != nil {
		return nil, err
	}
	l.locks, l.seen = locks, info
	return l, nil
}

func readSignatureLocks(path string) (map[string]time.Time, os.FileInfo, error) {
	f, err := openCredentialFile(path, "signature locks")
	if err != nil {
		// Only an absent file or non-directory parent means no locks.
		// Permission and other lookup errors must not silently drop locks.
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) || errors.Is(statErr, syscall.ENOTDIR) {
			return make(map[string]time.Time), nil, nil
		}
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stat signature locks: %w", err)
	}
	data, err := readRotationState(io.LimitReader(f, maxSignatureLocksBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading signature locks: %w", err)
	}
	if len(data) > maxSignatureLocksBytes {
		return nil, nil, fmt.Errorf("signature locks exceed %d bytes", maxSignatureLocksBytes)
	}
	var file signatureLockFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("decoding signature locks: %w", err)
	}
	if file.Version != 1 || file.Locks == nil {
		return nil, nil, fmt.Errorf("unsupported signature locks file")
	}
	locks := make(map[string]time.Time, len(file.Locks))
	for id, raw := range file.Locks {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil || !ValidKeyID(id) {
			return nil, nil, fmt.Errorf("invalid signature lock entry %q", id)
		}
		locks[id] = since.UTC()
	}
	return locks, info, nil
}

// refreshLocked re-reads the file when it changed since the last sync. A file
// that cannot be read or has vanished keeps the in-memory locks, so neither
// corruption nor deletion unlocks.
// Read-only callers use it, so it stays lock-free: one stat.
func (l *SignatureLocks) refreshLocked() {
	if l.path == "" {
		return
	}
	if l.dirty {
		// Unpersisted locks win over whatever the file says; retry the write
		// under the file lock, merged with what is on disk.
		if release, err := l.acquireLocked(); err == nil {
			release()
		} else {
			l.pendingErr = err
		}
		return
	}
	info, err := os.Stat(l.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if l.seen != nil {
			// The file existed and is gone. Nothing in the product deletes it
			// (ClearAll writes an empty map), so keep the locks and write them
			// back instead of failing open.
			slog.Error("signature lock file vanished; keeping in-memory locks and re-persisting", "path", l.path)
			l.seen = nil
			l.dirty = true
			if release, err := l.acquireLocked(); err == nil {
				release()
			} else {
				l.pendingErr = err
			}
		}
		return
	case err != nil:
		return
	}
	if l.seen != nil && os.SameFile(l.seen, info) && l.seen.ModTime().Equal(info.ModTime()) && l.seen.Size() == info.Size() {
		return
	}
	l.reloadLocked()
}

// reloadLocked unconditionally re-reads the file. Unpersisted in-memory locks
// are unioned in, so none is lost. A file that cannot be read keeps the
// in-memory locks.
func (l *SignatureLocks) reloadLocked() {
	locks, fresh, err := readSignatureLocks(l.path)
	if err != nil {
		slog.Error("signature lock file unreadable; keeping in-memory locks", "error", err)
		return
	}
	if l.dirty {
		for id, since := range l.locks {
			if _, ok := locks[id]; !ok {
				locks[id] = since
			}
		}
	}
	l.locks, l.seen = locks, fresh
}

// acquireLocked takes the cross-process file lock and re-reads the file so the
// caller mutates the latest contents; the caller must release before it
// returns. Without it, two processes doing read-modify-write could overwrite
// each other's change. Memory-only locks take no file lock. If the lock cannot
// be taken the error is returned and the in-memory state is left as it was.
func (l *SignatureLocks) acquireLocked() (func(), error) {
	if l.path == "" {
		return func() {}, nil
	}
	release, err := flockExclusive(l.path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("locking signature locks: %w", err)
	}
	l.reloadLocked()
	if l.dirty {
		_ = l.persistLocked()
	}
	return release, nil
}

func (l *SignatureLocks) persistLocked() error {
	if l.path == "" {
		return nil
	}
	file := signatureLockFile{Version: 1, Locks: make(map[string]string, len(l.locks))}
	for id, since := range l.locks {
		file.Locks[id] = since.UTC().Format(time.RFC3339)
	}
	data, _ := json.Marshal(file) // a map of strings always marshals
	if err := WriteStateFile(l.path, data); err != nil {
		l.dirty = true
		l.pendingErr = fmt.Errorf("persisting signature locks: %w", err)
		return l.pendingErr
	}
	l.dirty, l.pendingErr = false, nil
	if info, err := os.Stat(l.path); err == nil {
		l.seen = info
	}
	return nil
}

// LockedSince reports when keyID was locked to version 3, if it is.
func (l *SignatureLocks) LockedSince(keyID string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	since, ok := l.locks[keyID]
	return since, ok
}

// Lock records keyID as locked at the given time. It reports whether the lock
// is new. When persisting fails the lock still holds in memory and the error is
// returned so the caller can log it; every later check or Lock retries the
// write, and Lock keeps returning the error until it succeeds.
//
// The whole read-modify-write runs under an exclusive file lock, so a
// concurrent Clear from the CLI cannot drop this lock or be undone by it.
func (l *SignatureLocks) Lock(keyID string, at time.Time) (newly bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	if _, ok := l.locks[keyID]; ok && !l.dirty {
		return false, l.pendingErr
	}
	release, lockErr := l.acquireLocked()
	if lockErr == nil {
		defer release()
	}
	if _, ok := l.locks[keyID]; ok {
		return false, l.pendingErr
	}
	l.locks[keyID] = at.UTC().Truncate(time.Second)
	if lockErr != nil {
		// Keep the lock in memory; a later check retries the write.
		l.dirty, l.pendingErr = true, lockErr
		return true, lockErr
	}
	return true, l.persistLocked()
}

// Clear removes the lock for keyID and reports whether one existed. A lock the
// running agent has not managed to persist yet is merged back in, so a Clear
// that races an unwritten lock is undone; that fails closed.
func (l *SignatureLocks) Clear(keyID string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	release, err := l.acquireLocked()
	if err != nil {
		return false, err
	}
	defer release()
	if _, ok := l.locks[keyID]; !ok {
		return false, nil
	}
	delete(l.locks, keyID)
	return true, l.persistLocked()
}

// ClearAll removes every lock and returns how many there were.
func (l *SignatureLocks) ClearAll() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	release, err := l.acquireLocked()
	if err != nil {
		return 0, err
	}
	defer release()
	n := len(l.locks)
	if n == 0 {
		return 0, nil
	}
	l.locks = make(map[string]time.Time)
	return n, l.persistLocked()
}

// SignatureLock is one entry returned by List.
type SignatureLock struct {
	KeyID string
	Since time.Time
}

// List returns the current locks sorted by key ID.
func (l *SignatureLocks) List() []SignatureLock {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	out := make([]SignatureLock, 0, len(l.locks))
	for id, since := range l.locks {
		out = append(out, SignatureLock{KeyID: id, Since: since})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyID < out[j].KeyID })
	return out
}
