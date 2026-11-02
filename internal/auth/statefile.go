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
	dir := filepath.Dir(path)
	f, err := createRotationStateFile(dir, ".state-")
	if err != nil {
		return fmt.Errorf("creating state file: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing state file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing state file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing state file: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("replacing state file: %w", err)
	}
	return syncRotationStateDirectory(dir)
}
