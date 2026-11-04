// SPDX-License-Identifier: MIT

package main

// Windows does not use Unix file ownership. Release binaries target Linux and macOS.
func checkSignatureLockOwner(string, int) error { return nil }
