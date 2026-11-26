// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"crypto/rand"
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
	// Created is true when this call generated the key.
	Created bool
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
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return key, fmt.Errorf("generating enrolled key: %w", err)
	}
	pemData, err := MarshalPrivateKeyPEM(priv)
	if err != nil {
		return key, err
	}
	if err := WriteStateFile(key.MarkerPath, []byte("pending\n")); err != nil {
		return key, fmt.Errorf("writing enrollment marker: %w", err)
	}
	if err := writeFileExclusive(key.Path, pemData); err != nil {
		return key, fmt.Errorf("writing enrolled key: %w", err)
	}
	key.Pending = true
	key.Created = true
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

// writeFileExclusive durably writes data to path with mode 0600 and fails with
// os.ErrExist if path already exists.
func writeFileExclusive(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := createRotationStateFile(dir, ".enroll-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return err
		}
		// Filesystems without hard links: fall back to a checked rename.
		if _, statErr := os.Lstat(path); statErr == nil {
			return os.ErrExist
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return syncRotationStateDirectory(dir)
}
