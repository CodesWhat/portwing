// SPDX-License-Identifier: MIT

package auth

import (
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

// writeFileVia writes data to a private temporary file next to path, syncs it,
// hands it to commit to move into place, then syncs the directory. commit owns
// the final step (rename to replace, link to create exclusively) and wraps its
// own errors.
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
	return syncRotationStateDirectory(dir)
}
