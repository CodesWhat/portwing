// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if err := CheckStateWritable(dir); err != nil {
		return key, fmt.Errorf("cannot persist an enrolled key: %w", err)
	}
	pemData, _, err := generateKeyPair("")
	if err != nil {
		return key, fmt.Errorf("generating enrolled key: %w", err)
	}
	if err := WriteStateFile(key.MarkerPath, []byte("pending\n")); err != nil {
		return key, fmt.Errorf("writing enrollment marker: %w", err)
	}
	if err := writeFileExclusive(key.Path, pemData); err != nil {
		return key, fmt.Errorf("writing enrolled key: %w", err)
	}
	key.Pending = true
	return key, nil
}

// ConfirmEnrolledKey removes the pending marker once the controller accepted
// the key. A missing marker is not an error.
func ConfirmEnrolledKey(markerPath string) error {
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing enrollment marker: %w", err)
	}
	return syncRotationStateDirectory(filepath.Dir(markerPath))
}

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
