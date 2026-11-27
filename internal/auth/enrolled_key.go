// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// File names for first-start Edge enrollment, relative to the state directory.
const (
	EnrolledKeyFileName     = "enrolled-edge-key.pem"
	EnrolledPendingFileName = "enrolled-edge-key.pending"
	// EnrolledLockFileName is the sidecar flock file that serializes key
	// creation and confirmation in the same directory.
	EnrolledLockFileName = ".enrolled-edge-key.lock"
)

// ErrNoEnrolledKey means no enrolled key exists and no code was supplied to
// create one.
var ErrNoEnrolledKey = errors.New("no enrolled key")

// EnrolledKey describes the key LoadOrCreateEnrolledKey settled on.
type EnrolledKey struct {
	// Path is the PEM file, usable as PRIVATE_KEY_FILE.
	Path string
	// MarkerPath is the pending marker. It exists until the controller's
	// welcome confirms the enrollment.
	MarkerPath string
	// Pending is true while the controller has not yet confirmed the key.
	Pending bool
}

// LoadOrCreateEnrolledKey returns the persisted enrolled key in dir. When none
// exists and canCreate is true it generates an Ed25519 key, writes the pending
// marker, then writes the key (0600, temp file, fsync, exclusive link into
// place). An existing key is never overwritten, and one that fails the
// credential permission checks is a hard error. Without a key and without
// canCreate it returns ErrNoEnrolledKey. The marker goes down first so a crash
// can never leave a key that looks confirmed. Creation runs under an exclusive
// lock on the directory (see EnrolledLockFileName), and only the start that
// publishes the key creates the marker.
func LoadOrCreateEnrolledKey(dir string, canCreate bool) (EnrolledKey, error) {
	key := EnrolledKey{
		Path:       filepath.Join(dir, EnrolledKeyFileName),
		MarkerPath: filepath.Join(dir, EnrolledPendingFileName),
	}
	exists, err := enrolledKeyExists(key.Path)
	if err != nil {
		return key, err
	}
	if exists {
		return loadExistingEnrolledKey(dir, key)
	}
	if !canCreate {
		return key, ErrNoEnrolledKey
	}
	if err := checkEnrolledKeyDir(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	if err := CheckStateWritable(dir); err != nil {
		return key, fmt.Errorf("cannot persist an enrolled key: %w", err)
	}
	if err := checkEnrolledKeyDir(dir); err != nil {
		return key, err
	}
	// The check-and-create section below runs under an exclusive directory
	// lock, so no other start can publish a key, mark one pending or confirm
	// one while this start decides what to do.
	release, err := flockEnrolledLock(filepath.Join(dir, EnrolledLockFileName))
	if err != nil {
		return key, fmt.Errorf("locking enrolled key directory: %w", err)
	}
	defer release()
	// Re-check under the lock: a start that was delayed before it got here may
	// find a key another start published (and even confirmed) meanwhile. That
	// key is loaded as it stands; no marker is created for it.
	exists, err = enrolledKeyExists(key.Path)
	if err != nil {
		return key, err
	}
	if exists {
		return loadExistingEnrolledKey(dir, key)
	}
	pemData, _, err := generateKeyPair("")
	if err != nil {
		return key, fmt.Errorf("generating enrolled key: %w", err)
	}
	// The marker goes down before the key so a crash can never leave a key
	// that looks confirmed. ErrExist is a leftover from a crashed start (no
	// key exists, and we hold the lock), which still marks the key pending.
	markerCreated := true
	if err := createMarkerExclusive(key.MarkerPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return key, fmt.Errorf("writing enrollment marker: %w", err)
		}
		markerCreated = false
	}
	if err := writeFileExclusive(key.Path, pemData); err != nil {
		if wasPublished(err) {
			// The key is linked into place; only its final directory sync
			// failed. The marker must outlive it, or the next start sees an
			// unregistered key and skips enrollment.
			key.Pending = true
			return key, fmt.Errorf("writing enrolled key: %w", err)
		}
		if !errors.Is(err, os.ErrExist) {
			// Nothing was published. Remove only a marker this start made.
			if markerCreated {
				_ = os.Remove(key.MarkerPath)
			}
			return key, fmt.Errorf("writing enrolled key: %w", err)
		}
		// Only reachable where the lock is not enforced (no flock on this
		// platform): a start outside the lock won. The marker may be shared
		// with it, so it stays and the winner's key is loaded.
		return loadExistingEnrolledKey(dir, key)
	}
	key.Pending = true
	return key, nil
}

// enrolledKeyExists reports whether anything sits at the key path, replaceable
// in tests.
var enrolledKeyExists = func(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("checking enrolled key %q: %w", path, err)
}

// loadExistingEnrolledKey validates and loads the key at key.Path and reports
// whether its pending marker is still present.
func loadExistingEnrolledKey(dir string, key EnrolledKey) (EnrolledKey, error) {
	if err := checkEnrolledKeyDir(dir); err != nil {
		return key, err
	}
	if _, err := LoadPrivateKey(key.Path); err != nil {
		return key, fmt.Errorf("enrolled key: %w", err)
	}
	_, markerErr := os.Lstat(key.MarkerPath)
	key.Pending = markerErr == nil
	return key, nil
}

// checkEnrolledKeyDir refuses a group- or world-writable enrolled-key
// directory: another process in the group could swap the key or the marker.
// It applies to this directory only; other state paths keep the looser
// CheckStateWritable rule.
func checkEnrolledKeyDir(dir string) error {
	if enrolledKeyGOOS == "windows" {
		return nil
	}
	info, err := statDir(dir)
	if err != nil {
		return fmt.Errorf("checking enrolled key directory: %w", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("enrolled key directory %q is group- or world-writable (mode %04o); run chmod 700 %q", dir, info.Mode().Perm(), dir)
	}
	return checkEnrolledDirOwner(info, dir)
}

// ConfirmEnrolledKey removes the pending marker once the controller accepted
// the key. A missing marker is not an error. It takes the same directory lock
// as creation, so it cannot interleave with a start that is publishing a key.
func ConfirmEnrolledKey(markerPath string) error {
	release, err := flockEnrolledLock(filepath.Join(filepath.Dir(markerPath), EnrolledLockFileName))
	if err != nil {
		return fmt.Errorf("removing enrollment marker: locking enrolled key directory: %w", err)
	}
	defer release()
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing enrollment marker: %w", err)
	}
	return syncRotationStateDirectory(filepath.Dir(markerPath))
}

// createMarkerExclusive durably creates the pending marker, failing with
// os.ErrExist only when a regular file is already there.
func createMarkerExclusive(path string) error {
	f, err := openMarkerFile(path)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := lstatFile(path)
			if errors.Is(statErr, os.ErrNotExist) {
				// Confirmed and removed between our open and stat: the
				// caller's key link then fails with ErrExist and it reloads.
				return err
			}
			if statErr != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("enrollment marker path %q is not a regular file", path)
			}
		}
		return err
	}
	if _, err := f.WriteString("pending\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return syncRotationStateDirectory(filepath.Dir(path))
}

// markerFile is the slice of *os.File createMarkerExclusive writes through.
type markerFile interface {
	WriteString(string) (int, error)
	Sync() error
	Close() error
}

// openMarkerFile exclusively creates the marker, replaceable in tests.
var openMarkerFile = func(path string) (markerFile, error) {
	f, err := openNoFollow(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := checkPrivateRegularFile(f, path, "enrollment marker"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return f, nil
}

// statDir is os.Stat, replaceable in tests.
var statDir = os.Stat

// enrolledKeyGOOS is runtime.GOOS, replaceable in tests.
var enrolledKeyGOOS = runtime.GOOS

// lstatFile is os.Lstat, replaceable in tests.
var lstatFile = os.Lstat

// linkFile is os.Link, replaceable in tests.
var linkFile = os.Link

// generateKeyPair is GenerateKeyPair, replaceable in tests.
var generateKeyPair = GenerateKeyPair

// writeFileExclusive durably writes data to path with mode 0600 and fails with
// os.ErrExist if path already exists.
func writeFileExclusive(path string, data []byte) error {
	return writeFileVia(path, data, ".enroll-", func(tmp, dst string) error {
		if err := linkFile(tmp, dst); err != nil {
			if errors.Is(err, os.ErrExist) {
				return err
			}
			// No atomic no-replace primitive here (e.g. a filesystem without
			// hard links): a check-then-rename would race a concurrent start
			// and could replace a key, so fail closed.
			return fmt.Errorf("exclusive create needs hard link support: %w", err)
		}
		return nil
	})
}
