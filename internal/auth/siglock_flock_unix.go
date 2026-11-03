// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"os"
	"syscall"
)

// flockExclusive takes an exclusive advisory lock on path, creating it with
// 0600 if needed, and blocks until it is held. The returned func releases the
// lock. The lock dies with the process, so a crash cannot leave it held.
func flockExclusive(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
