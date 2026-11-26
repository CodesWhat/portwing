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
// can never leave a key that looks confirmed.
func LoadOrCreateEnrolledKey(dir string, canCreate bool) (EnrolledKey, error) {
	key := EnrolledKey{
		Path:       filepath.Join(dir, EnrolledKeyFileName),
		MarkerPath: filepath.Join(dir, EnrolledPendingFileName),
	}
	_, statErr := os.Lstat(key.Path)
	if statErr == nil {
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
	if !errors.Is(statErr, os.ErrNotExist) {
		return key, fmt.Errorf("checking enrolled key %q: %w", key.Path, statErr)
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
	pemData, _, err := generateKeyPair("")
	if err != nil {
		return key, fmt.Errorf("generating enrolled key: %w", err)
	}
	// The marker is created exclusively. ErrExist means another start (or an
	// earlier crashed one) already marked it, which is fine.
	if err := createMarkerExclusive(key.MarkerPath); err != nil && !errors.Is(err, os.ErrExist) {
		return key, fmt.Errorf("writing enrollment marker: %w", err)
	}
	if err := writeFileExclusive(key.Path, pemData); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return key, fmt.Errorf("writing enrolled key: %w", err)
		}
		// A concurrent first start won. The marker may be shared with it
		// (it can have seen ours and linked its key, still unconfirmed), so
		// it stays: a key that was never confirmed must not read as
		// confirmed. The worst case is one extra pending cycle.
		return LoadOrCreateEnrolledKey(dir, false)
	}
	key.Pending = true
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
	return nil
}

// ConfirmEnrolledKey removes the pending marker once the controller accepted
// the key. A missing marker is not an error.
func ConfirmEnrolledKey(markerPath string) error {
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
	// #nosec G304 -- marker path is the package-built name under the validated state directory.
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
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
