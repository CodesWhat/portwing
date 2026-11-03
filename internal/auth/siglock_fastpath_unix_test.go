// SPDX-License-Identifier: MIT

//go:build !windows && !solaris && !aix

package auth

import "testing"

// Not parallel: it swaps flockCall.
func TestSignatureLocks_LockOnLockedKeySkipsFileLock(t *testing.T) {
	orig := flockCall
	t.Cleanup(func() { flockCall = orig })
	dir := t.TempDir()
	first, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	const key = "aaaaaaaaaaaaaaaa"
	if newly, err := first.Lock(key, lockTestTime); err != nil || !newly {
		t.Fatalf("first Lock = %v, %v; want true, nil", newly, err)
	}

	calls := 0
	flockCall = func(fd, how int) error {
		calls++
		return orig(fd, how)
	}
	if newly, err := first.Lock(key, lockTestTime); err != nil || newly {
		t.Fatalf("second Lock = %v, %v; want false, nil", newly, err)
	}
	if calls != 0 {
		t.Fatalf("flock called %d times on an already-locked key; want 0", calls)
	}
	flockCall = orig

	second, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cleared, err := second.Clear(key); err != nil || !cleared {
		t.Fatalf("external Clear = %v, %v; want true, nil", cleared, err)
	}
	if newly, err := first.Lock(key, lockTestTime); err != nil || !newly {
		t.Fatalf("Lock after external Clear = %v, %v; want true, nil", newly, err)
	}
	third, err := OpenSignatureLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := third.LockedSince(key); !ok {
		t.Fatal("lock did not persist after external Clear")
	}
}
