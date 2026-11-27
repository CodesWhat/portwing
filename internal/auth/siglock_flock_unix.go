// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// flockCall is syscall.Flock; tests replace it to reach the retry and failure
// paths.
var flockCall = syscall.Flock

// flockExclusive takes an exclusive advisory lock on path, creating it with
// 0600 if needed, and blocks until it is held. The returned func releases the
// lock. The lock dies with the process, so a crash cannot leave it held.
func flockExclusive(path string) (func(), error) {
	// #nosec G304 -- path is the package-built sidecar next to the signature locks file in the agent's state directory.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return lockOpened(f)
}

// flockEnrolledLock is flockExclusive for the enrolled-key directory lock. It
// refuses a symlink, and an existing lock that is not a regular private file
// owned by the current user, so a peer on a shared volume cannot pre-seed the
// lock and hold it.
func flockEnrolledLock(path string) (func(), error) {
	f, err := openNoFollow(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening enrolled key lock %q (symlinks are refused): %w", path, err)
	}
	if err := checkPrivateRegularFile(f, path, "enrolled key lock"); err != nil {
		_ = f.Close()
		return nil, err
	}
	return lockOpened(f)
}

// lockOpened takes the exclusive lock on f, closing f on failure.
func lockOpened(f *os.File) (func(), error) {
	var err error
	for {
		err = flockCall(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = flockCall(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
