// SPDX-License-Identifier: MIT

//go:build windows || solaris || aix

package auth

import "os"

// openNoFollow is os.OpenFile where O_NOFOLLOW and ownership checks are not
// available.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	// #nosec G304 -- path is the package-built name under the enrolled key directory.
	return os.OpenFile(path, flag, perm)
}

// checkPrivateRegularFile is a no-op where POSIX modes and owners do not apply.
func checkPrivateRegularFile(*os.File, string, string) error { return nil }

// checkEnrolledDirOwner is a no-op where POSIX owners do not apply.
func checkEnrolledDirOwner(os.FileInfo, string) error { return nil }
