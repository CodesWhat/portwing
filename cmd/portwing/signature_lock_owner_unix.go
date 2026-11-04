// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/codeswhat/portwing/internal/auth"
)

func checkSignatureLockOwner(dir string, uid int) error {
	path := filepath.Join(dir, auth.SignatureLocksFileName)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		// No file means no locks to clear. Check the directory still exists;
		// the CLI returns without creating state or a sidecar in this case.
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("checking state directory %s: %w", dir, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking ownership of %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine owner of %s", path)
	}
	if int64(stat.Uid) != int64(uid) {
		return fmt.Errorf("%s is owned by UID %d; run as UID %d to avoid changing signature-lock ownership", path, stat.Uid, stat.Uid)
	}
	return nil
}
