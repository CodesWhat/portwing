// SPDX-License-Identifier: MIT

//go:build windows || solaris || aix

package auth

// flockExclusive is a no-op where syscall.Flock is unavailable. The agent is
// only released for linux and darwin, so cross-process serialization of the
// signature lock file is not provided on these platforms.
func flockExclusive(string) (func(), error) {
	return func() {}, nil
}
