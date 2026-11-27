// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteStateFile durably replaces path with data: write a private temporary
// file in the same directory, sync it, rename it into place, then sync the
// directory. The same steps as WriteRotationState, for state that is not
// rotation keys (agent ID, signature locks).
func WriteStateFile(path string, data []byte) error {
	return writeFileVia(path, data, ".state-", func(tmp, dst string) error {
		if err := os.Rename(tmp, dst); err != nil {
			return fmt.Errorf("replacing state file: %w", err)
		}
		return nil
	})
}

// publishedError wraps a failure after commit made the file visible at its
// final path, so a caller can tell it from a failure with nothing published.
type publishedError struct{ err error }

func (e *publishedError) Error() string { return e.err.Error() }
func (e *publishedError) Unwrap() error { return e.err }

// wasPublished reports whether err happened after the file reached its path.
func wasPublished(err error) bool {
	var pe *publishedError
	return errors.As(err, &pe)
}

// writeFileVia writes data to a private temporary file next to path, syncs it,
// hands it to commit to move into place, then syncs the directory. commit owns
// the final step (rename to replace, link to create exclusively) and wraps its
// own errors.
// A directory sync failure comes back as a publishedError: the file is already
// linked or renamed into place by then.
func writeFileVia(path string, data []byte, prefix string, commit func(tmp, path string) error) error {
	dir := filepath.Dir(path)
	f, err := createRotationStateFile(dir, prefix)
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing temporary file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if err := commit(f.Name(), path); err != nil {
		return err
	}
	if err := syncRotationStateDirectory(dir); err != nil {
		return &publishedError{err: err}
	}
	return nil
}
