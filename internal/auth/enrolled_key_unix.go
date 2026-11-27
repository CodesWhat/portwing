// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// geteuid is os.Geteuid, replaceable in tests.
var geteuid = os.Geteuid

// fileOwnerUID reports the owning uid of info, replaceable in tests.
var fileOwnerUID = func(info os.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// openNoFollow opens path without following a symlink at the final component.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	// #nosec G304 -- path is the package-built name under the enrolled key directory.
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// checkOwnedByEuid refuses an object that another user owns.
func checkOwnedByEuid(info os.FileInfo, path, what string) error {
	uid, ok := fileOwnerUID(info)
	if !ok {
		return fmt.Errorf("%s %q: cannot determine its owner", what, path)
	}
	if want := geteuid(); int64(uid) != int64(want) {
		return fmt.Errorf("%s %q is owned by uid %d, not the agent's uid %d", what, path, uid, want)
	}
	return nil
}

// checkPrivateRegularFile fstat-checks an opened file: a regular file, no
// group or other permission bits, owned by the current euid. Checking the
// opened descriptor leaves no window between the check and the use.
func checkPrivateRegularFile(f *os.File, path, what string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("checking %s %q: %w", what, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q is not a regular file", what, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s %q has group or other permissions (mode %04o); run chmod 600 %q", what, path, info.Mode().Perm(), path)
	}
	return checkOwnedByEuid(info, path, what)
}

// checkEnrolledDirOwner refuses an enrolled-key directory another user owns.
func checkEnrolledDirOwner(info os.FileInfo, dir string) error {
	return checkOwnedByEuid(info, dir, "enrolled key directory")
}
